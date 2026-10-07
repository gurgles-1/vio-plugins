package main

import (
	"testing"
)

func TestPickLogoEnglishPreferred(t *testing.T) {
	logos := []tmdbLogo{
		{FilePath: "/de.png", Iso639_1: "de", VoteAverage: 5.0},
		{FilePath: "/en.png", Iso639_1: "en", VoteAverage: 3.0},
	}
	got := pickLogo(logos)
	if got == nil || got.FilePath != "/en.png" {
		t.Fatalf("expected English logo, got %+v", got)
	}
}

func TestPickLogoHighestVoted(t *testing.T) {
	logos := []tmdbLogo{
		{FilePath: "/a.png", Iso639_1: "en", VoteAverage: 2.0},
		{FilePath: "/b.png", Iso639_1: "en", VoteAverage: 4.5},
		{FilePath: "/c.png", Iso639_1: "en", VoteAverage: 4.0},
	}
	got := pickLogo(logos)
	if got == nil || got.FilePath != "/b.png" {
		t.Fatalf("expected highest voted, got %+v", got)
	}
}

func TestPickLogoNeutralFallback(t *testing.T) {
	logos := []tmdbLogo{
		{FilePath: "/fr.png", Iso639_1: "fr", VoteAverage: 5.0},
		{FilePath: "/neutral.png", Iso639_1: "", VoteAverage: 1.0},
	}
	got := pickLogo(logos)
	if got == nil || got.FilePath != "/neutral.png" {
		t.Fatalf("expected neutral fallback, got %+v", got)
	}
}

func TestPickLogoEmpty(t *testing.T) {
	if got := pickLogo(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
	if got := pickLogo([]tmdbLogo{{FilePath: "/fr.png", Iso639_1: "fr"}}); got != nil {
		t.Fatalf("expected nil when only non-English non-neutral, got %+v", got)
	}
}

func TestParseTMDBLogos(t *testing.T) {
	body := map[string]any{
		"logos": []any{
			map[string]any{"file_path": "/a.png", "iso_639_1": "en", "vote_average": 3.5, "width": 800, "height": 200},
			map[string]any{"file_path": "", "iso_639_1": "en"},
			map[string]any{"file_path": "/b.png", "iso_639_1": "EN", "vote_average": 4.0},
		},
	}
	logos := parseTMDBLogos(body)
	if len(logos) != 2 {
		t.Fatalf("expected 2 logos, got %d", len(logos))
	}
	if logos[0].Iso639_1 != "en" || logos[0].Width != 800 {
		t.Fatalf("bad parse: %+v", logos[0])
	}
	if logos[1].Iso639_1 != "en" {
		t.Fatalf("iso should be lowercased: %+v", logos[1])
	}
}
