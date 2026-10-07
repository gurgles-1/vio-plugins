package main

import (
	"strings"
	"testing"
)

func TestParseListIDs(t *testing.T) {
	ids := parseListIDs("pvg2vr8nuvkh2g5\nhddzdnbivm49u1r\n")
	if len(ids) != 2 || ids[0] != "pvg2vr8nuvkh2g5" || ids[1] != "hddzdnbivm49u1r" {
		t.Fatalf("unexpected ids: %v", ids)
	}
	ids = parseListIDs([]any{"a", " b ", ""})
	if len(ids) != 2 || ids[1] != "b" {
		t.Fatalf("unexpected ids: %v", ids)
	}
	if ids := parseListIDs(""); len(ids) != 0 {
		t.Fatalf("expected empty, got %v", ids)
	}
}

func TestYearFromDate(t *testing.T) {
	if y := yearFromDate("2024-06-07"); y != 2024 {
		t.Fatalf("got %d", y)
	}
	if y := yearFromDate(""); y != 0 {
		t.Fatalf("got %d", y)
	}
}

func TestConfigValidate(t *testing.T) {
	ok := pluginConfig{PMDBAPIKey: "k", PMDBLists: []string{"a"}, TMDBAPIKey: "t", MovieLibrary: "Movies"}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []pluginConfig{
		{TMDBAPIKey: "t", PMDBLists: []string{"a"}, MovieLibrary: "Movies"},
		{PMDBAPIKey: "k", TMDBAPIKey: "t", MovieLibrary: "Movies"},
		{PMDBAPIKey: "k", PMDBLists: []string{"a"}},
		{PMDBAPIKey: "k", PMDBLists: []string{"a"}, TMDBAPIKey: "t"},
	}
	for i, c := range bad {
		if err := c.validate(); err == nil {
			t.Fatalf("config %d should be invalid", i)
		}
	}
}

func TestBuildRegistrationMovie(t *testing.T) {
	cfg := pluginConfig{MovieLibrary: "Movies", SeriesLibrary: "TV"}
	title := &tmdbTitle{TMDBID: 550, MediaType: "movie", IMDbID: "tt0137523", Title: "Fight Club", Year: 1999}
	req, err := buildRegistration(cfg, title, "3", "4")
	if err != nil {
		t.Fatal(err)
	}
	if req.VirtualURI != "virtual://movie/tt0137523" {
		t.Fatalf("bad movie uri: %s", req.VirtualURI)
	}
	if req.LibraryID != "3" || req.SourceKey != syncSourceKey || req.TMDBID != "550" {
		t.Fatalf("bad fields: %+v", req)
	}
}

func TestBuildRegistrationSeries(t *testing.T) {
	cfg := pluginConfig{MovieLibrary: "Movies", SeriesLibrary: "TV"}
	title := &tmdbTitle{
		TMDBID: 1396, MediaType: "tv", IMDbID: "tt0903747", Title: "Breaking Bad", Year: 2008,
		Episodes: []tmdbEpisode{{Season: 1, Episode: 1, Title: "Pilot"}},
	}
	req, err := buildRegistration(cfg, title, "3", "4")
	if err != nil {
		t.Fatal(err)
	}
	if req.VirtualURI != "" {
		t.Fatalf("series must not have a series-level URI, got %s", req.VirtualURI)
	}
	if len(req.Episodes) != 1 || req.Episodes[0].VirtualURI != "virtual://series/tt0903747/1/1" {
		t.Fatalf("bad episodes: %+v", req.Episodes)
	}
	if req.LibraryID != "4" {
		t.Fatalf("bad library: %s", req.LibraryID)
	}
	// A series with no episodes must be rejected, not registered empty.
	_, err = buildRegistration(cfg, &tmdbTitle{TMDBID: 1, MediaType: "tv", IMDbID: "tt1", Title: "X"}, "3", "4")
	if err == nil {
		t.Fatal("expected error for episode-less series")
	}
}

func TestRedactSecrets(t *testing.T) {
	s := redactSecrets("failed https://x/?api_key=SECRET123&b=1")
	if strings.Contains(s, "SECRET123") {
		t.Fatalf("secret leaked: %s", s)
	}
}
