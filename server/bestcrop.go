package main

import (
	_ "embed"
	"image"
	"log"
	"sort"
	"sync"

	pigo "github.com/esimov/pigo/core"
	"github.com/nfnt/resize"
)

// cascadeBytes is pigo's face-detection cascade, embedded so the cropper has no
// runtime file or network dependency (see cascade/README.md for provenance).
//
//go:embed cascade/facefinder
var cascadeBytes []byte

const (
	// analysisMaxSide is the longest side detection runs at. A ~1000 px working
	// copy still resolves a face down to a small fraction of the frame (see
	// minFaceSize) while keeping the cascade sweep - which is quadratic in
	// pixels - to a few tens of milliseconds on a 12 MP original.
	analysisMaxSide = 1000

	// minFaceSize is the smallest detection window, in downscaled pixels, and
	// therefore the smallest face this can find: with analysisMaxSide at 1000,
	// roughly 2% of the long side. It is pigo's own example value and it is
	// deliberately not scaled to the image - deriving it from the image's
	// dimensions instead made the detector blind to anyone not filling the
	// frame, which is exactly the group photo the crop most needs to get right.
	// Smaller than this the cascade mostly reports noise, and the sweep gets
	// expensive fast (window count grows as 1/size^2).
	minFaceSize = 20

	// minFaceQuality is the score a cluster must beat. Note ClusterDetections
	// *sums* the scores of the windows it merged rather than averaging them, so
	// this is not a normalised confidence and it grows with the face's size in
	// the frame - it is a floor on "several windows agreed", not on "the
	// classifier was 5.0 sure". 5.0 is pigo's own example value; lower lets
	// background texture through, and a false face drags the crop somewhere
	// nobody asked for.
	minFaceQuality = 5.0

	// dupCentreFraction is how close two detection centres have to be, as a
	// fraction of the larger box's side, before the later one is treated as a
	// re-detection of the same face rather than a second person. See
	// duplicateDetection.
	dupCentreFraction = 0.3

	shiftFactor = 0.1
	scaleFactor = 1.1
	iouThresh   = 0.2
)

var (
	classifierOnce sync.Once
	classifier     *pigo.Pigo
)

// faceClassifier parses the embedded cascade once. A parse error is impossible
// unless the embedded file is corrupt, so it is logged once and then treated as
// "no detector": every crop degrades to a centered one. A broken cropper must
// never stop the frame from getting a picture.
func faceClassifier() *pigo.Pigo {
	classifierOnce.Do(func() {
		pg, err := pigo.NewPigo().Unpack(cascadeBytes)
		if err != nil {
			log.Printf("warning: face cascade is unusable (%v); every crop will be centered", err)
			return
		}
		classifier = pg
	})
	return classifier
}

// grayDownscale converts img to the 8-bit grayscale plane pigo wants, sampling
// with an integer stride so the longer side is at most analysisMaxSide. This is
// analysis input, not output: nearest-neighbour costs one read per kept pixel
// and does not move a face by enough to matter, whereas a Lanczos pass over a
// 12 MP original would cost more than the detection itself.
//
// A whole-number stride means the analysis resolution falls in steps, not
// smoothly: a 2000 px source keeps a 1000 px plane at stride 2, and one pixel
// more drops it to 667 at stride 3, so the smallest face this can find (see
// minFaceSize) jumps by half again at each cliff. That is accepted rather than
// fixed - smoothing it needs a box filter that reads every source pixel, which
// costs more than the detection the downscale exists to make affordable, and
// the step only shifts the minimum face size between "small" and "slightly
// less small".
//
// It returns the plane, its dimensions, and the stride, so detections can be
// mapped back to full-image coordinates.
func grayDownscale(img image.Image) (pixels []uint8, rows, cols, stride int) {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, 0, 0, 1
	}

	longer := srcW
	if srcH > longer {
		longer = srcH
	}
	stride = (longer + analysisMaxSide - 1) / analysisMaxSide
	if stride < 1 {
		stride = 1
	}

	cols = (srcW + stride - 1) / stride
	rows = (srcH + stride - 1) / stride
	pixels = make([]uint8, rows*cols)

	i := 0
	for y := 0; y < rows; y++ {
		sy := b.Min.Y + y*stride
		for x := 0; x < cols; x++ {
			sx := b.Min.X + x*stride
			r, g, bb, _ := img.At(sx, sy).RGBA()
			// Rec. 601 luma on the 16-bit values image/color hands back,
			// shifted back down to 8 bits.
			pixels[i] = uint8((299*r + 587*g + 114*bb) / 1000 >> 8)
			i++
		}
	}
	return pixels, rows, cols, stride
}

// detectFaces returns the faces found in img, as rectangles in img's own
// coordinate space (which need not start at the origin: a GIF frame or a
// SubImage keeps its parent's coordinates).
func detectFaces(img image.Image) []image.Rectangle {
	pg := faceClassifier()
	if pg == nil {
		return nil
	}

	pixels, rows, cols, stride := grayDownscale(img)
	if rows == 0 || cols == 0 {
		return nil
	}

	// MaxSize comes off the *shorter* side: pigo's detection window is square
	// and has to fit, since it scans rows in [scale/2+1, rows-scale/2-1], so a
	// window wider than the short side runs zero iterations. The same is why
	// the guard below tests the short side - a panorama has plenty of pixels
	// and no room at all.
	shorter := cols
	if rows < shorter {
		shorter = rows
	}
	minSize := minFaceSize
	if minSize+2 > shorter {
		// A thumbnail with no room for the smallest window pigo would scan.
		return nil
	}

	dets := pg.RunCascade(pigo.CascadeParams{
		MinSize:     minSize,
		MaxSize:     shorter,
		ShiftFactor: shiftFactor,
		ScaleFactor: scaleFactor,
		ImageParams: pigo.ImageParams{
			Pixels: pixels,
			Rows:   rows,
			Cols:   cols,
			Dim:    cols,
		},
	}, 0.0)
	dets = pg.ClusterDetections(dets, iouThresh)

	// Highest score first, so the dedup below keeps the best-scoring version of
	// a face. ClusterDetections under-merges - it routinely answers a single
	// face with two or three near-concentric clusters at different scales - and
	// cropAroundFaces takes the union of what it is given, so the duplicates
	// would quietly widen the crop and the log line would triple-count.
	sort.Slice(dets, func(i, j int) bool { return dets[i].Q > dets[j].Q })

	origin := img.Bounds().Min
	var faces []image.Rectangle
	for _, d := range dets {
		if d.Q <= minFaceQuality {
			continue
		}
		// A detection is a circle: (Col, Row) centre, Scale diameter. Take the
		// bounding square, in downscaled pixels, and scale it back up.
		half := d.Scale / 2
		r := image.Rect(
			origin.X+(d.Col-half)*stride,
			origin.Y+(d.Row-half)*stride,
			origin.X+(d.Col+half)*stride,
			origin.Y+(d.Row+half)*stride,
		)
		if duplicateDetection(faces, r) {
			continue
		}
		faces = append(faces, r)
	}
	return faces
}

// duplicateDetection reports whether r is another detection of a face already
// kept, judged by how far apart the two centres are: closer than
// dupCentreFraction of the larger box's side and it is the same face.
//
// The distance is what separates the two cases. ClusterDetections under-merges
// and answers one face with two or three boxes at different scales, but those
// are near-*concentric* - their centres sit almost on top of each other. Two
// real faces are at least a face-width apart even cheek to cheek, and pigo's box
// runs about twice the tight face, so a third of a box is a tight margin for a
// re-detection and a wide one for two people.
//
// Asking instead whether a centre falls inside the other box - the obvious test,
// and what this used to do - throws away real people: because the box is about
// twice the face, a parent's box swallows the centre of the baby they are
// holding and the baby is dropped from the crop entirely.
func duplicateDetection(faces []image.Rectangle, r image.Rectangle) bool {
	rc := centreOf(r)
	for _, f := range faces {
		fc := centreOf(f)
		// The boxes are squares, so either side is "the" side. Measure against
		// the larger of the pair: the spurious re-detections are often a small
		// fraction of the face they sit on (an eye, a mouth), and scaling the
		// threshold to *those* would leave it at a handful of pixels and let
		// every one of them through.
		side := f.Dx()
		if r.Dx() > side {
			side = r.Dx()
		}
		// Chebyshev distance: the detections a cluster leaves behind are offset
		// on both axes at once, and the max of the two is the honest measure of
		// "the same place".
		dist := absInt(rc.X - fc.X)
		if dy := absInt(rc.Y - fc.Y); dy > dist {
			dist = dy
		}
		if float64(dist) < dupCentreFraction*float64(side) {
			return true
		}
	}
	return false
}

func centreOf(r image.Rectangle) image.Point {
	return image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// cropAroundFaces positions the crop over the faces. It only ever chooses
// *where* the window sits: the returned rectangle is always exactly the size
// centerCrop would have returned for the same bounds and ratio, never smaller.
// Zooming in on a face would throw away resolution and, after the exact-size
// resize, show less picture than the panel can hold - the panel must always be
// filled by the largest crop of its aspect ratio that the photo allows.
//
// Within that fixed size the window is placed to show as much face as possible:
// it maximises the face area it covers, each face weighted by its size, and
// among the placements that tie it takes the one nearest the faces' centre.
// That tie-break is what keeps the ordinary case ordinary - when every face fits
// in one window, centring on them covers all of it and wins - while the
// maximisation is what handles a group too wide for the window: a panorama with
// somebody at each end gets one of them whole rather than the empty middle
// ground between them, which is what centring on the span alone returns.
//
// With no faces the answer is exactly centerCrop, which is also the whole
// fallback story.
//
// It is deliberately pure: no detection, no image, so the placement rules are
// testable on their own.
func cropAroundFaces(bounds image.Rectangle, faces []image.Rectangle, width, height int) image.Rectangle {
	crop := centerCrop(bounds, width, height)
	if len(faces) == 0 || crop.Empty() {
		return crop
	}

	// The faces are only interesting where they overlap the image; a detection
	// clipped by the frame edge would otherwise drag the crop off the picture.
	// (Intersect answers the canonical empty rectangle and Union with an empty
	// rectangle is the identity, so no special-casing is needed here.)
	var union image.Rectangle
	clipped := make([]image.Rectangle, 0, len(faces))
	for _, f := range faces {
		f = f.Intersect(bounds)
		if f.Empty() {
			continue
		}
		clipped = append(clipped, f)
		union = union.Union(f)
	}
	if union.Empty() {
		return crop
	}

	// Placement is one-dimensional. centerCrop's window spans the whole image on
	// at least one axis, so at most one axis has any freedom left; the other
	// resolves to bounds.Min either way, through the size >= span shortcut in
	// placeWindow.
	x := placeWindow(bounds.Min.X, bounds.Max.X, crop.Dx(), union.Min.X, union.Max.X, clipped, xAxis)
	y := placeWindow(bounds.Min.Y, bounds.Max.Y, crop.Dy(), union.Min.Y, union.Max.Y, clipped, yAxis)

	return image.Rect(x, y, x+crop.Dx(), y+crop.Dy())
}

// The axis placeWindow projects the faces onto.
type axis int

const (
	xAxis axis = iota
	yAxis
)

// span projects r onto the axis.
func (a axis) span(r image.Rectangle) (min, max int) {
	if a == xAxis {
		return r.Min.X, r.Max.X
	}
	return r.Min.Y, r.Max.Y
}

// placeWindow chooses the offset of a window of the given size inside
// [boundsMin, boundsMax) that covers the most face.
//
// Each face is projected onto the axis and scored by the fraction of that
// interval the window covers, weighted by the face's area so that the big face
// in the foreground outranks a small one in the background. The total is
// piecewise linear in the offset - every face contributes a ramp that starts
// when the window's trailing edge reaches it and flattens once it is fully
// inside - so the maximum is always at a breakpoint. Those are the four offsets
// per face at which the window's edges meet the face's, plus the ends of the
// legal range, plus the union-centred offset, which is included so that it can
// win the tie whenever it is optimal.
//
// Ties go to the candidate nearest the union-centred offset, then to the lowest
// offset, so the answer never depends on the order the detector reported faces.
func placeWindow(boundsMin, boundsMax, size, unionMin, unionMax int, faces []image.Rectangle, a axis) int {
	if size >= boundsMax-boundsMin {
		// No freedom on this axis: the window is the whole image.
		return boundsMin
	}

	clamp := func(v int) int {
		if v > boundsMax-size {
			v = boundsMax - size
		}
		if v < boundsMin {
			v = boundsMin
		}
		return v
	}

	// The old behaviour, and still the preferred answer whenever nothing beats
	// it: the window centred on the middle of the faces.
	centred := clamp((unionMin+unionMax)/2 - size/2)

	best, bestScore := centred, coveredFaceArea(centred, size, faces, a)
	consider := func(v int) {
		v = clamp(v)
		score := coveredFaceArea(v, size, faces, a)
		switch {
		case score > bestScore:
		case score < bestScore:
			return
		default:
			// Equal coverage: prefer the placement closer to the faces' centre,
			// and the leftmost/topmost of those.
			d, bd := absInt(v-centred), absInt(best-centred)
			if d > bd || (d == bd && v >= best) {
				return
			}
		}
		best, bestScore = v, score
	}

	consider(boundsMin)
	consider(boundsMax - size)
	for _, f := range faces {
		min, max := a.span(f)
		consider(min - size) // window ends where the face begins
		consider(max - size) // window ends where the face ends
		consider(min)        // window begins where the face begins
		consider(max)        // window begins where the face ends
	}
	return best
}

// coveredFaceArea is how much face area a window of [offset, offset+size) on
// the given axis shows: for each face, its area times the fraction of its
// projection the window covers.
func coveredFaceArea(offset, size int, faces []image.Rectangle, a axis) float64 {
	end := offset + size
	total := 0.0
	for _, f := range faces {
		min, max := a.span(f)
		lo, hi := min, max
		if lo < offset {
			lo = offset
		}
		if hi > end {
			hi = end
		}
		if hi <= lo {
			continue
		}
		area := float64(f.Dx()) * float64(f.Dy())
		total += area * float64(hi-lo) / float64(max-min)
	}
	return total
}

// centerCrop returns the largest centered sub-rectangle of bounds that has the
// requested aspect ratio. It is the fallback used when the smart cropper is
// unavailable: a frame with a broken cropper should still show photos.
func centerCrop(bounds image.Rectangle, width, height int) image.Rectangle {
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 || width <= 0 || height <= 0 {
		return bounds
	}

	cropW, cropH := srcW, srcW*height/width
	if cropH > srcH {
		cropW, cropH = srcH*width/height, srcH
	}
	if cropW < 1 {
		cropW = 1
	}
	if cropH < 1 {
		cropH = 1
	}

	x := bounds.Min.X + (srcW-cropW)/2
	y := bounds.Min.Y + (srcH-cropH)/2

	return image.Rect(x, y, x+cropW, y+cropH)
}

// snapToAspect confines a crop rectangle to the image and shrinks it to the
// panel's exact aspect ratio. The cropper answers in whole pixels, so its
// rectangle is always a fraction of a percent off the requested ratio, and the
// resize that follows is exact-size - the difference would come out as an
// anisotropic stretch. A rectangle that does not overlap the image at all (a
// confused cropper) degrades to a centered crop of the whole image, which is
// also what the fallback path asks for.
func snapToAspect(r, bounds image.Rectangle, width, height int) image.Rectangle {
	r = r.Intersect(bounds)
	if r.Empty() {
		r = bounds
	}
	return centerCrop(r, width, height)
}

func GetBestPieceOfImage(width, height int, img image.Image) image.Image {
	bounds := img.Bounds()

	bestCrop := centerCrop(bounds, width, height)
	if bestCrop == bounds {
		// The photo already has the panel's aspect ratio, so the window is the
		// whole image and there is no placement left to decide. Running the
		// cascade anyway would cost tens of milliseconds per request to learn
		// nothing - and every 4:3 photo on a 4:3 panel takes this path.
		log.Printf("crop: the crop is the whole image %v; skipping face detection", bestCrop)
	} else if faces := detectFaces(img); len(faces) > 0 {
		bestCrop = cropAroundFaces(bounds, faces, width, height)
		log.Printf("crop: %d face(s) found; crop %v", len(faces), bestCrop)
	} else {
		log.Printf("crop: no faces found; centered crop %v", bestCrop)
	}

	bestCrop = snapToAspect(bestCrop, bounds, width, height)

	type SubImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	croppedImg := img
	if subImager, ok := img.(SubImager); ok {
		croppedImg = subImager.SubImage(bestCrop)
	}

	resizedImg := resize.Resize(uint(width), uint(height), croppedImg, resize.Lanczos3)

	return resizedImg
}
