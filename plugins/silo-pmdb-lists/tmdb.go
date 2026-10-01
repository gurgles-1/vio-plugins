package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const tmdbBaseURL = "https://api.themoviedb.org/3"
const tmdbImageBase = "https://image.tmdb.org/t/p"

// tmdbTitle is the normalized metadata the list pages need for one TMDB
// record. The browser never plays anything, so no episode data is fetched.
type tmdbTitle struct {
	TMDBID      int      `json:"tmdb_id"`
	MediaType   string   `json:"media_type"`
	IMDbID      string   `json:"imdb_id"`
	Title       string   `json:"title"`
	Year        int      `json:"year"`
	Overview    string   `json:"overview"`
	PosterURL   string   `json:"poster_url"`
	BackdropURL string   `json:"backdrop_url"`
	Genres      []string `json:"genres"`
}

type tmdbClient struct {
	http   *http.Client
	apiKey string
	jwt    bool
}

func newTMDBClient(apiKey string) *tmdbClient {
	// v4 read tokens are JWTs; v3 keys go in the query string.
	jwt := strings.HasPrefix(strings.TrimSpace(apiKey), "eyJ")
	return &tmdbClient{
		http:   &http.Client{Timeout: 30 * time.Second},
		apiKey: strings.TrimSpace(apiKey),
		jwt:    jwt,
	}
}

func (c *tmdbClient) get(path string) (map[string]any, error) {
	url := tmdbBaseURL + path
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if !c.jwt {
		url += sep + "api_key=" + c.apiKey
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

// fetchTitle resolves one TMDB movie or TV record with its IMDb id and
// display metadata.
func (c *tmdbClient) fetchTitle(tmdbID int, mediaType string) (*tmdbTitle, error) {
	kind := "movie"
	if mediaType == "tv" {
		kind = "tv"
	}
	body, err := c.get(fmt.Sprintf("/%s/%d?append_to_response=external_ids", kind, tmdbID))
	if err != nil {
		return nil, err
	}
	t := &tmdbTitle{TMDBID: tmdbID, MediaType: mediaType}
	if ext, _ := body["external_ids"].(map[string]any); ext != nil {
		t.IMDbID = strings.TrimSpace(strFromAny(ext["imdb_id"]))
	}
	if kind == "movie" {
		t.Title = strFromAny(body["title"])
		t.Year = yearFromDate(strFromAny(body["release_date"]))
	} else {
		t.Title = strFromAny(body["name"])
		t.Year = yearFromDate(strFromAny(body["first_air_date"]))
	}
	t.Overview = strFromAny(body["overview"])
	if p := strFromAny(body["poster_path"]); p != "" {
		t.PosterURL = tmdbImageBase + "/w342" + p
	}
	if b := strFromAny(body["backdrop_path"]); b != "" {
		t.BackdropURL = tmdbImageBase + "/w780" + b
	}
	if genres, ok := body["genres"].([]any); ok {
		for _, g := range genres {
			if gm, ok := g.(map[string]any); ok {
				if name := strings.TrimSpace(strFromAny(gm["name"])); name != "" {
					t.Genres = append(t.Genres, name)
				}
			}
		}
	}
	return t, nil
}

func yearFromDate(date string) int {
	if len(date) >= 4 {
		y, err := strconv.Atoi(date[:4])
		if err == nil && y > 1800 && y < 2200 {
			return y
		}
	}
	return 0
}
