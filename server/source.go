package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// An ImageSource produces the next decoded, upright photo to show.
//
// The frame asks for a picture and gets one; where it came from is this
// interface's business and nothing downstream of it — crop, resize, dither,
// pack — knows or cares. That split is what lets a second source exist at all
// without touching the pipeline that has been tuned against the panels.
type ImageSource interface {
	NextImage(ctx context.Context) (img image.Image, name string, err error)
}

// localDirSource draws from a directory of files on disk. It is the original
// behaviour and stays the default: no configuration, no network, and the only
// thing that can stop it is an empty or unreadable directory.
//
// Used through a pointer, because it carries the no-repeat memory below.
type localDirSource struct {
	dir string

	mu sync.Mutex
	// lastFile is the file the previous request served. The draw has no memory
	// of its own, so a small directory shows the same photo twice in a row
	// surprisingly often (1 in 20 for 20 images); remembering the last file and
	// trying it last removes the back-to-back repeat. It is one value per
	// source, so with several frames pointed at one server the guarantee
	// weakens to "not the last picture this server served" — and, as with the
	// Immich source's own lastAssetID, a fetch from the other source leaves it
	// alone, so falling back for one cycle does not cost the memory.
	// Guarded by a mutex: chi serves every request in its own goroutine.
	lastFile string
}

// NextImage ignores ctx: reading a local file is a syscall away and pickImage's
// walk is bounded by the file count, so there is nothing long-running here to
// cancel. Taking the context anyway keeps every source interchangeable.
func (s *localDirSource) NextImage(ctx context.Context) (image.Image, string, error) {
	return s.pickImage()
}

func (s *localDirSource) rememberServed(path string) {
	s.mu.Lock()
	s.lastFile = path
	s.mu.Unlock()
}

func (s *localDirSource) lastServed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFile
}

// fallbackSource tries primary and, if that fails for any reason, serves from
// fallback instead.
//
// This exists because of how a picture frame fails. A frame that gets no image
// keeps showing yesterday's picture, and a stale frame looks exactly like a
// healthy one — nobody notices for days. So a remote source being down, slow,
// misconfigured, or answering something unparseable must never be the reason a
// refresh is missed: the photos on the server's own disk are always there, and
// showing one of those is strictly better than showing nothing new. The failure
// is logged once per request rather than swallowed, so the reason is in the
// journal when somebody does go looking.
type fallbackSource struct {
	primary  ImageSource
	fallback ImageSource
}

func (s fallbackSource) NextImage(ctx context.Context) (image.Image, string, error) {
	img, name, err := s.primary.NextImage(ctx)
	if err == nil {
		return img, name, nil
	}
	// A cancelled request is the client having given up, not the source being
	// broken: there is nobody left to show a fallback picture to, and reading
	// one off the disk would be work done for a connection that is already gone.
	if ctx.Err() != nil {
		return nil, "", err
	}
	// Named by role rather than by implementation: the wrapper knows nothing
	// about which source is which, and the wrapped error already says which
	// component failed.
	log.Printf("warning: primary image source failed (%v); falling back", err)
	return s.fallback.NextImage(ctx)
}

// decodableExtensions are the file suffixes image.Decode can handle with the
// decoders this package registers. A .heic exported from a phone, a .mp4 or a
// stray .txt is a guaranteed decode failure, so it is not drawn while anything
// better is available.
var decodableExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".jpe":  true,
	".jfif": true,
	".gif":  true,
	".webp": true,
}

// imageCandidates lists the files in dir worth trying, in the order to try
// them, with last — the file the caller served previously, or "" — moved to the
// back. Sub-directories are not pictures, and dotfiles are either metadata
// (.DS_Store) or rsync's partial transfers, so neither is a candidate at all.
// Everything else is: the files whose extension the registered decoders
// understand come first, and the rest follow rather than being dropped, because
// image.Decode goes by content — an extension-less export is still a picture,
// and it must not be unreachable just because one .jpg in the directory happens
// to be corrupt.
func imageCandidates(dir string, last string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var decodable, rest []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if decodableExtensions[strings.ToLower(filepath.Ext(name))] {
			decodable = append(decodable, path)
		} else {
			rest = append(rest, path)
		}
	}

	files := append(drawOrder(decodable, last), drawOrder(rest, last)...)
	if len(files) == 0 {
		return nil, fmt.Errorf("no usable image files in %q: %w", dir, os.ErrNotExist)
	}

	return files, nil
}

// drawOrder shuffles files and moves last, the previously served file, to the
// back — to the back rather than out of the list, so that a one-image directory
// keeps serving its one image and a directory whose other files are all broken
// still falls back to the one that works.
func drawOrder(files []string, last string) []string {
	rand.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })

	if last == "" || len(files) < 2 {
		return files
	}
	for i, f := range files {
		if f == last {
			copy(files[i:], files[i+1:])
			files[len(files)-1] = last
			break
		}
	}
	return files
}

// pickImage returns a decoded image from the source's directory, walking past
// the candidates that fail to decode. A file can be undecodable despite its
// name (truncated, half-written mid-rsync, misnamed), and a single such file
// used to cost the frame a whole sleep cycle — up to hours of stale picture —
// because the request 500'd instead of trying another file. Only a directory
// where nothing at all decodes is an error; the walk is bounded by the
// candidate count, and each rejection costs one open plus a failed format sniff.
func (s *localDirSource) pickImage() (image.Image, string, error) {
	candidates, err := imageCandidates(s.dir, s.lastServed())
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for _, path := range candidates {
		img, err := ReadImage(path)
		if err == nil {
			s.rememberServed(path)
			return img, path, nil
		}
		log.Printf("skipping unusable image %q: %v", path, err)
		lastErr = err
	}

	// candidates is never empty, so a decode error is always set here.
	return nil, "", lastErr
}
