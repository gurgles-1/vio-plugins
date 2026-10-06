package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// pbPublicBaseURL is the PublicMetaDB PocketBase backend. Its lists
// collection is world-readable for public lists, which is exactly how the
// PMDB web app resolves a /lists/u/{user}/{slug} URL. A var so tests can
// point it at an httptest server.
var pbPublicBaseURL = "https://api.publicmetadb.com"

var publicListURLPattern = regexp.MustCompile(
	`(?i)^https?://(?:www\.)?publicmetadb\.com/lists/u/([^/]+)/([^/?#]+)`)

// parsePublicListURL extracts the (user, slug) pair from a public PMDB list
// URL like https://publicmetadb.com/lists/u/snoak/trending-kids-movies.
// ok is false when the string is not such a URL (e.g. a raw list ID).
func parsePublicListURL(raw string) (user, slug string, ok bool) {
	m := publicListURLPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", "", false
	}
	user, _ = url.PathUnescape(m[1])
	slug, _ = url.PathUnescape(m[2])
	user = strings.TrimSpace(user)
	slug = strings.TrimSpace(slug)
	if user == "" || slug == "" {
		return "", "", false
	}
	return user, slug, true
}

// resolvePublicList turns a (user, slug) pair into the PMDB list ID by
// querying the public PocketBase lists collection. No authentication is
// needed; the owner name from the URL is verified against the record so a
// renamed or mistyped slug cannot resolve to someone else's list.
func resolvePublicList(user, slug string) (id, name string, err error) {
	filter := fmt.Sprintf(`is_public=true && slug="%s"`, slug)
	endpoint := fmt.Sprintf("%s/api/collections/lists/records?perPage=1&filter=%s&expand=user",
		pbPublicBaseURL, url.QueryEscape(filter))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vio-pmdb-lists/0.2.0")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("resolve public list %s/%s: %w", user, slug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("resolve public list %s/%s: unexpected status %d", user, slug, resp.StatusCode)
	}
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Slug   string `json:"slug"`
			Expand struct {
				User *struct {
					Name string `json:"name"`
				} `json:"user"`
			} `json:"expand"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("resolve public list %s/%s: decode: %w", user, slug, err)
	}
	if len(body.Items) == 0 {
		return "", "", fmt.Errorf("no public list found for u/%s/%s", user, slug)
	}
	rec := body.Items[0]
	owner := ""
	if rec.Expand.User != nil {
		owner = rec.Expand.User.Name
	}
	if owner != user && !strings.EqualFold(owner, user) {
		return "", "", fmt.Errorf("slug %q belongs to %q, not %q", slug, owner, user)
	}
	return rec.ID, rec.Name, nil
}

// resolveListEntry maps one configured list entry to the PMDB list ID used
// by the items API. A public list URL is resolved via the public endpoint;
// anything else is passed through as a raw list ID (your own lists).
// It returns the ID and a human-friendly label for status output.
func resolveListEntry(entry string) (id, label string, err error) {
	if user, slug, ok := parsePublicListURL(entry); ok {
		id, name, err := resolvePublicList(user, slug)
		if err != nil {
			return "", "", err
		}
		if name == "" {
			name = slug
		}
		return id, fmt.Sprintf("%s (u/%s/%s)", name, user, slug), nil
	}
	id = strings.TrimSpace(entry)
	if id == "" {
		return "", "", fmt.Errorf("empty list entry")
	}
	return id, id, nil
}
