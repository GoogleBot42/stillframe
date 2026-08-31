package main

import (
	"bytes"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nfnt/resize"
)

// ===========================================================================
// cropAroundFaces
//
// The placement rules are a pure function of the geometry, so they are tested
// without running the detector at all.
// ===========================================================================

func TestCropAroundFaces(t *testing.T) {
	cases := []struct {
		name          string
		bounds        image.Rectangle
		faces         []image.Rectangle
		width, height int
		want          image.Rectangle
	}{
		{
			// No faces at all is the fallback path: exactly centerCrop.
			name:   "no faces is a centered crop",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  nil,
			width:  1, height: 1,
			want: image.Rect(25, 0, 75, 50),
		},
		{
			// A square crop of a 100x50 source is 50 wide. The face sits at
			// x=[0,20), so a crop centered on it would start at -15; it must
			// clamp to the left edge and still contain the whole face.
			name:   "a face at the left edge clamps inside the image",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  []image.Rectangle{image.Rect(0, 10, 20, 30)},
			width:  1, height: 1,
			want: image.Rect(0, 0, 50, 50),
		},
		{
			name:   "a face at the right edge clamps inside the image",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  []image.Rectangle{image.Rect(80, 10, 100, 30)},
			width:  1, height: 1,
			want: image.Rect(50, 0, 100, 50),
		},
		{
			// Two faces 30 px apart, union [30,70) -> centre 50, crop 50 wide.
			name:   "two spread faces are both covered",
			bounds: image.Rect(0, 0, 100, 50),
			faces: []image.Rectangle{
				image.Rect(30, 10, 40, 20),
				image.Rect(60, 30, 70, 40),
			},
			width: 1, height: 1,
			want: image.Rect(25, 0, 75, 50),
		},
		{
			// Union [5,95) is wider than the 50 px crop, so no window holds both
			// faces. This used to centre on the union's middle, which shows
			// [25,75): the empty ground between the two people. The placement
			// now maximises covered face area instead, so it takes a window with
			// a whole face in it - and of the several that tie at one whole face
			// (offsets 0, 5, 45 and 50) the tie-break picks the one nearest the
			// union centre, then the leftmost, which is 5.
			name:   "faces wider than the crop keep one of them whole",
			bounds: image.Rect(0, 0, 100, 50),
			faces: []image.Rectangle{
				image.Rect(5, 10, 15, 20),
				image.Rect(85, 10, 95, 20),
			},
			width: 1, height: 1,
			want: image.Rect(5, 0, 55, 50),
		},
		{
			// A detection clipped by the frame edge must not drag the crop off
			// the picture: only the part inside bounds counts.
			name:   "a face partly outside bounds is clamped, not obeyed",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  []image.Rectangle{image.Rect(-40, -30, 10, 20)},
			width:  1, height: 1,
			want: image.Rect(0, 0, 50, 50),
		},
		{
			name:   "a face entirely outside bounds degrades to a centered crop",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  []image.Rectangle{image.Rect(500, 500, 600, 600)},
			width:  1, height: 1,
			want: image.Rect(25, 0, 75, 50),
		},
		{
			// Portrait panel, landscape source: the crop is 25x50 and slides
			// horizontally onto the face.
			name:   "portrait target on a landscape source",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  []image.Rectangle{image.Rect(70, 10, 90, 30)},
			width:  1, height: 2,
			want: image.Rect(68, 0, 93, 50),
		},
		{
			// Landscape panel, portrait source: the crop is 50x25 and slides
			// vertically onto the face.
			name:   "landscape target on a portrait source",
			bounds: image.Rect(0, 0, 50, 100),
			faces:  []image.Rectangle{image.Rect(10, 70, 30, 90)},
			width:  2, height: 1,
			want: image.Rect(0, 68, 50, 93),
		},
		{
			// The image's own origin is not always (0,0) - a GIF frame or a
			// SubImage keeps its parent's coordinates.
			name:   "honours a non-zero origin",
			bounds: image.Rect(100, 200, 200, 250),
			faces:  []image.Rectangle{image.Rect(100, 210, 120, 230)},
			width:  1, height: 1,
			want: image.Rect(100, 200, 150, 250),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cropAroundFaces(tc.bounds, tc.faces, tc.width, tc.height)
			if got != tc.want {
				t.Errorf("cropAroundFaces(%v, %v, %d, %d) = %v, want %v",
					tc.bounds, tc.faces, tc.width, tc.height, got, tc.want)
			}
			if !got.In(tc.bounds) {
				t.Errorf("%v is not inside the image %v", got, tc.bounds)
			}
			if want := centerCrop(tc.bounds, tc.width, tc.height); got.Dx() != want.Dx() || got.Dy() != want.Dy() {
				t.Errorf("%v is not the largest crop of the ratio (%v)", got, want)
			}
		})
	}
}

// A face that fits inside the crop must end up entirely inside it, wherever in
// the image it sits. Sweeping every position is what catches an off-by-one in
// the clamp that a handful of cases would miss.
func TestCropAroundFacesKeepsASingleFaceWhole(t *testing.T) {
	bounds := image.Rect(0, 0, 200, 100) // square crop is 100x100
	for x := 0; x <= 180; x += 5 {
		face := image.Rect(x, 40, x+20, 60)
		got := cropAroundFaces(bounds, []image.Rectangle{face}, 1, 1)
		if !face.In(got) {
			t.Fatalf("face %v is not inside crop %v", face, got)
		}
	}
}

// The panorama case the union-centring rule got wrong: two people at opposite
// ends of a very wide picture, with nothing but scenery between them. Centring
// the window on the middle of their span returns a crop containing no face at
// all, which is the worst possible answer - it is neither person and it is not
// even the picture's own centre. Whatever the placement chooses, at least one
// face has to be whole in it.
func TestCropAroundFacesCoversAFaceOnAWidePanorama(t *testing.T) {
	bounds := image.Rect(0, 0, 3000, 400) // 400x400 crop, 2600 px of freedom
	faces := []image.Rectangle{
		image.Rect(0, 150, 100, 250),
		image.Rect(1400, 150, 1500, 250),
	}

	got := cropAroundFaces(bounds, faces, 400, 400)

	if got.Dx() != 400 || got.Dy() != 400 {
		t.Fatalf("crop %v is %dx%d, want the full-size 400x400", got, got.Dx(), got.Dy())
	}
	covered := 0
	for _, f := range faces {
		if f.In(got) {
			covered++
		}
	}
	if covered == 0 {
		t.Errorf("crop %v contains none of the faces %v - the empty middle again", got, faces)
	}
	// Specifically not the old answer, which was the 400 px window centred on
	// the union of [0,1500): x=550, showing the scenery between the two.
	if got.Min.X == 550 {
		t.Errorf("crop %v is the old union-centred placement", got)
	}
}

// Backward compatibility for the ordinary case: when one window can hold every
// face, the union-centred placement covers all of it, so it ties with every
// other full-coverage placement and the tie-break has to pick it. Anything else
// would slide the group off-centre for no gain.
func TestCropAroundFacesStillCentersWhenEveryFaceFits(t *testing.T) {
	cases := []struct {
		name   string
		bounds image.Rectangle
		faces  []image.Rectangle
		w, h   int
	}{
		{
			// Two faces 30 px apart in a 100x50 source: union [30,70) fits
			// inside the 50 px square crop with room to spare.
			name:   "two faces a short way apart",
			bounds: image.Rect(0, 0, 100, 50),
			faces:  []image.Rectangle{image.Rect(30, 10, 40, 20), image.Rect(60, 30, 70, 40)},
			w:      1, h: 1,
		},
		{
			name:   "a single face mid-frame",
			bounds: image.Rect(0, 0, 1000, 500),
			faces:  []image.Rectangle{image.Rect(600, 200, 700, 300)},
			w:      1, h: 1,
		},
		{
			name:   "three faces in a row",
			bounds: image.Rect(0, 0, 1200, 600),
			faces: []image.Rectangle{
				image.Rect(400, 200, 460, 260),
				image.Rect(500, 210, 570, 280),
				image.Rect(620, 190, 700, 270),
			},
			w: 4, h: 3,
		},
		{
			// Different-sized faces: the area weighting must not tug the window
			// towards the big one when centring already covers both.
			name:   "a big face and a small one",
			bounds: image.Rect(0, 0, 900, 400),
			faces:  []image.Rectangle{image.Rect(300, 100, 460, 260), image.Rect(500, 180, 540, 220)},
			w:      1, h: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cropAroundFaces(tc.bounds, tc.faces, tc.w, tc.h)

			want := unionCenteredCrop(tc.bounds, tc.faces, tc.w, tc.h)
			if got != want {
				t.Errorf("cropAroundFaces = %v, want the union-centred %v", got, want)
			}
			for _, f := range tc.faces {
				if !f.In(got) {
					t.Errorf("face %v is not inside the crop %v", f, got)
				}
			}
		})
	}
}

// unionCenteredCrop is the placement cropAroundFaces used to do unconditionally:
// the full-size window centred on the union of the faces and clamped into the
// image. It is still the required answer whenever it covers every face.
func unionCenteredCrop(bounds image.Rectangle, faces []image.Rectangle, width, height int) image.Rectangle {
	crop := centerCrop(bounds, width, height)
	var union image.Rectangle
	for _, f := range faces {
		union = union.Union(f.Intersect(bounds))
	}
	if union.Empty() {
		return crop
	}
	place := func(lo, hi, size, boundsMin, boundsMax int) int {
		v := (lo+hi)/2 - size/2
		if v+size > boundsMax {
			v = boundsMax - size
		}
		if v < boundsMin {
			v = boundsMin
		}
		return v
	}
	x := place(union.Min.X, union.Max.X, crop.Dx(), bounds.Min.X, bounds.Max.X)
	y := place(union.Min.Y, union.Max.Y, crop.Dy(), bounds.Min.Y, bounds.Max.Y)
	return image.Rect(x, y, x+crop.Dx(), y+crop.Dy())
}

// Placement must not depend on the order the detector happened to report the
// faces in - two runs of the same picture would otherwise disagree.
func TestCropAroundFacesIsOrderIndependent(t *testing.T) {
	bounds := image.Rect(0, 0, 2000, 500)
	faces := []image.Rectangle{
		image.Rect(100, 100, 200, 200),
		image.Rect(900, 150, 1050, 300),
		image.Rect(1800, 120, 1880, 200),
	}
	want := cropAroundFaces(bounds, faces, 1, 1)
	for _, order := range [][]int{{2, 1, 0}, {1, 0, 2}, {0, 2, 1}, {2, 0, 1}} {
		shuffled := make([]image.Rectangle, len(order))
		for i, j := range order {
			shuffled[i] = faces[j]
		}
		if got := cropAroundFaces(bounds, shuffled, 1, 1); got != want {
			t.Errorf("order %v: got %v, want %v", order, got, want)
		}
	}
}

// The area weighting is what makes "as much face as possible" mean the face
// that dominates the picture, not whichever one is listed first: a big
// foreground face beats a small background one when the window can only hold
// one of them.
func TestCropAroundFacesPrefersTheLargerFaceWhenOnlyOneFits(t *testing.T) {
	bounds := image.Rect(0, 0, 3000, 400)
	small := image.Rect(100, 150, 160, 210)
	big := image.Rect(1500, 50, 1900, 380)

	got := cropAroundFaces(bounds, []image.Rectangle{small, big}, 1, 1)

	if !big.In(got) {
		t.Errorf("crop %v does not contain the large face %v", got, big)
	}
}

func TestCropAroundFacesEmptyIsCenterCrop(t *testing.T) {
	bounds := image.Rect(0, 0, 64, 48)
	if got, want := cropAroundFaces(bounds, nil, 32, 24), centerCrop(bounds, 32, 24); got != want {
		t.Errorf("no faces: got %v, want the centered crop %v", got, want)
	}
	if got, want := cropAroundFaces(bounds, []image.Rectangle{}, 3, 4), centerCrop(bounds, 3, 4); got != want {
		t.Errorf("empty slice: got %v, want the centered crop %v", got, want)
	}
}

// The crop always fills the panel: its job is to choose *where* the largest
// rectangle of the requested aspect ratio sits, never how big it is. Zooming in
// on a small or tightly clustered face would throw away resolution and, once
// snapToAspect and the resize are done with it, show less picture than the
// panel could hold. So for every face layout the answer must be exactly the
// size centerCrop would have returned - only the offset may differ.
func TestCropAroundFacesNeverChangesTheCropSize(t *testing.T) {
	boundsCases := []image.Rectangle{
		image.Rect(0, 0, 1000, 750),
		image.Rect(0, 0, 750, 1000),
		image.Rect(0, 0, 1000, 1000),
		image.Rect(0, 0, 3000, 400),
		image.Rect(-40, -90, 260, 110),
		image.Rect(0, 0, 3, 1),
	}
	faceCases := map[string][]image.Rectangle{
		"none":              nil,
		"empty slice":       {},
		"one tiny":          {image.Rect(10, 10, 14, 14)},
		"one huge":          {image.Rect(-500, -500, 5000, 5000)},
		"corner":            {image.Rect(0, 0, 30, 30)},
		"two clustered":     {image.Rect(100, 100, 130, 130), image.Rect(135, 105, 165, 135)},
		"two far apart":     {image.Rect(0, 0, 30, 30), image.Rect(900, 700, 930, 730)},
		"all outside":       {image.Rect(9000, 9000, 9100, 9100)},
		"degenerate rect":   {image.Rect(50, 50, 50, 50)},
		"inverted rect":     {image.Rect(90, 90, 40, 40)},
		"three in a corner": {image.Rect(5, 5, 15, 15), image.Rect(20, 5, 30, 15), image.Rect(5, 20, 15, 30)},
	}
	sizes := [][2]int{{800, 480}, {480, 800}, {1, 1}, {1872, 1404}, {3, 7}}

	for _, bounds := range boundsCases {
		for name, faces := range faceCases {
			for _, s := range sizes {
				w, h := s[0], s[1]
				center := centerCrop(bounds, w, h)
				got := cropAroundFaces(bounds, faces, w, h)

				if got.Dx() != center.Dx() || got.Dy() != center.Dy() {
					t.Errorf("bounds %v, faces %q, %dx%d: crop %v is %dx%d, want the full-size %dx%d (%v)",
						bounds, name, w, h, got, got.Dx(), got.Dy(), center.Dx(), center.Dy(), center)
				}
				if !got.In(bounds) {
					t.Errorf("bounds %v, faces %q, %dx%d: crop %v escapes the image", bounds, name, w, h, got)
				}
				// GetBestPieceOfImage runs the result through snapToAspect,
				// which is the one other place the rectangle could shrink. It
				// may only give up the sub-pixel rounding residue - at most one
				// pixel per axis, and without sliding the crop off the faces.
				snapped := snapToAspect(got, bounds, w, h)
				if snapped.Dx() > got.Dx() || snapped.Dy() > got.Dy() {
					t.Errorf("bounds %v, faces %q, %dx%d: snapToAspect grew %v to %v", bounds, name, w, h, got, snapped)
				}
				if 100*snapped.Dx() < 99*got.Dx() || 100*snapped.Dy() < 99*got.Dy() {
					t.Errorf("bounds %v, faces %q, %dx%d: snapToAspect shrank %v to %v - more than the sub-pixel ratio residue",
						bounds, name, w, h, got, snapped)
				}
			}
		}
	}
}

// grayDownscale is the only place the analysis resolution is decided, and an
// off-by-one there silently drops the last row or column of the picture.
func TestGrayDownscale(t *testing.T) {
	for _, size := range [][2]int{
		{1, 1}, {3, 7}, {999, 400}, {1000, 1000}, {1001, 500},
		{1999, 999}, {2000, 1500}, {2001, 100}, {4000, 3000},
	} {
		w, h := size[0], size[1]
		// A non-zero origin, because that is the case that gets sampling wrong.
		img := image.NewRGBA(image.Rect(37, 11, 37+w, 11+h))

		pixels, rows, cols, stride := grayDownscale(img)

		longer := w
		if h > longer {
			longer = h
		}
		wantStride := (longer + analysisMaxSide - 1) / analysisMaxSide
		if wantStride < 1 {
			wantStride = 1
		}
		if stride != wantStride {
			t.Errorf("%dx%d: stride %d, want %d", w, h, stride, wantStride)
		}
		// Ceiling division: flooring would drop the last partial column, which
		// is a whole edge of the picture on a 2001-wide source.
		if want := (w + stride - 1) / stride; cols != want {
			t.Errorf("%dx%d: cols %d, want %d", w, h, cols, want)
		}
		if want := (h + stride - 1) / stride; rows != want {
			t.Errorf("%dx%d: rows %d, want %d", w, h, rows, want)
		}
		if len(pixels) != rows*cols {
			t.Errorf("%dx%d: %d pixels for a %dx%d plane", w, h, len(pixels), cols, rows)
		}
		if cols > analysisMaxSide || rows > analysisMaxSide {
			t.Errorf("%dx%d: analysis plane %dx%d exceeds %d", w, h, cols, rows, analysisMaxSide)
		}
		// The last sample must still be a pixel of the image, not past its end.
		if last := image.Pt(37+(cols-1)*stride, 11+(rows-1)*stride); !last.In(img.Bounds()) {
			t.Errorf("%dx%d: last sample %v is outside %v", w, h, last, img.Bounds())
		}
	}
}

// The luma must actually reflect the pixels, and land in 0..255 for the
// extremes - pigo indexes tables with it.
func TestGrayDownscaleLuma(t *testing.T) {
	cases := []struct {
		name string
		c    color.RGBA
		want uint8
	}{
		{"black", color.RGBA{0, 0, 0, 255}, 0},
		{"white", color.RGBA{255, 255, 255, 255}, 255},
		{"mid gray", color.RGBA{128, 128, 128, 255}, 128},
		{"pure red", color.RGBA{255, 0, 0, 255}, 76},
		{"pure green", color.RGBA{0, 255, 0, 255}, 149},
		{"pure blue", color.RGBA{0, 0, 255, 255}, 29},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pixels, _, _, _ := grayDownscale(solidRGBA(4, 4, tc.c))
			for _, p := range pixels {
				if int(p) < int(tc.want)-1 || int(p) > int(tc.want)+1 {
					t.Fatalf("luma %d, want ~%d", p, tc.want)
				}
			}
		})
	}
}

// ===========================================================================
// detectFaces
//
// testdata/sample.jpg is pigo's own 320x400 sample; the cascade is the one
// embedded in the binary, so this exercises the real detector.
// ===========================================================================

func loadSample(t *testing.T) image.Image {
	t.Helper()
	f, err := os.Open("testdata/sample.jpg")
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	return img
}

// sampleFaceBox is where the face in testdata/sample.jpg actually is, measured
// by eye from the 320x400 original. The detector's box is the cascade's square
// around the same face, so it should land close to this - asserting only
// "some rectangle came back" would pass with the coordinate mapping inverted,
// the wrong scale, or the origin translation dropped.
var sampleFaceBox = image.Rect(95, 120, 215, 270)

func TestDetectFacesFindsTheSampleFace(t *testing.T) {
	src := loadSample(t)

	started := time.Now()
	faces := detectFaces(src)
	t.Logf("found %d face(s) in %s: %v", len(faces), time.Since(started).Round(time.Millisecond), faces)

	// One face in the picture, and pigo's clustering is deduplicated, so one
	// rectangle out. Two would mean the concentric re-detections are back.
	if len(faces) != 1 {
		t.Fatalf("got %d faces in testdata/sample.jpg, want exactly 1: %v", len(faces), faces)
	}
	got := faces[0]

	if !got.In(src.Bounds()) {
		t.Errorf("detection %v is not inside the image %v", got, src.Bounds())
	}
	if got.Dx() != got.Dy() {
		t.Errorf("detection %v is not square; the Scale-to-rect conversion is wrong", got)
	}
	// The cascade's square is roughly the head, so it must contain the face and
	// not be wildly larger than it. Both halves matter: dropping the /2 in
	// `half := d.Scale / 2` doubles the box and still contains the face.
	if !sampleFaceBox.In(got) {
		t.Errorf("detection %v does not contain the face at %v", got, sampleFaceBox)
	}
	if got.Dx() > 3*sampleFaceBox.Dx() {
		t.Errorf("detection %v is %d px wide, far larger than the %d px face", got, got.Dx(), sampleFaceBox.Dx())
	}
}

// The analysis downscale must not lose the face. Feeding the sample scaled up
// by 2x, 3x and 4x drives grayDownscale onto each of its stride branches and
// the detection must still land on the same part of the picture.
func TestDetectFacesSurvivesTheAnalysisDownscale(t *testing.T) {
	src := loadSample(t)
	b := src.Bounds()

	for _, mult := range []int{2, 3, 4} {
		big := resize.Resize(uint(b.Dx()*mult), uint(b.Dy()*mult), src, resize.Lanczos3)

		faces := detectFaces(big)
		if len(faces) != 1 {
			t.Errorf("%dx upscale: got %d faces, want 1: %v", mult, len(faces), faces)
			continue
		}
		// The same face, in the scaled image's coordinates.
		want := image.Rect(
			sampleFaceBox.Min.X*mult, sampleFaceBox.Min.Y*mult,
			sampleFaceBox.Max.X*mult, sampleFaceBox.Max.Y*mult,
		)
		if !want.In(faces[0]) {
			t.Errorf("%dx upscale: detection %v does not contain the face at %v", mult, faces[0], want)
		}
	}
}

// A 12 MP phone photo is the realistic worst case, and it is the case the
// analysis downscale exists for: the whole crop decision happens inline in the
// /fetchImage handler while a battery-powered frame holds the connection open.
// Timed as a benchmark rather than asserted as a deadline - a wall-clock
// assertion in a unit test only flakes on a loaded CI runner.
func BenchmarkDetectFacesOn12MP(b *testing.B) {
	src := gradientRGBA(4000, 3000)
	detectFaces(gradientRGBA(64, 64)) // pay the one-time cascade unpack first
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detectFaces(src)
	}
}

func BenchmarkGetBestPieceOfImageOnTheSample(b *testing.B) {
	f, err := os.Open("testdata/sample.jpg")
	if err != nil {
		b.Fatalf("open sample: %v", err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		b.Fatalf("decode sample: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetBestPieceOfImage(800, 480, src)
	}
}

// The dedup rule decides which detections survive, and it is the difference
// between "one face, found three times" and "a parent and the baby they are
// holding".
func TestDuplicateDetection(t *testing.T) {
	// A box of side px centred on (cx, cy) - the shape detectFaces builds from
	// pigo's circle.
	box := func(cx, cy, side int) image.Rectangle {
		h := side / 2
		return image.Rect(cx-h, cy-h, cx+h, cy+h)
	}

	cases := []struct {
		name  string
		kept  []image.Rectangle
		r     image.Rectangle
		isDup bool
	}{
		{
			// The regression this rule exists for. pigo's box is roughly twice
			// the tight face, so the small face's centre lands inside the large
			// box even though the two people are plainly side by side. The old
			// centre-in-box test dropped the small one.
			name:  "a small face beside a large one is a second person",
			kept:  []image.Rectangle{box(300, 300, 240)},
			r:     box(410, 300, 120),
			isDup: false,
		},
		{
			// The case the rule is for: one face, clustered twice at different
			// scales, centres 10 px apart.
			name:  "near-concentric boxes at different scales are one face",
			kept:  []image.Rectangle{box(300, 300, 280)},
			r:     box(310, 300, 200),
			isDup: true,
		},
		{
			// The real numbers from a 4x upscale of testdata/sample.jpg: the
			// cascade answers the one face with its 928 px box and an 84 px
			// sliver of it. This is why the threshold scales with the *larger*
			// box - a third of the sliver is 25 px, which nothing would ever
			// trip, and the sliver would come back as a second "face" dragging
			// the crop towards an eyebrow.
			name:  "a sliver detection inside a big face is the same face",
			kept:  []image.Rectangle{box(628, 800, 928)},
			r:     box(460, 724, 84),
			isDup: true,
		},
		{
			name:  "identical boxes are one face",
			kept:  []image.Rectangle{box(300, 300, 200)},
			r:     box(300, 300, 200),
			isDup: true,
		},
		{
			// Offset on both axes at once - the Chebyshev distance is 55, past
			// 0.3 x 100.
			name:  "a diagonal neighbour is a second person",
			kept:  []image.Rectangle{box(300, 300, 100)},
			r:     box(345, 355, 100),
			isDup: false,
		},
		{
			name:  "nothing kept yet is never a duplicate",
			kept:  nil,
			r:     box(300, 300, 200),
			isDup: false,
		},
		{
			// Only one of the kept boxes has to match.
			name:  "matches any kept box, not just the first",
			kept:  []image.Rectangle{box(50, 50, 100), box(300, 300, 200)},
			r:     box(305, 295, 180),
			isDup: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := duplicateDetection(tc.kept, tc.r); got != tc.isDup {
				t.Errorf("duplicateDetection(%v, %v) = %v, want %v", tc.kept, tc.r, got, tc.isDup)
			}
		})
	}
}

// A blank image has nothing to find; the detector must say so rather than
// inventing a face out of flat gray.
func TestDetectFacesFindsNothingInAFlatImage(t *testing.T) {
	if faces := detectFaces(solidRGBA(320, 240, color.RGBA{128, 128, 128, 255})); len(faces) != 0 {
		t.Errorf("found %d face(s) in a flat gray image: %v", len(faces), faces)
	}
}

// A picture too small to hold a minFaceSize detection window must return
// cleanly rather than asking pigo for a nonsensical scale range.
func TestDetectFacesOnATinyImage(t *testing.T) {
	if faces := detectFaces(gradientRGBA(8, 6)); len(faces) != 0 {
		t.Errorf("found %d face(s) in an 8x6 image", len(faces))
	}
}

// The crop the pipeline actually picks must contain the face the detector
// found - for both panel orientations.
func TestGetBestPieceOfImageKeepsTheSampleFace(t *testing.T) {
	src := loadSample(t)
	faces := detectFaces(src)
	if len(faces) == 0 {
		t.Fatal("no faces found in testdata/sample.jpg")
	}

	for _, size := range [][2]int{{800, 480}, {480, 800}} {
		w, h := size[0], size[1]

		crop := cropAroundFaces(src.Bounds(), faces, w, h)
		for _, f := range faces {
			center := image.Pt((f.Min.X+f.Max.X)/2, (f.Min.Y+f.Max.Y)/2)
			if !center.In(crop) {
				t.Errorf("%dx%d: face centre %v is outside the crop %v", w, h, center, crop)
			}
		}

		got := GetBestPieceOfImage(w, h, src)
		if want := image.Rect(0, 0, w, h); got.Bounds() != want {
			t.Errorf("%dx%d: got %v, want %v", w, h, got.Bounds(), want)
		}
	}
}

// ===========================================================================
// GetBestPieceOfImage
// ===========================================================================

func TestGetBestPieceOfImageResizesToRequestedSize(t *testing.T) {
	src := gradientRGBA(64, 48)
	got := GetBestPieceOfImage(32, 24, src)
	if got.Bounds() != image.Rect(0, 0, 32, 24) {
		t.Errorf("got %v, want a 32x24 image anchored at the origin", got.Bounds())
	}
}

// wideRedBlue is a 90x30 image with red edges and a blue middle third: a square
// crop of it keeps the blue if and only if the crop is centered.
func wideRedBlue() *image.RGBA {
	src := image.NewRGBA(image.Rect(0, 0, 90, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 90; x++ {
			if x >= 30 && x < 60 {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
	}
	return src
}

// With no face in the picture the crop is centered, not a corner.
func TestGetBestPieceOfImageWithoutFacesKeepsTheCenter(t *testing.T) {
	got := GetBestPieceOfImage(8, 8, wideRedBlue())
	r, _, b, _ := got.At(4, 4).RGBA()
	if b <= r {
		t.Errorf("the centered crop should keep the blue middle, got r=%d b=%d", r, b)
	}
}

// A photo that already has the panel's aspect ratio leaves the crop no freedom
// at all, so the detector is skipped: the sweep would cost tens of milliseconds
// to learn nothing. The picture must still come out at the requested size.
func TestGetBestPieceOfImageSkipsDetectionWhenTheCropIsTheWholeImage(t *testing.T) {
	var logs bytes.Buffer
	restore := captureLog(t, &logs)

	// The sample has a real, findable face in it, so this would detect if the
	// shortcut were missing. 320x400 asked for at 160x200 is the same ratio.
	got := GetBestPieceOfImage(160, 200, loadSample(t))
	restore()

	if want := image.Rect(0, 0, 160, 200); got.Bounds() != want {
		t.Errorf("got %v, want %v", got.Bounds(), want)
	}
	if !strings.Contains(logs.String(), "skipping face detection") {
		t.Errorf("detection was not skipped; log said %q", logs.String())
	}
}

// captureLog redirects the standard logger into buf and returns a function that
// puts it back (also deferred, so a failing assertion cannot leave it hijacked).
func captureLog(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	restored := false
	restore := func() {
		if !restored {
			log.SetOutput(out)
			log.SetFlags(flags)
			restored = true
		}
	}
	t.Cleanup(restore)
	return restore
}

// An image whose bounds do not start at the origin (a GIF frame anchored at its
// frame offset, a SubImage) must round-trip through the whole crop path: the
// detector reports in the image's own space and SubImage selects the region the
// cropper actually chose, not an empty one.
func TestGetBestPieceOfImageOnNonZeroOrigin(t *testing.T) {
	// 32x16 anchored at (100,50): left half red, right half blue.
	src := image.NewRGBA(image.Rect(100, 50, 132, 66))
	for y := 50; y < 66; y++ {
		for x := 100; x < 132; x++ {
			if x < 116 {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}

	got := GetBestPieceOfImage(8, 8, src)
	if got.Bounds() != image.Rect(0, 0, 8, 8) {
		t.Fatalf("got %v, want an 8x8 image", got.Bounds())
	}
	// No faces here, so the crop is the centered 16x16 square straddling the
	// colour boundary: the left column is red and the right column is blue.
	rl, _, bl, _ := got.At(1, 4).RGBA()
	rr, _, br, _ := got.At(6, 4).RGBA()
	if bl >= rl {
		t.Errorf("left of the centered crop should be red, got r=%d b=%d", rl, bl)
	}
	if br <= rr {
		t.Errorf("right of the centered crop should be blue, got r=%d b=%d", rr, br)
	}
}

// The sample face lives in a SubImage of a larger canvas: the detection has to
// come back in the sub-image's coordinates for the crop to select it.
func TestGetBestPieceOfImageOnASubImage(t *testing.T) {
	sample := loadSample(t)
	b := sample.Bounds()

	// Paint the sample into the middle of a bigger canvas, then take the
	// sub-image back out - which keeps the parent's non-zero coordinates.
	canvas := image.NewRGBA(image.Rect(0, 0, b.Dx()+200, b.Dy()+200))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			canvas.Set(100+x, 100+y, sample.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	sub := canvas.SubImage(image.Rect(100, 100, 100+b.Dx(), 100+b.Dy()))

	if sub.Bounds().Min != image.Pt(100, 100) {
		t.Fatalf("sub-image bounds %v, want a (100,100) origin", sub.Bounds())
	}
	// Compare against the same detection on the untranslated original. Merely
	// checking the result overlaps the sub-image is not enough: an untranslated
	// detection still lands inside these bounds, so that assertion passes even
	// with the origin offset dropped entirely.
	want := detectFaces(sample)
	if len(want) != 1 {
		t.Fatalf("got %d faces in the untranslated sample, want 1", len(want))
	}
	faces := detectFaces(sub)
	if len(faces) != 1 {
		t.Fatalf("got %d faces in the translated sample, want 1: %v", len(faces), faces)
	}
	// Not exact equality: the sample sits on a larger black canvas here, which
	// moves pigo's scan grid and its border neighbourhoods by a few pixels. A
	// tolerance of a few percent is still two orders of magnitude tighter than
	// the 100 px the missing translation would cost.
	expect := want[0].Add(image.Pt(100, 100))
	const tol = 10
	got := faces[0]
	if absInt(got.Min.X-expect.Min.X) > tol || absInt(got.Min.Y-expect.Min.Y) > tol ||
		absInt(got.Max.X-expect.Max.X) > tol || absInt(got.Max.Y-expect.Max.Y) > tol {
		t.Errorf("detection %v, want within %d px of %v (the original %v shifted by the sub-image origin)",
			got, tol, expect, want[0])
	}

	resized := GetBestPieceOfImage(800, 480, sub)
	if want := image.Rect(0, 0, 800, 480); resized.Bounds() != want {
		t.Errorf("got %v, want %v", resized.Bounds(), want)
	}
}

func TestGetBestPieceOfImageClampsOversizedCropRect(t *testing.T) {
	// snapToAspect intersects with the image bounds, so an over-large rect can
	// never reach SubImage - but keep the end-to-end assertion.
	got := GetBestPieceOfImage(16, 8, gradientRGBA(32, 32))
	if got.Bounds() != image.Rect(0, 0, 16, 8) {
		t.Errorf("got %v, want a 16x8 image", got.Bounds())
	}
}

// ===========================================================================
// centerCrop / snapToAspect geometry
// ===========================================================================

func TestCenterCrop(t *testing.T) {
	cases := []struct {
		name          string
		bounds        image.Rectangle
		width, height int
		want          image.Rectangle
	}{
		{"same aspect ratio uses the whole image", image.Rect(0, 0, 64, 48), 32, 24, image.Rect(0, 0, 64, 48)},
		{"source wider than target crops the sides", image.Rect(0, 0, 100, 50), 1, 1, image.Rect(25, 0, 75, 50)},
		{"source taller than target crops top and bottom", image.Rect(0, 0, 50, 100), 1, 1, image.Rect(0, 25, 50, 75)},
		{"honours a non-zero origin", image.Rect(10, 20, 110, 70), 1, 1, image.Rect(35, 20, 85, 70)},
		{"degenerate source is returned unchanged", image.Rect(0, 0, 0, 0), 8, 8, image.Rect(0, 0, 0, 0)},
		{"degenerate target is returned unchanged", image.Rect(0, 0, 8, 8), 0, 0, image.Rect(0, 0, 8, 8)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := centerCrop(tc.bounds, tc.width, tc.height); got != tc.want {
				t.Errorf("centerCrop(%v, %d, %d) = %v, want %v", tc.bounds, tc.width, tc.height, got, tc.want)
			}
		})
	}
}

// The cropper answers in whole pixels, so its rectangle is never exactly the
// panel's aspect ratio; the exact-size resize would turn the difference into an
// anisotropic stretch, so the rect is snapped before it is used.
func TestSnapToAspect(t *testing.T) {
	bounds := image.Rect(0, 0, 1000, 1000)
	cases := []struct {
		name          string
		r             image.Rectangle
		width, height int
		want          image.Rectangle
	}{
		// 605x809 is the kind of rounding a whole-pixel cropper produces for a
		// 1200x1600 panel: 0.17% narrow, snapped down to an exact 3:4.
		{"rounds a slightly-off ratio down", image.Rect(0, 0, 605, 809), 1200, 1600, image.Rect(0, 1, 605, 807)},
		{"leaves an exact ratio alone", image.Rect(10, 20, 610, 820), 600, 800, image.Rect(10, 20, 610, 820)},
		{"clamps an overhanging rect", image.Rect(500, 500, 2000, 2000), 1, 1, image.Rect(500, 500, 1000, 1000)},
		{"a rect off the image degrades to the centered crop", image.Rect(2000, 2000, 3000, 3000), 2, 1, image.Rect(0, 250, 1000, 750)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapToAspect(tc.r, bounds, tc.width, tc.height)
			if got != tc.want {
				t.Errorf("snapToAspect(%v, %v, %d, %d) = %v, want %v", tc.r, bounds, tc.width, tc.height, got, tc.want)
			}
			if got.Empty() || !got.In(bounds) {
				t.Fatalf("%v is not a usable region of %v", got, bounds)
			}
			// Whole-pixel crops cannot always hit the ratio dead on, but the
			// residual must be far below the 0.29% the raw cropper rectangle
			// carries - that is what shows up as a stretch after the resize.
			if e := aspectError(got, tc.width, tc.height); e > 0.002 {
				t.Errorf("%v is %d:%d, %.3f%% off the requested %d:%d", got, got.Dx(), got.Dy(), e*100, tc.width, tc.height)
			}
		})
	}
}

// aspectError is how far r's aspect ratio is from width:height, as a fraction.
func aspectError(r image.Rectangle, width, height int) float64 {
	want := float64(width) / float64(height)
	got := float64(r.Dx()) / float64(r.Dy())
	return math.Abs(got-want) / want
}
