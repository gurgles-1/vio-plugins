package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tmdbLogoBaseURL = "https://api.themoviedb.org/3"
const tmdbLogoImageBase = "https://image.tmdb.org/t/p/original"

// tmdbLogo is one entry from a TMDB /images logos list.
type tmdbLogo struct {
	FilePath    string
	Iso639_1    string
	VoteAverage float64
	Width       int
	Height      int
}

type tmdbLogoClient struct {
	http   *http.Client
	apiKey string
	jwt    bool
}

func newTMDBLogoClient(apiKey string) *tmdbLogoClient {
	// v4 read tokens are JWTs; v3 keys go in the query string.
	jwt := strings.HasPrefix(strings.TrimSpace(apiKey), "eyJ")
	return &tmdbLogoClient{
		http:   &http.Client{Timeout: 30 * time.Second},
		apiKey: strings.TrimSpace(apiKey),
		jwt:    jwt,
	}
}

func (c *tmdbLogoClient) get(path string) (map[string]any, error) {
	u := tmdbLogoBaseURL + path
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if !c.jwt {
		u += sep + "api_key=" + url.QueryEscape(c.apiKey)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.jwt {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmdb GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb GET %s: unexpected status %d", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("tmdb GET %s: decode: %w", path, err)
	}
	return out, nil
}

func parseTMDBLogos(body map[string]any) []tmdbLogo {
	var out []tmdbLogo
	raw, _ := body["logos"].([]any)
	for _, e := range raw {
		m, _ := e.(map[string]any)
		if m == nil {
			continue
		}
		fp := strings.TrimSpace(strFromAny(m["file_path"]))
		if fp == "" {
			continue
		}
		l := tmdbLogo{FilePath: fp, Iso639_1: strings.ToLower(strings.TrimSpace(strFromAny(m["iso_639_1"])))}
		if v, ok := m["vote_average"].(float64); ok {
			l.VoteAverage = v
		}
		l.Width = intFromAny(m["width"])
		l.Height = intFromAny(m["height"])
		out = append(out, l)
	}
	return out
}

// pickLogo chooses the best logo: English first, then language-neutral,
// highest vote_average wins. Returns nil when there is nothing usable.
func pickLogo(logos []tmdbLogo) *tmdbLogo {
	var best *tmdbLogo
	considered := func() []tmdbLogo {
		var en []tmdbLogo
		for _, l := range logos {
			if l.Iso639_1 == "en" {
				en = append(en, l)
			}
		}
		if len(en) > 0 {
			return en
		}
		var neutral []tmdbLogo
		for _, l := range logos {
			if l.Iso639_1 == "" || l.Iso639_1 == "null" || l.Iso639_1 == "xx" {
				neutral = append(neutral, l)
			}
		}
		return neutral
	}()
	for i := range considered {
		l := &considered[i]
		if best == nil || l.VoteAverage > best.VoteAverage {
			best = l
		}
	}
	return best
}

// fetchLogo downloads the logo PNG bytes (transparency preserved).
func (c *tmdbLogoClient) fetchLogo(l *tmdbLogo) ([]byte, error) {
	if l == nil {
		return nil, fmt.Errorf("no logo")
	}
	u := tmdbLogoImageBase + l.FilePath
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmdb logo download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb logo download: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
	if err != nil {
		return nil, fmt.Errorf("tmdb logo download: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("tmdb logo download: empty body")
	}
	return raw, nil
}

// resolveCollectionLogo finds a TMDB id for a collection title, then the best
// logo for it. It tries collection search (franchises), then movie, then TV.
// Returns nil, nil when nothing usable is found (caller falls back to text).
func (c *tmdbLogoClient) resolveCollectionLogo(title string) ([]byte, string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, "", nil
	}
	type hit struct {
		kind string
		id   int
	}
	var hits []hit
	for _, searchKind := range []struct {
		path string
		kind string
	}{
		{"/search/collection", "collection"},
		{"/search/movie", "movie"},
		{"/search/tv", "tv"},
	} {
		body, err := c.get(searchKind.path + "?query=" + url.QueryEscape(title) + "&include_adult=false")
		if err != nil {
			continue
		}
		raw, _ := body["results"].([]any)
		if len(raw) == 0 {
			continue
		}
		if m, ok := raw[0].(map[string]any); ok {
			if id := intFromAny(m["id"]); id > 0 {
				hits = append(hits, hit{searchKind.kind, id})
				break
			}
		}
	}
	for _, h := range hits {
		var imgPath string
		switch h.kind {
		case "collection":
			// TMDB collections expose posters/backdrops, not logos; use the
			// backdrop as a fallback only when no logo exists elsewhere.
			continue
		case "movie":
			imgPath = fmt.Sprintf("/movie/%d/images", h.id)
		case "tv":
			imgPath = fmt.Sprintf("/tv/%d/images", h.id)
		}
		body, err := c.get(imgPath)
		if err != nil {
			continue
		}
		if logo := pickLogo(parseTMDBLogos(body)); logo != nil {
			raw, err := c.fetchLogo(logo)
			if err != nil {
				continue
			}
			return raw, fmt.Sprintf("%s:%d", h.kind, h.id), nil
		}
	}
	return nil, "", nil
}
