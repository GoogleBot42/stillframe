package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// immichSource draws a random photo from an Immich instance
// (https://immich.app), optionally restricted to one album.
//
// API assumptions, all against the documented REST API as of Immich v1.13x
// (the endpoints below have been stable since roughly v1.118):
//
//   - POST /api/search/random answers a JSON *array* of asset objects. Older
//     builds (before the endpoint was introduced in the v1.11x series) have no
//     such route at all and will 404; that is a configuration error the
//     fallback handles rather than something to paper over here.
//   - GET /api/assets/{id}/thumbnail?size=preview answers the server's own
//     transcode of the photo. This is deliberately not /original: an iPhone
//     library is mostly HEIC and a camera dump is mostly RAW, neither of which
//     Go can decode, and the preview is a few hundred KB rather than tens of
//     megabytes. Immich renamed this route from the singular /api/asset/... in
//     v1.106, so instances older than that will 404 here.
//   - GET /api/albums answers an array of albums carrying at least id and
//     albumName.
//   - Authentication is the x-api-key header, which Immich accepts on every
//     one of these routes.
//
// Unknown fields in a request body are ignored by Immich's validation layer
// for these endpoints, so sending "visibility" to an instance predating that
// field is harmless; a field that changes meaning would not be, which is why
// the body below stays as small as it can be.
type immichSource struct {
	// baseURL is the instance root with any trailing slash removed, e.g.
	// "https://photos.example.com". The /api prefix is added per request.
	// base is the same value parsed once at construction: every request path is
	// built from it with (*url.URL).JoinPath rather than by re-parsing a string,
	// so the only place a malformed IMMICH_URL can be reported is startup.
	baseURL string
	base    *url.URL
	apiKey  string
	// album is empty (the whole library), a UUID, or an album name to resolve.
	album  string
	client *http.Client

	mu sync.Mutex
	// albumID caches the UUID that album resolved to. Only ever set to a
	// successful resolution: a failure must not be remembered, or renaming an
	// album — or restarting Immich a minute before the server — would poison
	// the process until someone noticed the frames were stale.
	albumID string
	// lastAssetID is the asset the previous successful fetch served. Each source
	// keeps its own no-repeat memory (the directory source has the same field
	// under a different name): sharing one slot between the two would mean a
	// single fallback to the local directory erased Immich's memory, and vice
	// versa, which is precisely when the repeat is most visible.
	lastAssetID string
}

const (
	// immichTimeout bounds one HTTP request end to end. The frame is awake and
	// burning battery while it waits, and its own HTTP client gives up
	// eventually too, so a hung Immich has to become a fallback quickly rather
	// than holding a conversion slot open.
	immichTimeout = 30 * time.Second

	// immichJSONLimit caps the search endpoint, whose body is a single asset
	// object — a couple of kilobytes at most. 64 KiB is generous for that and
	// small enough that a misconfigured URL pointing at something that streams
	// forever cannot be read into the heap.
	immichJSONLimit = 64 << 10

	// immichAlbumListLimit caps the album list, which is the one response whose
	// size grows with the library rather than being a fixed shape: an album
	// object runs several hundred bytes, so 64 KiB would stop resolving names
	// somewhere around a hundred albums — and it would fail by quietly falling
	// back to the local directory forever, which is the worst way to fail.
	// 4 MiB is thousands of albums and still a bounded read.
	immichAlbumListLimit = 4 << 20

	// immichImageLimit caps the preview download. Previews run 1-2 MB, but the
	// preview of a stitched panorama, or an instance configured to transcode
	// previews at original resolution, is legitimately far larger; 64 MiB
	// admits those while still bounding what one request can allocate.
	immichImageLimit = 64 << 20

	// immichSnippetLimit is how much of an error response is quoted back into
	// the log line. Enough to recognise "invalid api key" or an HTML proxy
	// error page, not enough to fill the journal.
	immichSnippetLimit = 200

	// immichDrainLimit is how much of an unread response body is swallowed to
	// keep the connection reusable. Only whatever is left of an ordinary
	// response has to fit: an error page is a few kilobytes, and 256 KiB covers
	// even a chatty proxy's without giving a hostile or broken peer a second
	// budget the size of the download cap.
	immichDrainLimit = 256 << 10
)

// uuidPattern matches the canonical 8-4-4-4-12 form. It decides whether
// IMMICH_ALBUM is an id or a name, and it validates ids coming back from the
// API before they are pasted into a URL path.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// immichSourceFromEnv builds the Immich source described by the environment,
// or (nil, nil) when IMMICH_URL is unset — which is the default, and the case
// where this whole file may as well not exist.
//
// The configuration is environment-only on purpose: the binary's positional
// arguments (port, image directory) are interpolated by server/service.nix and
// adding a third would break every deployment that already passes two.
func immichSourceFromEnv() (*immichSource, error) {
	base := strings.TrimSpace(os.Getenv("IMMICH_URL"))
	if base == "" {
		return nil, nil
	}
	return newImmichSource(base, strings.TrimSpace(os.Getenv("IMMICH_API_KEY")), strings.TrimSpace(os.Getenv("IMMICH_ALBUM")))
}

func newImmichSource(baseURL, apiKey, album string) (*immichSource, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("IMMICH_URL %q is not a URL: %w", baseURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("IMMICH_URL %q must be an http:// or https:// URL", baseURL)
	}
	if apiKey == "" {
		// Every route used here requires authentication, so this would fail on
		// the first fetch anyway; failing at startup names the actual problem.
		return nil, fmt.Errorf("IMMICH_API_KEY must be set when IMMICH_URL is")
	}

	return &immichSource{
		baseURL: baseURL,
		base:    parsed,
		apiKey:  apiKey,
		album:   album,
		// One client, shared: it owns the connection pool, so a per-request
		// client would open a fresh TCP+TLS connection for every wake.
		client: &http.Client{
			Timeout: immichTimeout,
			// Redirects are refused outright, because the API key travels in a
			// custom header. Go's default policy strips only Authorization,
			// Www-Authenticate and the Cookie headers when a redirect crosses to
			// another host; anything else the caller set — x-api-key here — is
			// re-sent verbatim to whatever the Location pointed at. A captive
			// portal, an expired domain or a proxy someone else controls could
			// therefore collect a full-access Immich key just by answering 302.
			// None of the three routes used here legitimately redirects, so an
			// IMMICH_URL that does is a misconfiguration (http:// where the
			// instance wants https://, most likely) and is better surfaced as an
			// error: the non-2xx path below reports the 3xx status.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// albumDescription is for the startup log line. The API key never appears in
// it, or anywhere else that is logged.
func (s *immichSource) albumDescription() string {
	if s.album == "" {
		return "whole library"
	}
	return fmt.Sprintf("album %q", s.album)
}

func (s *immichSource) NextImage(ctx context.Context) (image.Image, string, error) {
	albumID, err := s.resolveAlbum(ctx)
	if err != nil {
		return nil, "", err
	}

	id, err := s.randomAssetID(ctx, albumID)
	if err != nil {
		return nil, "", err
	}

	// The random search has no memory, so a library of n photos repeats
	// back-to-back one time in n — always, for a single-asset album. One retry
	// removes the common case cheaply; a second would not help much and would
	// double the latency for the album that has nothing else to offer, so
	// whatever the retry returns is accepted, repeat or not.
	if id == s.previousAsset() {
		retry, err := s.randomAssetID(ctx, albumID)
		if err != nil {
			log.Printf("immich: re-drawing to avoid repeating asset %s failed (%v); showing it again", id, err)
		} else {
			id = retry
		}
	}

	data, err := s.fetchPreview(ctx, id)
	if err != nil {
		return nil, "", err
	}

	img, err := decodeUpright(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("immich asset %s: %w", id, err)
	}

	s.rememberAsset(id)
	return img, "immich:" + id, nil
}

func (s *immichSource) previousAsset() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAssetID
}

func (s *immichSource) rememberAsset(id string) {
	s.mu.Lock()
	s.lastAssetID = id
	s.mu.Unlock()
}

// resolveAlbum turns the configured album into the UUID the search endpoint
// wants, returning "" when no album is configured (search the whole library).
//
// A name is resolved by listing the albums once and caching the answer for the
// life of the process: album ids never change, and doing this on every wake
// would double the request count for no information. A *failed* resolution is
// not cached — see the albumID field.
func (s *immichSource) resolveAlbum(ctx context.Context) (string, error) {
	if s.album == "" {
		return "", nil
	}
	if uuidPattern.MatchString(s.album) {
		return s.album, nil
	}

	s.mu.Lock()
	cached := s.albumID
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}

	// The lookup runs outside the lock. Two concurrent first requests can both
	// perform it, which costs one extra HTTP call and nothing else; holding a
	// mutex across a 30 s network call to prevent that would be the worse
	// trade, since it would serialise every frame behind the slowest one.
	body, err := s.get(ctx, immichAlbumListLimit, "", "api", "albums")
	if err != nil {
		return "", fmt.Errorf("listing albums: %w", err)
	}

	var albums []struct {
		ID   string `json:"id"`
		Name string `json:"albumName"`
	}
	if err := json.Unmarshal(body, &albums); err != nil {
		return "", fmt.Errorf("decoding the album list: %w", err)
	}

	for _, album := range albums {
		// Case-insensitive: album names are typed by a human in one UI and
		// into a config file by the same human weeks later.
		if strings.EqualFold(album.Name, s.album) && album.ID != "" {
			s.mu.Lock()
			s.albumID = album.ID
			s.mu.Unlock()
			return album.ID, nil
		}
	}

	return "", fmt.Errorf("no album named %q (%d albums visible to this API key)", s.album, len(albums))
}

// immichRandomSearch is the POST body for /api/search/random. It is kept to
// the fields whose meaning is not in question: one asset, images only (a video
// would come back as an undecodable blob), and — for a library that uses
// Immich's archive — only the photos the owner still considers part of their
// timeline.
type immichRandomSearch struct {
	Size       int      `json:"size"`
	Type       string   `json:"type"`
	Visibility string   `json:"visibility"`
	AlbumIDs   []string `json:"albumIds,omitempty"`
}

func (s *immichSource) randomAssetID(ctx context.Context, albumID string) (string, error) {
	search := immichRandomSearch{Size: 1, Type: "IMAGE", Visibility: "timeline"}
	if albumID != "" {
		search.AlbumIDs = []string{albumID}
	}
	payload, err := json.Marshal(search)
	if err != nil {
		return "", fmt.Errorf("encoding the search body: %w", err)
	}

	body, err := s.post(ctx, immichJSONLimit, payload, "api", "search", "random")
	if err != nil {
		return "", fmt.Errorf("searching for a random asset: %w", err)
	}

	var assets []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &assets); err != nil {
		return "", fmt.Errorf("decoding the search result: %w", err)
	}
	if len(assets) == 0 {
		// An empty library, an album with no images left in it, or a filter
		// that matched nothing. All are the operator's to fix; the frame just
		// needs a different source this cycle.
		return "", fmt.Errorf("no assets matched")
	}
	if assets[0].ID == "" {
		return "", fmt.Errorf("search result has no asset id")
	}
	// The id is about to become a URL path element, so it is checked against
	// the shape Immich documents rather than trusted. url.JoinPath escapes what
	// it is given, but a value that is not a UUID means the response is not
	// what this code thinks it is, and guessing past that is how a request ends
	// up somewhere it was never meant to go.
	if !uuidPattern.MatchString(assets[0].ID) {
		return "", fmt.Errorf("search returned an asset id that is not a UUID: %q", truncate(assets[0].ID, 64))
	}

	return assets[0].ID, nil
}

// fetchPreview downloads the server-rendered preview of one asset.
func (s *immichSource) fetchPreview(ctx context.Context, id string) ([]byte, error) {
	data, err := s.get(ctx, immichImageLimit, "size=preview", "api", "assets", id, "thumbnail")
	if err != nil {
		return nil, fmt.Errorf("downloading the preview of asset %s: %w", id, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("the preview of asset %s is empty", id)
	}
	return data, nil
}

func (s *immichSource) get(ctx context.Context, limit int64, query string, elems ...string) ([]byte, error) {
	return s.do(ctx, http.MethodGet, limit, nil, query, elems)
}

func (s *immichSource) post(ctx context.Context, limit int64, payload []byte, elems ...string) ([]byte, error) {
	return s.do(ctx, http.MethodPost, limit, payload, "", elems)
}

// do performs one request and returns its body, read under a hard cap.
//
// Nothing about the response is trusted: the URL on the other end is whatever
// IMMICH_URL points at, which may be a typo'd host, a captive portal or a
// reverse proxy having a bad day. So the status is checked first, the body is
// read through a LimitReader, and the reader is drained under a small fixed
// budget and closed on every path so the pooled connection can be reused
// instead of being torn down each wake.
func (s *immichSource) do(ctx context.Context, method string, limit int64, payload []byte, query string, elems []string) ([]byte, error) {
	// Built element by element from the URL parsed at construction, so that a
	// path element can never introduce a "/" or a "?" of its own, and with the
	// query set as a field rather than glued onto the path for the same reason.
	endpoint := s.base.JoinPath(elems...)
	endpoint.RawQuery = query

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("x-api-key", s.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		// Drained only far enough to let an ordinary response finish, so the
		// pooled connection can be reused instead of being torn down each wake.
		// The budget is fixed and small rather than the response cap, because
		// the paths that leave bytes unread are exactly the ones where the peer
		// is misbehaving — downloading another 64 MiB from it to be tidy would
		// be the failure, not the fix. Past the budget the Close below tears the
		// connection down anyway, which is all those extra bytes would buy.
		io.Copy(io.Discard, io.LimitReader(resp.Body, immichDrainLimit))
		resp.Body.Close()
	}()

	// The status is checked before the body is read, for two reasons. A 404
	// behind a 200 KB HTML error page is still a 404, and saying "response too
	// large" there would send whoever is debugging it looking in exactly the
	// wrong place. And a 500 whose body dies mid-read has to be reported as a
	// 500: "reading the response: connection reset by peer" names the symptom
	// and loses the one fact that would have explained it.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// One byte past the snippet so truncate can mark that there was more.
		// The error body is quoted, not kept, so it is read under the snippet
		// cap rather than the caller's much larger one.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, immichSnippetLimit+1))
		return nil, fmt.Errorf("%s %s: %s: %q", method, endpointSummary(endpoint), resp.Status, truncate(string(snippet), immichSnippetLimit))
	}

	// One byte past the cap, so that "exactly at the limit" is distinguishable
	// from "truncated".
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}

	return data, nil
}

// endpointSummary is the scheme, host and path of an endpoint, for an error
// message. The query string is dropped: there is nothing secret in the one this
// client sends, but that is where a token would end up if this ever grew one.
func endpointSummary(endpoint *url.URL) string {
	return endpoint.Scheme + "://" + endpoint.Host + endpoint.Path
}

// truncate shortens s for a log line, marking that it was cut so a snippet is
// never mistaken for the whole response.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
