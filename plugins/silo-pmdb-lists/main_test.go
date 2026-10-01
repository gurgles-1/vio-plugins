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
	ok := pluginConfig{PMDBAPIKey: "k", PMDBListIDs: []string{"a"}, TMDBAPIKey: "t"}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for i, c := range []pluginConfig{
		{TMDBAPIKey: "t", PMDBListIDs: []string{"a"}},
		{PMDBAPIKey: "k", TMDBAPIKey: "t"},
		{PMDBAPIKey: "k", PMDBListIDs: []string{"a"}},
	} {
		if err := c.validate(); err == nil {
			t.Fatalf("config %d should be invalid", i)
		}
	}
}

func TestRedactSecrets(t *testing.T) {
	s := redactSecrets("failed https://x/?api_key=SECRET123&b=1")
	if strings.Contains(s, "SECRET123") {
		t.Fatalf("secret leaked: %s", s)
	}
}

func TestRenderIndexEscapes(t *testing.T) {
	cache := &listCache{Lists: map[string]*cachedList{
		"abc": {ID: "abc", Name: `<script>alert(1)</script>`, Items: []*tmdbTitle{
			{TMDBID: 1, MediaType: "movie", Title: "A & B", Year: 2020},
		}},
	}}
	html := renderIndex(cache, "")
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("unescaped list name in index")
	}
	if !strings.Contains(html, "A &amp; B") && strings.Contains(html, "A & B") {
		t.Fatal("title not escaped")
	}
}

func TestRenderDetailFilter(t *testing.T) {
	cl := &cachedList{ID: "abc", Name: "Test", Items: []*tmdbTitle{
		{TMDBID: 1, MediaType: "movie", Title: "InLib", Year: 2020},
		{TMDBID: 2, MediaType: "movie", Title: "Missing", Year: 2021},
	}}
	presence := map[string]bool{"1": true}
	all := renderListDetail(cl, presence, "all", "")
	if !strings.Contains(all, "InLib") || !strings.Contains(all, "Missing") {
		t.Fatal("all filter should show both")
	}
	missing := renderListDetail(cl, presence, "missing", "")
	if strings.Contains(missing, "InLib") || !strings.Contains(missing, "Missing") {
		t.Fatal("missing filter should show only the absent title")
	}
}
