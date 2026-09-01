package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ===========================================================================
// A fake Immich
//
// There is no live instance to test against — the integration is written from
// the documented API — so the mock is the specification: every assumption this
// code makes about Immich's wire format is written down here, and a future
// Immich that breaks one of them breaks a test with a name that says which.
// ===========================================================================

type immichMock struct {
	server *httptest.Server

	// Responses. assetIDs is consumed one per search; the last entry repeats
	// once exhausted, so a test only lists the ids whose order matters.
	assetIDs     []string
	albums       []immichMockAlbum
	imageBytes   []byte
	searchStatus int
	thumbStatus  int
	albumStatus  int
	// searchBody, when set, is returned by the search endpoint verbatim,
	// bypassing assetIDs. It is how the malformed-response cases are written.
	searchBody string

	mu             sync.Mutex
	searchRequests int
	albumRequests  int
	thumbRequests  int
	searchPayloads []immichRandomSearch
	apiKeys        []string
	thumbPaths     []string
	thumbQueries   []string
}

type immichMockAlbum struct {
	ID   string `json:"id"`
	Name string `json:"albumName"`
}

// newImmichMock starts a fake Immich serving one asset id and a real JPEG.
func newImmichMock(t *testing.T) *immichMock {
	t.Helper()

	m := &immichMock{
		assetIDs:   []string{"11111111-2222-3333-4444-555555555555"},
		imageBytes: encodeJPEG(t, gradientRGBA(64, 48)),
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.server.Close)
	return m
}

func (m *immichMock) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.apiKeys = append(m.apiKeys, r.Header.Get("x-api-key"))
	m.mu.Unlock()

	switch {
	case r.URL.Path == "/api/search/random" && r.Method == http.MethodPost:
		m.serveSearch(w, r)
	case r.URL.Path == "/api/albums" && r.Method == http.MethodGet:
		m.serveAlbums(w)
	case strings.HasPrefix(r.URL.Path, "/api/assets/") && strings.HasSuffix(r.URL.Path, "/thumbnail"):
		m.serveThumbnail(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (m *immichMock) serveSearch(w http.ResponseWriter, r *http.Request) {
	var payload immichRandomSearch
	_ = json.NewDecoder(r.Body).Decode(&payload)

	m.mu.Lock()
	m.searchRequests++
	m.searchPayloads = append(m.searchPayloads, payload)
	n := m.searchRequests
	m.mu.Unlock()

	if m.searchStatus != 0 && m.searchStatus != http.StatusOK {
		http.Error(w, "immich is unwell", m.searchStatus)
		return
	}
	if m.searchBody != "" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, m.searchBody)
		return
	}

	id := m.assetIDs[len(m.assetIDs)-1]
	if n <= len(m.assetIDs) {
		id = m.assetIDs[n-1]
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `[{"id":%q,"type":"IMAGE","originalFileName":"DSC_0001.jpg"}]`, id)
}

func (m *immichMock) serveAlbums(w http.ResponseWriter) {
	m.mu.Lock()
	m.albumRequests++
	m.mu.Unlock()

	if m.albumStatus != 0 && m.albumStatus != http.StatusOK {
		http.Error(w, "immich is unwell", m.albumStatus)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m.albums)
}

func (m *immichMock) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.thumbRequests++
	m.thumbPaths = append(m.thumbPaths, r.URL.Path)
	m.thumbQueries = append(m.thumbQueries, r.URL.RawQuery)
	m.mu.Unlock()

	if m.thumbStatus != 0 && m.thumbStatus != http.StatusOK {
		http.Error(w, "no such asset", m.thumbStatus)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(m.imageBytes)
}

func (m *immichMock) counts() (search, albums, thumbs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.searchRequests, m.albumRequests, m.thumbRequests
}

func (m *immichMock) payloads() []immichRandomSearch {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]immichRandomSearch(nil), m.searchPayloads...)
}

func (m *immichMock) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.apiKeys...)
}

// source builds an immichSource pointed at the mock.
func (m *immichMock) source(t *testing.T, album string) *immichSource {
	t.Helper()
	s, err := newImmichSource(m.server.URL, "test-api-key", album)
	if err != nil {
		t.Fatalf("newImmichSource: %v", err)
	}
	return s
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// ===========================================================================
// The happy path
// ===========================================================================

func TestImmichNextImage(t *testing.T) {
	m := newImmichMock(t)
	src := m.source(t, "")

	img, name, err := src.NextImage(context.Background())
	if err != nil {
		t.Fatalf("NextImage: %v", err)
	}
	if img == nil {
		t.Fatal("NextImage returned no image and no error")
	}
	if got, want := img.Bounds().Dx(), 64; got != want {
		t.Errorf("image is %d wide, want %d", got, want)
	}
	if want := "immich:" + m.assetIDs[0]; name != want {
		t.Errorf("name %q, want %q", name, want)
	}

	// The API key has to be on every request, or Immich answers 401 and the
	// frame silently falls back forever.
	for i, key := range m.keys() {
		if key != "test-api-key" {
			t.Errorf("request %d carried x-api-key %q, want %q", i, key, "test-api-key")
		}
	}

	payloads := m.payloads()
	if len(payloads) != 1 {
		t.Fatalf("got %d searches, want 1", len(payloads))
	}
	if payloads[0].Size != 1 || payloads[0].Type != "IMAGE" || payloads[0].Visibility != "timeline" {
		t.Errorf("search body %+v, want size 1, type IMAGE, visibility timeline", payloads[0])
	}
	if len(payloads[0].AlbumIDs) != 0 {
		t.Errorf("search body carried albumIds %v with no album configured", payloads[0].AlbumIDs)
	}

	// The preview endpoint, not /original: the original may be HEIC or RAW.
	m.mu.Lock()
	paths, queries := m.thumbPaths, m.thumbQueries
	m.mu.Unlock()
	if len(paths) != 1 || paths[0] != "/api/assets/"+m.assetIDs[0]+"/thumbnail" {
		t.Errorf("thumbnail paths %v, want one /api/assets/<id>/thumbnail", paths)
	}
	if len(queries) != 1 || queries[0] != "size=preview" {
		t.Errorf("thumbnail queries %v, want [size=preview]", queries)
	}
}

// A base URL entered with a trailing slash is the most likely way for someone
// to configure this, and it must not produce //api/... paths.
func TestImmichTrailingSlashInBaseURL(t *testing.T) {
	m := newImmichMock(t)
	src, err := newImmichSource(m.server.URL+"/", "test-api-key", "")
	if err != nil {
		t.Fatalf("newImmichSource: %v", err)
	}
	if _, _, err := src.NextImage(context.Background()); err != nil {
		t.Fatalf("NextImage: %v", err)
	}
}

// ===========================================================================
// Albums
// ===========================================================================

func TestImmichAlbumByNameResolvesOnce(t *testing.T) {
	m := newImmichMock(t)
	m.albums = []immichMockAlbum{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Name: "Other"},
		{ID: "aaaaaaaa-0000-0000-0000-000000000002", Name: "Living Room"},
	}
	// Case-insensitively: the name is typed twice by a human, weeks apart.
	src := m.source(t, "living room")

	for i := 0; i < 2; i++ {
		if _, _, err := src.NextImage(context.Background()); err != nil {
			t.Fatalf("NextImage %d: %v", i, err)
		}
	}

	if _, albums, _ := m.counts(); albums != 1 {
		t.Errorf("listed albums %d times across two fetches, want 1 (the id is cached)", albums)
	}
	for i, payload := range m.payloads() {
		if len(payload.AlbumIDs) != 1 || payload.AlbumIDs[0] != m.albums[1].ID {
			t.Errorf("search %d carried albumIds %v, want [%s]", i, payload.AlbumIDs, m.albums[1].ID)
		}
	}
}

// A configured UUID is used as-is: listing every album to look up an id we were
// already given is a request per process for nothing.
func TestImmichAlbumByUUIDSkipsTheAlbumList(t *testing.T) {
	m := newImmichMock(t)
	const albumID = "aaaaaaaa-0000-0000-0000-000000000002"
	src := m.source(t, albumID)

	if _, _, err := src.NextImage(context.Background()); err != nil {
		t.Fatalf("NextImage: %v", err)
	}

	if _, albums, _ := m.counts(); albums != 0 {
		t.Errorf("listed albums %d times, want 0", albums)
	}
	payloads := m.payloads()
	if len(payloads) != 1 || len(payloads[0].AlbumIDs) != 1 || payloads[0].AlbumIDs[0] != albumID {
		t.Errorf("search bodies %+v, want one carrying albumIds [%s]", payloads, albumID)
	}
}

func TestImmichUnknownAlbumNameIsAnError(t *testing.T) {
	m := newImmichMock(t)
	m.albums = []immichMockAlbum{{ID: "aaaaaaaa-0000-0000-0000-000000000001", Name: "Other"}}
	src := m.source(t, "Living Room")

	_, _, err := src.NextImage(context.Background())
	if err == nil {
		t.Fatal("expected an error for an album that does not exist")
	}
	if !strings.Contains(err.Error(), "Living Room") {
		t.Errorf("error %q should name the album that was not found", err)
	}
	if search, _, _ := m.counts(); search != 0 {
		t.Errorf("searched %d times despite the album being unresolvable, want 0", search)
	}
}

// A failed resolution must not be cached: an Immich that was still starting up,
// or an album created a minute later, has to be picked up on the next wake
// rather than at the next server restart.
func TestImmichAlbumResolutionRetriedAfterFailure(t *testing.T) {
	m := newImmichMock(t)
	m.albumStatus = http.StatusInternalServerError
	src := m.source(t, "Living Room")

	if _, _, err := src.NextImage(context.Background()); err == nil {
		t.Fatal("expected an error while the album list is failing")
	}

	m.albumStatus = 0
	m.albums = []immichMockAlbum{{ID: "aaaaaaaa-0000-0000-0000-000000000002", Name: "Living Room"}}
	if _, _, err := src.NextImage(context.Background()); err != nil {
		t.Fatalf("NextImage after the album list recovered: %v", err)
	}
	if _, albums, _ := m.counts(); albums != 2 {
		t.Errorf("listed albums %d times, want 2 (the failure must not be cached)", albums)
	}
}

// ===========================================================================
// No-repeat
// ===========================================================================

func TestImmichRetriesOnceWhenTheDrawRepeats(t *testing.T) {
	m := newImmichMock(t)
	const (
		first  = "11111111-1111-1111-1111-111111111111"
		second = "22222222-2222-2222-2222-222222222222"
	)
	m.assetIDs = []string{first, first, second}
	src := m.source(t, "")

	if _, name, err := src.NextImage(context.Background()); err != nil || name != "immich:"+first {
		t.Fatalf("first NextImage: name %q, err %v", name, err)
	}
	if _, name, err := src.NextImage(context.Background()); err != nil || name != "immich:"+second {
		t.Fatalf("second NextImage: name %q, err %v; the repeat should have been re-drawn", name, err)
	}

	if search, _, _ := m.counts(); search != 3 {
		t.Errorf("made %d searches, want 3 (one per fetch plus exactly one re-draw)", search)
	}
}

// An album with one photo in it repeats every time, and re-drawing forever
// would mean never showing anything: the second draw is accepted as-is.
func TestImmichAcceptsTheRepeatAfterOneRetry(t *testing.T) {
	m := newImmichMock(t)
	const only = "11111111-1111-1111-1111-111111111111"
	m.assetIDs = []string{only}
	src := m.source(t, "")

	for i := 0; i < 2; i++ {
		if _, name, err := src.NextImage(context.Background()); err != nil || name != "immich:"+only {
			t.Fatalf("NextImage %d: name %q, err %v", i, name, err)
		}
	}
	if search, _, thumbs := m.counts(); search != 3 || thumbs != 2 {
		t.Errorf("made %d searches and %d downloads, want 3 and 2", search, thumbs)
	}
}

// ===========================================================================
// Failure modes
// ===========================================================================

func TestImmichErrors(t *testing.T) {
	const validID = "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name    string
		setup   func(m *immichMock)
		wantErr string
	}{
		{
			name:    "search answers 500",
			setup:   func(m *immichMock) { m.searchStatus = http.StatusInternalServerError },
			wantErr: "500",
		},
		{
			name:    "search answers an empty array",
			setup:   func(m *immichMock) { m.searchBody = `[]` },
			wantErr: "no assets matched",
		},
		{
			name:    "search answers something that is not an array",
			setup:   func(m *immichMock) { m.searchBody = `{"assets":{"items":[]}}` },
			wantErr: "decoding the search result",
		},
		{
			name:    "search answers an asset with no id",
			setup:   func(m *immichMock) { m.searchBody = `[{"type":"IMAGE"}]` },
			wantErr: "no asset id",
		},
		{
			name:    "search answers an id that is not a UUID",
			setup:   func(m *immichMock) { m.searchBody = `[{"id":"../../admin"}]` },
			wantErr: "not a UUID",
		},
		{
			name: "search answers more JSON than the cap allows",
			setup: func(m *immichMock) {
				m.searchBody = `[{"id":"` + validID + `","pad":"` + strings.Repeat("x", immichJSONLimit) + `"}]`
			},
			wantErr: "exceeds",
		},
		{
			name:    "the preview 404s",
			setup:   func(m *immichMock) { m.thumbStatus = http.StatusNotFound },
			wantErr: "404",
		},
		{
			name:    "the preview is not an image",
			setup:   func(m *immichMock) { m.imageBytes = []byte("this is not an image") },
			wantErr: "decoding",
		},
		{
			name:    "the preview is empty",
			setup:   func(m *immichMock) { m.imageBytes = nil },
			wantErr: "empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newImmichMock(t)
			tc.setup(m)
			src := m.source(t, "")

			img, _, err := src.NextImage(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if img != nil {
				t.Errorf("expected no image alongside the error, got %v", img.Bounds())
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should mention %q", err, tc.wantErr)
			}
		})
	}
}

// A redirect is never followed, because the API key rides in a custom header
// and Go's default policy only strips the *standard* auth headers when a
// redirect crosses to another host: x-api-key would be handed, in full, to
// whatever answered the 302 — a captive portal, an expired domain, a proxy
// somebody else now controls. None of the routes used here redirects, so the
// 3xx is reported as the misconfiguration it is.
func TestImmichDoesNotFollowRedirects(t *testing.T) {
	var mu sync.Mutex
	var elsewhereRequests int
	var elsewhereKeys []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhereRequests++
		elsewhereKeys = append(elsewhereKeys, r.Header.Get("x-api-key"))
		mu.Unlock()
		fmt.Fprint(w, `[{"id":"11111111-2222-3333-4444-555555555555"}]`)
	}))
	t.Cleanup(elsewhere.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	src, err := newImmichSource(redirector.URL, "test-api-key", "")
	if err != nil {
		t.Fatalf("newImmichSource: %v", err)
	}

	_, _, err = src.NextImage(context.Background())
	if err == nil {
		t.Fatal("expected an error rather than a followed redirect")
	}
	if !strings.Contains(err.Error(), "302") {
		t.Errorf("error %q should name the 3xx status it refused to follow", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if elsewhereRequests != 0 {
		t.Errorf("the redirect target saw %d requests carrying keys %v; the API key must never leave the configured host", elsewhereRequests, elsewhereKeys)
	}
}

// A body that dies mid-read must not hide the status that explains it: a 500
// reported as "reading the response: connection reset" sends whoever is
// debugging it looking at the network instead of at Immich's log.
func TestImmichReadFailureStillReportsTheStatus(t *testing.T) {
	// Content-Length promises far more than the handler writes, so the server
	// closes the connection and the client's read fails part-way through.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "immich is unwell")
	}))
	t.Cleanup(server.Close)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)

	src, err := newImmichSource(server.URL, "test-api-key", "")
	if err != nil {
		t.Fatalf("newImmichSource: %v", err)
	}

	_, _, err = src.NextImage(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should name the 500 rather than only the read failure", err)
	}
}

// Nothing listening at all is the case a misconfigured URL or a rebooting
// Immich produces, and it must be an ordinary error rather than a hang.
func TestImmichUnreachableInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := server.URL
	server.Close()

	src, err := newImmichSource(addr, "test-api-key", "")
	if err != nil {
		t.Fatalf("newImmichSource: %v", err)
	}
	if _, _, err := src.NextImage(context.Background()); err == nil {
		t.Fatal("expected an error against a closed server")
	}
}

// A cancelled request — the frame gave up and disconnected — must not be
// waited out.
func TestImmichHonoursContextCancellation(t *testing.T) {
	m := newImmichMock(t)
	src := m.source(t, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := src.NextImage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
}

// ===========================================================================
// EXIF
//
// Immich previews are normally already rotated, but the transcode is the
// admin's setting and an original that kept its orientation tag must not come
// out sideways here when the same file read off disk comes out upright.
// ===========================================================================

func TestImmichPreviewHonoursExifOrientation(t *testing.T) {
	m := newImmichMock(t)
	// Orientation 6 is "rotate 90° clockwise", so a 4x2 source is a 2x4 photo.
	m.imageBytes = jpegWithSegments(t, gradientRGBA(4, 2),
		exifAPP1(orientationTIFF(binary.BigEndian, 6)))
	src := m.source(t, "")

	img, _, err := src.NextImage(context.Background())
	if err != nil {
		t.Fatalf("NextImage: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 2 || got.Dy() != 4 {
		t.Errorf("got bounds %v, want a 2x4 image (orientation 6 applied)", got)
	}
}

// ===========================================================================
// fallbackSource
// ===========================================================================

func TestFallbackSourceServesLocalWhenImmichFails(t *testing.T) {
	tests := []struct {
		name    string
		primary func(t *testing.T) ImageSource
	}{
		{
			name: "immich answers 500",
			primary: func(t *testing.T) ImageSource {
				m := newImmichMock(t)
				m.searchStatus = http.StatusInternalServerError
				return m.source(t, "")
			},
		},
		{
			name: "nothing is listening",
			primary: func(t *testing.T) ImageSource {
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				addr := server.URL
				server.Close()
				src, err := newImmichSource(addr, "test-api-key", "")
				if err != nil {
					t.Fatalf("newImmichSource: %v", err)
				}
				return src
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTempPNG(t, dir, "only.png", gradientRGBA(64, 48))

			src := fallbackSource{primary: tc.primary(t), fallback: &localDirSource{dir: dir}}
			_, name, err := src.NextImage(context.Background())
			if err != nil {
				t.Fatalf("NextImage: %v", err)
			}
			if !strings.HasSuffix(name, "only.png") {
				t.Errorf("served %q, want the local file", name)
			}
		})
	}
}

func TestFallbackSourcePrefersThePrimary(t *testing.T) {
	dir := t.TempDir()
	writeTempPNG(t, dir, "only.png", gradientRGBA(64, 48))

	m := newImmichMock(t)
	src := fallbackSource{primary: m.source(t, ""), fallback: &localDirSource{dir: dir}}
	_, name, err := src.NextImage(context.Background())
	if err != nil {
		t.Fatalf("NextImage: %v", err)
	}
	if !strings.HasPrefix(name, "immich:") {
		t.Errorf("served %q, want the Immich asset", name)
	}
}

// A client that hung up is not a reason to go and read a file it will never
// read: the cancellation is returned rather than fallen back from.
func TestFallbackSourceDoesNotFallBackForACancelledRequest(t *testing.T) {
	dir := t.TempDir()
	writeTempPNG(t, dir, "only.png", gradientRGBA(64, 48))

	m := newImmichMock(t)
	src := fallbackSource{primary: m.source(t, ""), fallback: &localDirSource{dir: dir}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := src.NextImage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
}

// Both sources failing is the only case that reaches the handler's error path.
func TestFallbackSourceFailsWhenBothFail(t *testing.T) {
	m := newImmichMock(t)
	m.searchStatus = http.StatusInternalServerError

	src := fallbackSource{primary: m.source(t, ""), fallback: &localDirSource{dir: t.TempDir()}}
	if _, _, err := src.NextImage(context.Background()); err == nil {
		t.Fatal("expected an error when neither source can produce an image")
	}
}

// The end-to-end shape that matters: a frame asking for a picture while Immich
// is down still gets a full, correctly sized frame buffer rather than a 500.
func TestFetchImageFallsBackToTheLocalDirectory(t *testing.T) {
	silenceStdout(t)
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	m := newImmichMock(t)
	m.searchStatus = http.StatusInternalServerError
	withImageSource(t, fallbackSource{primary: m.source(t, ""), fallback: &localDirSource{dir: imgDir}})

	const w, h = 16, 8
	rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d", rec.Body.Len(), want)
	}
}

// The same, over the wire from Immich itself.
func TestFetchImageServesAnImmichAsset(t *testing.T) {
	silenceStdout(t)
	imgDir := t.TempDir()
	withImageDir(t, imgDir) // deliberately empty: only Immich can answer
	m := newImmichMock(t)
	withImageSource(t, fallbackSource{primary: m.source(t, ""), fallback: &localDirSource{dir: imgDir}})

	const w, h = 16, 8
	rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d", rec.Body.Len(), want)
	}
}

// ===========================================================================
// Configuration
// ===========================================================================

func TestImmichSourceFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		key      string
		album    string
		wantNil  bool
		wantErr  string
		wantBase string
	}{
		{name: "unset means no Immich at all", wantNil: true},
		{name: "url and key", url: "https://photos.example.com", key: "k", wantBase: "https://photos.example.com"},
		{name: "trailing slashes are trimmed", url: "https://photos.example.com//", key: "k", wantBase: "https://photos.example.com"},
		{name: "a url with no key is refused", url: "https://photos.example.com", wantErr: "IMMICH_API_KEY"},
		{name: "a non-http url is refused", url: "ftp://photos.example.com", key: "k", wantErr: "http://"},
		{name: "a bare hostname is refused", url: "photos.example.com", key: "k", wantErr: "http://"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("IMMICH_URL", tc.url)
			t.Setenv("IMMICH_API_KEY", tc.key)
			t.Setenv("IMMICH_ALBUM", tc.album)

			src, err := immichSourceFromEnv()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q should mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("immichSourceFromEnv: %v", err)
			}
			if tc.wantNil {
				if src != nil {
					t.Fatalf("got a source for an unset IMMICH_URL: %+v", src)
				}
				return
			}
			if src == nil {
				t.Fatal("got no source and no error")
			}
			if src.baseURL != tc.wantBase {
				t.Errorf("baseURL %q, want %q", src.baseURL, tc.wantBase)
			}
		})
	}
}

// The startup log line has to say where the pictures come from without ever
// printing the API key.
func TestImmichAlbumDescription(t *testing.T) {
	for _, tc := range []struct{ album, want string }{
		{"", "whole library"},
		{"Living Room", `album "Living Room"`},
	} {
		src, err := newImmichSource("https://photos.example.com", "super-secret", tc.album)
		if err != nil {
			t.Fatalf("newImmichSource: %v", err)
		}
		got := src.albumDescription()
		if got != tc.want {
			t.Errorf("albumDescription() = %q, want %q", got, tc.want)
		}
		if strings.Contains(got, "super-secret") {
			t.Errorf("albumDescription() leaked the API key: %q", got)
		}
	}
}
