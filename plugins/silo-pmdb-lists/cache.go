package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cachedList is one PMDB list as stored in the cache file.
type cachedList struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Items []*tmdbTitle `json:"items"`
}

type listCache struct {
	SyncedAt time.Time             `json:"synced_at"`
	Lists    map[string]*cachedList `json:"lists"`
	Errors   []string              `json:"errors"`
}

type syncSummary struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Skipped    bool      `json:"skipped"`
	SkipReason string    `json:"skip_reason,omitempty"`
	Lists      int       `json:"lists"`
	Titles     int       `json:"titles"`
	Errors     []string  `json:"errors"`
}

// resolveStatePath keeps the cache inside the plugin data dir so it survives
// container recreation.
func resolveStatePath(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		file = ".silo-pmdb-lists-cache.json"
	}
	if filepath.IsAbs(file) {
		return file
	}
	if base := strings.TrimSpace(os.Getenv("SILO_PLUGIN_CACHE_DIR")); base != "" {
		return filepath.Join(base, "com.gurgles-1.silo-pmdb-lists", file)
	}
	return file
}

func loadCache(path string) *listCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return &listCache{Lists: map[string]*cachedList{}}
	}
	var c listCache
	if err := json.Unmarshal(data, &c); err != nil {
		return &listCache{Lists: map[string]*cachedList{}}
	}
	if c.Lists == nil {
		c.Lists = map[string]*cachedList{}
	}
	return &c
}

func saveCache(path string, c *listCache) {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// doSync fetches every configured PMDB list, resolves its titles through
// TMDB, and writes the cache file. force bypasses the minimum-interval guard.
func (s *runtimeServer) doSync(ctx context.Context, force bool) (*syncSummary, error) {
	s.mu.Lock()
	cfg := s.cfg
	lastSync := s.lastSync
	s.mu.Unlock()

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if !force && !lastSync.IsZero() {
		if min := time.Duration(cfg.SyncIntervalMinutes) * time.Minute; time.Since(lastSync) < min {
			return &syncSummary{
				Skipped:    true,
				SkipReason: fmt.Sprintf("last sync %s ago; next run after %s", time.Since(lastSync).Round(time.Second), lastSync.Add(min).Format(time.RFC3339)),
			}, nil
		}
	}

	sum := &syncSummary{StartedAt: time.Now()}
	pmdb := newPMDBClient(cfg.PMDBAPIKey)
	tmdb := newTMDBClient(cfg.TMDBAPIKey)

	cache := &listCache{Lists: map[string]*cachedList{}}
	for _, listID := range cfg.PMDBListIDs {
		cl := &cachedList{ID: listID, Name: pmdb.listName(listID)}
		if cl.Name == "" {
			cl.Name = listID
		}
		items, err := pmdb.listItems(listID)
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("list %s: %v", listID, redactSecrets(err.Error())))
			continue
		}
		seen := map[string]bool{}
		for _, it := range items {
			key := fmt.Sprintf("%d:%s", it.TMDBID, it.MediaType)
			if seen[key] {
				continue
			}
			seen[key] = true
			title, err := tmdb.fetchTitle(it.TMDBID, it.MediaType)
			if err != nil {
				sum.Errors = append(sum.Errors, fmt.Sprintf("tmdb %d (%s): %v", it.TMDBID, it.MediaType, redactSecrets(err.Error())))
				continue
			}
			cl.Items = append(cl.Items, title)
			time.Sleep(100 * time.Millisecond) // be polite to the TMDB rate limiter
		}
		cache.Lists[listID] = cl
		sum.Lists++
		sum.Titles += len(cl.Items)
	}

	sum.FinishedAt = time.Now()
	cache.SyncedAt = sum.FinishedAt
	cache.Errors = sum.Errors

	s.mu.Lock()
	s.lastSync = sum.FinishedAt
	s.lastSummary = sum
	statePath := resolveStatePath(cfg.StateFile)
	s.mu.Unlock()
	saveCache(statePath, cache)

	return sum, nil
}
