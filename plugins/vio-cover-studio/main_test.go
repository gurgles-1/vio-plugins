package main

import (
	"testing"
)

func validCoverConfig() pluginConfig {
	return pluginConfig{
		VioURL:       "http://127.0.0.1:8080",
		VioAPIKey:    "sa_test",
		TMDBAPIKey:   "tmdb_test",
		OverlayMode:  "logo",
		Layout:       "grid",
		RefreshHours: 24,
	}
}

func TestCoverConfigValidate(t *testing.T) {
	if err := validCoverConfig().validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []pluginConfig{
		{ /* empty */ },
		{ VioURL: "http://x", TMDBAPIKey: "t", OverlayMode: "logo", Layout: "grid", RefreshHours: 24 },
		{ VioURL: "http://x", VioAPIKey: "k", OverlayMode: "logo", Layout: "grid", RefreshHours: 24 },
		func() pluginConfig { c := validCoverConfig(); c.OverlayMode = "bogus"; return c }(),
		func() pluginConfig { c := validCoverConfig(); c.Layout = "bogus"; return c }(),
		func() pluginConfig { c := validCoverConfig(); c.RefreshHours = 0; return c }(),
	}
	for i, c := range bad {
		if err := c.validate(); err == nil {
			t.Fatalf("config %d should be invalid", i)
		}
	}
}

func TestConfigManages(t *testing.T) {
	c := validCoverConfig()
	if !c.manages("12") {
		t.Fatal("empty allowlist should manage everything")
	}
	c.Collections = []string{"12", "34"}
	if !c.manages("12") || c.manages("99") {
		t.Fatal("allowlist not honored")
	}
}

func TestConfigOverrides(t *testing.T) {
	c := validCoverConfig()
	st := &pluginState{Overrides: map[string]*collectionOverride{}}
	if got := c.overlayFor("12", st); got != "logo" {
		t.Fatalf("default overlay: %s", got)
	}
	if got := c.layoutFor("12", st); got != "grid" {
		t.Fatalf("default layout: %s", got)
	}
	enabled := false
	st.Overrides["12"] = &collectionOverride{OverlayMode: "text", Layout: "columns", Enabled: &enabled}
	if got := c.overlayFor("12", st); got != "text" {
		t.Fatalf("override overlay: %s", got)
	}
	if got := c.layoutFor("12", st); got != "columns" {
		t.Fatalf("override layout: %s", got)
	}
	if c.enabled("12", st) {
		t.Fatal("override enabled=false not honored")
	}
	if !c.enabled("99", st) {
		t.Fatal("unlisted collection should be enabled by default")
	}
}

func TestParseLines(t *testing.T) {
	got := parseLines("12\n34\n")
	if len(got) != 2 || got[0] != "12" || got[1] != "34" {
		t.Fatalf("bad lines: %v", got)
	}
	got = parseLines([]any{"a", " b ", ""})
	if len(got) != 2 || got[1] != "b" {
		t.Fatalf("bad lines: %v", got)
	}
}

func TestAbsURL(t *testing.T) {
	base := "http://127.0.0.1:8080/api/v2"
	if got := absURL(base, "/img/p.jpg"); got != "http://127.0.0.1:8080/img/p.jpg" {
		t.Fatalf("relative: %s", got)
	}
	if got := absURL(base, "https://cdn/x.png"); got != "https://cdn/x.png" {
		t.Fatalf("absolute: %s", got)
	}
	if got := absURL(base, ""); got != "" {
		t.Fatalf("empty: %s", got)
	}
}

func TestRedactCoverSecrets(t *testing.T) {
	s := redactSecrets("boom https://h/?api_key=S3CR3T&x=1")
	for _, bad := range []string{"S3CR3T"} {
		found := false
		for i := 0; i+len(bad) <= len(s); i++ {
			if s[i:i+len(bad)] == bad {
				found = true
			}
		}
		if found {
			t.Fatalf("secret leaked: %s", s)
		}
	}
}
