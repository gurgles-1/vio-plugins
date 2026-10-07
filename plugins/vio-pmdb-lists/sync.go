package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtimehost"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const syncSourceKey = "pmdb-lists"

type listStat struct {
	Items      int `json:"items"`
	Registered int `json:"registered"`
}

type syncResult struct {
	StartedAt   time.Time           `json:"started_at"`
	FinishedAt  time.Time           `json:"finished_at"`
	Skipped     bool                `json:"skipped"`
	SkipReason  string              `json:"skip_reason,omitempty"`
	Lists       map[string]listStat `json:"lists"`
	Registered  int                 `json:"registered"`
	Removed     int                 `json:"removed"`
	SkippedIMDb int                 `json:"skipped_no_imdb"`
	Reconciled  bool                `json:"reconciled"`
	Errors      []string            `json:"errors"`
}

// resolveStatePath mirrors the reference plugin: relative paths live under
// the plugin data dir so state survives container recreation.
func resolveStatePath(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		file = ".vio-pmdb-lists-state.json"
	}
	if filepath.IsAbs(file) {
		return file
	}
	if base := strings.TrimSpace(os.Getenv("SILO_PLUGIN_CACHE_DIR")); base != "" {
		return filepath.Join(base, "com.gurgles-1.vio-pmdb-lists", file)
	}
	return file
}

type persistedState struct {
	LastSync   time.Time   `json:"last_sync"`
	LastResult *syncResult `json:"last_result"`
	MediaIDs   []string    `json:"media_ids"`
}

func loadPersistedState(path string) *persistedState {
	data, err := os.ReadFile(path)
	if err != nil {
		return &persistedState{}
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return &persistedState{}
	}
	return &st
}

func savePersistedState(path string, st *persistedState) {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// doSync fetches every configured PMDB list and upserts its titles as
// virtual media. force bypasses the minimum-interval guard.
func (s *runtimeServer) doSync(ctx context.Context, force bool) (*syncResult, error) {
	s.mu.Lock()
	cfg := s.cfg
	lastSync := s.lastSync
	s.mu.Unlock()

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if !force && !lastSync.IsZero() {
		if min := time.Duration(cfg.SyncIntervalMinutes) * time.Minute; time.Since(lastSync) < min {
			return &syncResult{
				Skipped:    true,
				SkipReason: fmt.Sprintf("last sync %s ago; next run after %s", time.Since(lastSync).Round(time.Second), lastSync.Add(min).Format(time.RFC3339)),
			}, nil
		}
	}
	host := sdkruntime.Host()
	if host == nil {
		return nil, fmt.Errorf("host connection unavailable; the plugin may still be starting")
	}

	// Resolve destination libraries: explicit names (or legacy numeric
	// IDs) from config, falling back to auto-detecting the first
	// Movies/Series-type library on the host.
	movieLibID, seriesLibID, err := resolveLibraryIDs(ctx, host, cfg)
	if err != nil {
		return nil, err
	}

	res := &syncResult{StartedAt: time.Now(), Lists: map[string]listStat{}}
	pmdb := newPMDBClient(cfg.PMDBAPIKey)
	tmdb := newTMDBClient(cfg.TMDBAPIKey)

	keepSet := map[string]bool{}
	var keepIDs []string
	seenTitles := map[string]bool{} // tmdbID:mediaType dedup across lists

	for _, entry := range cfg.PMDBLists {
		stat := listStat{}
		// Public list URLs are resolved to IDs on every sync; raw IDs
		// (your own lists) pass through untouched.
		listID, label, err := resolveListEntry(entry)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("list %q: %v", entry, redactSecrets(err.Error())))
			continue
		}
		items, err := pmdb.listItems(listID)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("list %s: %v", label, redactSecrets(err.Error())))
			continue
		}
		stat.Items = len(items)
		for _, it := range items {
			key := fmt.Sprintf("%d:%s", it.TMDBID, it.MediaType)
			if seenTitles[key] {
				continue
			}
			seenTitles[key] = true
			title, err := tmdb.fetchTitle(it.TMDBID, it.MediaType)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("tmdb %d (%s): %v", it.TMDBID, it.MediaType, redactSecrets(err.Error())))
				continue
			}
			if title.IMDbID == "" {
				res.SkippedIMDb++
				continue
			}
			req, err := buildRegistration(cfg, title, movieLibID, seriesLibID)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", title.Title, err))
				continue
			}
			out, err := host.UpsertVirtualMedia(ctx, *req)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("register %s: %v", title.Title, err))
				continue
			}
			stat.Registered++
			if out != nil && out.MediaID != "" && !keepSet[out.MediaID] {
				keepSet[out.MediaID] = true
				keepIDs = append(keepIDs, out.MediaID)
			}
			time.Sleep(100 * time.Millisecond)
		}
		res.Lists[label] = stat
		res.Registered += stat.Registered
	}

	// Reconcile only on a fully clean run: a failed list fetch or title
	// lookup must never look like "the user deleted everything".
	if len(res.Errors) == 0 && len(cfg.PMDBLists) > 0 {
		libIDs := []string{}
		if movieLibID != "" {
			libIDs = append(libIDs, movieLibID)
		}
		if seriesLibID != "" {
			libIDs = append(libIDs, seriesLibID)
		}
		rec, err := host.ReconcileVirtualMedia(ctx, syncSourceKey, keepIDs, libIDs)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("reconcile: %v", err))
		} else {
			res.Reconciled = true
			if rec != nil {
				res.Removed = int(rec.GetItemsRemoved())
			}
		}
	}

	res.FinishedAt = time.Now()

	s.mu.Lock()
	s.lastSync = res.FinishedAt
	s.lastResult = res
	statePath := resolveStatePath(cfg.StateFile)
	s.mu.Unlock()
	savePersistedState(statePath, &persistedState{LastSync: res.FinishedAt, LastResult: res, MediaIDs: keepIDs})

	return res, nil
}

// resolveLibraryIDs turns the configured library names (or legacy numeric
// IDs) into host library IDs. Blank names auto-detect the first library of
// the matching media type.
func resolveLibraryIDs(ctx context.Context, host *runtimehost.Client, cfg pluginConfig) (movieID, seriesID string, err error) {
	if cfg.MovieLibraryID > 0 {
		movieID = strconv.Itoa(cfg.MovieLibraryID)
	}
	if cfg.SeriesLibraryID > 0 {
		seriesID = strconv.Itoa(cfg.SeriesLibraryID)
	}
	if movieID != "" && seriesID != "" {
		return movieID, seriesID, nil
	}
	libs, err := host.ListLibraries(ctx, "")
	if err != nil {
		return "", "", fmt.Errorf("list libraries: %w", err)
	}
	if movieID == "" {
		movieID, err = pickLibrary(libs, cfg.MovieLibrary, "movie", "Movies")
		if err != nil {
			return "", "", err
		}
	}
	if seriesID == "" {
		seriesID, err = pickLibrary(libs, cfg.SeriesLibrary, "tv", "Series")
		if err != nil {
			return "", "", err
		}
	}
	return movieID, seriesID, nil
}

// pickLibrary finds a library by name (case-insensitive), or — when name is
// blank — the first library with the given media type.
func pickLibrary(libs []*pluginv1.Library, name, mediaType, label string) (string, error) {
	if strings.TrimSpace(name) != "" {
		for _, l := range libs {
			if strings.EqualFold(strings.TrimSpace(l.GetName()), strings.TrimSpace(name)) {
				return l.GetId(), nil
			}
		}
		available := []string{}
		for _, l := range libs {
			available = append(available, l.GetName())
		}
		return "", fmt.Errorf("%s library %q not found (available: %s)", label, name, strings.Join(available, ", "))
	}
	for _, l := range libs {
		if l.GetMediaType() == mediaType {
			return l.GetId(), nil
		}
	}
	return "", fmt.Errorf("no %s library found on the host; name one in the plugin settings", label)
}

// buildRegistration turns a resolved TMDB title into a virtual-media request.
// Movies get a canonical virtual:// URI; series attach per-episode URIs (the
// host rejects a series-level URI because there is no playable file at the
// series container itself).
func buildRegistration(cfg pluginConfig, t *tmdbTitle, movieLibID, seriesLibID string) (*runtimehost.VirtualMediaRequest, error) {
	if strings.TrimSpace(t.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	// The host's UpsertVirtualMedia only accepts "movie"/"series"; TMDB/PMDB
	// use "movie"/"tv", so map here.
	hostMediaType := "series"
	isMovie := t.MediaType == "movie"
	if isMovie {
		hostMediaType = "movie"
	}
	req := &runtimehost.VirtualMediaRequest{
		MediaType:      hostMediaType,
		Title:          t.Title,
		Year:           t.Year,
		IMDbID:         t.IMDbID,
		TMDBID:         strconv.Itoa(t.TMDBID),
		Overview:       t.Overview,
		Genres:         t.Genres,
		PosterPath:     t.PosterURL,
		BackdropPath:   t.BackdropURL,
		RuntimeMinutes: t.Runtime,
		SourceKey:      syncSourceKey,
	}
	if isMovie {
		if movieLibID == "" {
			return nil, fmt.Errorf("no Movies library configured; name one in the plugin settings")
		}
		req.LibraryID = movieLibID
		req.VirtualURI = "virtual://" + "movie/" + t.IMDbID
	} else {
		if seriesLibID == "" {
			return nil, fmt.Errorf("no Series library configured; name one in the plugin settings")
		}
		req.LibraryID = seriesLibID
		for _, ep := range t.Episodes {
			uri := fmt.Sprintf("virtual://series/%s/%d/%d", t.IMDbID, ep.Season, ep.Episode)
			var air time.Time
			if ep.AirDate != "" {
				air, _ = time.Parse("2006-01-02", ep.AirDate)
			}
			req.Episodes = append(req.Episodes, runtimehost.VirtualEpisode{
				SeasonNumber:   ep.Season,
				EpisodeNumber:  ep.Episode,
				Title:          ep.Title,
				Overview:       ep.Overview,
				AirDate:        air,
				RuntimeMinutes: ep.Runtime,
				StillPath:      ep.StillURL,
				VirtualURI:     uri,
			})
		}
		if len(req.Episodes) == 0 {
			return nil, fmt.Errorf("series has no episodes to register")
		}
	}
	return req, nil
}
