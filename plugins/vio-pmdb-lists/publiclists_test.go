package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePublicListURL(t *testing.T) {
	user, slug, ok := parsePublicListURL("https://publicmetadb.com/lists/u/snoak/trending-kids-movies")
	if !ok || user != "snoak" || slug != "trending-kids-movies" {
		t.Fatalf("got %q %q %v", user, slug, ok)
	}
	// http, www, trailing slash, and query strings are tolerated.
	user, slug, ok = parsePublicListURL("http://www.publicmetadb.com/lists/u/Joshlucpoll/louis-theroux-documentaries/?x=1")
	if !ok || user != "Joshlucpoll" || slug != "louis-theroux-documentaries" {
		t.Fatalf("got %q %q %v", user, slug, ok)
	}
	// Percent-encoded segments are decoded.
	_, slug, ok = parsePublicListURL("https://publicmetadb.com/lists/u/snoak/my%20list")
	if !ok || slug != "my list" {
		t.Fatalf("got %q %v", slug, ok)
	}
	// Raw IDs and unrelated URLs are not public list URLs.
	for _, raw := range []string{
		"x6qkmzks02t0se1",
		"https://publicmetadb.com/lists",
		"https://trakt.tv/users/snoak/lists/trending-kids-movies",
		"",
	} {
		if _, _, ok := parsePublicListURL(raw); ok {
			t.Fatalf("%q should not parse as a public list URL", raw)
		}
	}
}

func TestResolvePublicList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/collections/lists/records") {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("filter"); !strings.Contains(got, `slug="trending-kids-movies"`) || !strings.Contains(got, "is_public=true") {
			t.Errorf("unexpected filter: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{map[string]any{
				"id":   "x6qkmzks02t0se1",
				"name": "Trending Kids Movies",
				"slug": "trending-kids-movies",
				"expand": map[string]any{
					"user": map[string]any{"name": "snoak"},
				},
			}},
		})
	}))
	defer srv.Close()

	old := pbPublicBaseURL
	pbPublicBaseURL = srv.URL
	defer func() { pbPublicBaseURL = old }()

	id, name, err := resolvePublicList("snoak", "trending-kids-movies")
	if err != nil {
		t.Fatal(err)
	}
	if id != "x6qkmzks02t0se1" || name != "Trending Kids Movies" {
		t.Fatalf("got %q %q", id, name)
	}
	// Owner mismatch must not resolve.
	if _, _, err := resolvePublicList("someoneelse", "trending-kids-movies"); err == nil {
		t.Fatal("expected owner-mismatch error")
	}
	// Unknown slug must not resolve.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv2.Close()
	pbPublicBaseURL = srv2.URL
	if _, _, err := resolvePublicList("snoak", "nope"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestResolveListEntry(t *testing.T) {
	// Raw IDs pass through untouched.
	id, label, err := resolveListEntry("x6qkmzks02t0se1")
	if err != nil || id != "x6qkmzks02t0se1" || label != "x6qkmzks02t0se1" {
		t.Fatalf("got %q %q %v", id, label, err)
	}
	if _, _, err := resolveListEntry("  "); err == nil {
		t.Fatal("expected error for empty entry")
	}
}
