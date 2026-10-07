package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// vioCollection is one entry from GET /admin/collections.
type vioCollection struct {
	ID    string
	Title string
	Type  string // collection_type, e.g. "manual"
	Kind  string // "movie" or "series", when the API reports it
}

type vioMember struct {
	MediaItemID string
	Position    int
}

// vioTitle is the catalog metadata we need per member.
type vioTitle struct {
	MediaItemID string
	TMDBID      int
	MediaType   string // "movie" or "tv"
	PosterURL   string
	BackdropURL string
	Title       string
}

type vioClient struct {
	http    *http.Client
	base    string // vio_url + /api/v2
	apiKey  string
	profile string // X-Profile-Id, resolved lazily
}

func newVioClient(vioURL, apiKey string) *vioClient {
	return &vioClient{
		http:   &http.Client{Timeout: 60 * time.Second},
		base:   strings.TrimRight(vioURL, "/") + "/api/v2",
		apiKey: strings.TrimSpace(apiKey),
	}
}

func (c *vioClient) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vio-cover-studio/0.1.0")
	if c.profile != "" {
		req.Header.Set("X-Profile-Id", c.profile)
	}
	return req, nil
}

func (c *vioClient) doJSON(req *http.Request) (map[string]any, int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusNoContent {
		return map[string]any{}, resp.StatusCode, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// Some endpoints return arrays; wrap them.
		var arr []any
		if err2 := json.Unmarshal(raw, &arr); err2 == nil {
			return map[string]any{"items": arr}, resp.StatusCode, nil
		}
		return nil, resp.StatusCode, fmt.Errorf("decode %s: %w", req.URL.Path, err)
	}
	return out, resp.StatusCode, nil
}

// getJSON performs a GET, resolving the profile header once if the catalog
// demands it (401/403 without X-Profile-Id).
func (c *vioClient) getJSON(path string) (map[string]any, error) {
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	out, status, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	if (status == http.StatusUnauthorized || status == http.StatusForbidden) && c.profile == "" {
		if err := c.resolveProfile(); err != nil {
			return nil, fmt.Errorf("GET %s -> %d and profile lookup failed: %v", path, status, err)
		}
		req, err = c.newRequest(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		out, status, err = c.doJSON(req)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("GET %s -> %d", path, status)
	}
	return out, nil
}

func (c *vioClient) resolveProfile() error {
	req, err := c.newRequest(http.MethodGet, "/profiles", nil)
	if err != nil {
		return err
	}
	out, status, err := c.doJSON(req)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("GET /profiles -> %d", status)
	}
	items, _ := out["items"].([]any)
	var first map[string]any
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		if first == nil {
			first = m
		}
		if b, _ := m["is_primary"].(bool); b {
			first = m
			break
		}
	}
	if first == nil {
		return fmt.Errorf("no profiles on the account")
	}
	c.profile = strings.TrimSpace(strFromAny(first["id"]))
	if c.profile == "" {
		return fmt.Errorf("profile has no id")
	}
	return nil
}

func strFromAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strings.TrimSpace(fmt.Sprintf("%d", int64(t)))
		}
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(t), "%d", &n)
		return n
	default:
		return 0
	}
}

// listCollections returns the manual collections on the server.
func (c *vioClient) listCollections() ([]vioCollection, error) {
	out, err := c.getJSON("/admin/collections?limit=200")
	if err != nil {
		return nil, err
	}
	items, _ := out["items"].([]any)
	var cols []vioCollection
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		ctype := strings.ToLower(strFromAny(m["collection_type"]))
		if ctype != "" && ctype != "manual" {
			continue
		}
		cols = append(cols, vioCollection{
			ID:    strFromAny(m["id"]),
			Title: strFromAny(m["title"]),
			Type:  ctype,
			Kind:  strings.ToLower(strFromAny(m["kind"])),
		})
	}
	return cols, nil
}

// collectionMembers returns up to limit member item ids in position order.
func (c *vioClient) collectionMembers(collectionID string, limit int) ([]vioMember, error) {
	if limit <= 0 {
		limit = 200
	}
	out, err := c.getJSON("/admin/collections/" + url.PathEscape(collectionID) +
		fmt.Sprintf("/items?limit=%d", limit))
	if err != nil {
		return nil, err
	}
	items, _ := out["items"].([]any)
	var members []vioMember
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		id := strFromAny(m["media_item_id"])
		if id == "" {
			continue
		}
		members = append(members, vioMember{MediaItemID: id, Position: intFromAny(m["position"])})
	}
	return members, nil
}

// catalogTitle fetches poster/backdrop/TMDB ids for one catalog item.
func (c *vioClient) catalogTitle(mediaItemID string) (*vioTitle, error) {
	out, err := c.getJSON("/catalog/items/" + url.PathEscape(mediaItemID))
	if err != nil {
		return nil, err
	}
	t := &vioTitle{MediaItemID: mediaItemID}
	t.Title = strFromAny(out["title"])
	if t.Title == "" {
		t.Title = strFromAny(out["name"])
	}
	t.PosterURL = absURL(c.base, strFromAny(out["poster_url"]))
	if t.PosterURL == "" {
		t.PosterURL = absURL(c.base, strFromAny(out["poster_thumbhash"]))
	}
	t.BackdropURL = absURL(c.base, strFromAny(out["backdrop_url"]))
	t.TMDBID = intFromAny(out["tmdb_id"])
	mt := strings.ToLower(strFromAny(out["media_type"]))
	if mt == "" {
		mt = strings.ToLower(strFromAny(out["type"]))
	}
	switch {
	case strings.Contains(mt, "movie"):
		t.MediaType = "movie"
	case strings.Contains(mt, "series") || strings.Contains(mt, "show") || strings.Contains(mt, "tv"):
		t.MediaType = "tv"
	}
	return t, nil
}

func absURL(base, u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "data:") {
		return u
	}
	// base is <root>/api/v2; strip back to the root for relative artwork URLs.
	root := strings.TrimSuffix(base, "/api/v2")
	if strings.HasPrefix(u, "/") {
		return root + u
	}
	return root + "/" + u
}

// uploadImage PUTs PNG/JPEG bytes to a collection image endpoint.
// ok=false with nil error means the endpoint does not exist (graceful skip).
func (c *vioClient) uploadImage(collectionID, endpoint string, img []byte) (ok bool, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("image", "cover.png")
	if err != nil {
		return false, err
	}
	if _, err := fw.Write(img); err != nil {
		return false, err
	}
	if err := w.Close(); err != nil {
		return false, err
	}
	req, err := c.newRequest(http.MethodPut,
		"/admin/collections/"+url.PathEscape(collectionID)+"/"+endpoint, &buf)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, nil
	default:
		return false, fmt.Errorf("PUT %s -> %d", endpoint, resp.StatusCode)
	}
}

// listCollections is the package-level helper used by the admin status page.
func listCollections(cfg pluginConfig) ([]vioCollection, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return newVioClient(cfg.VioURL, cfg.VioAPIKey).listCollections()
}
