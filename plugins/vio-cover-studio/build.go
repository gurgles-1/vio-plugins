package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type rebuildResult struct {
	Total   int
	Rebuilt int
	Skipped int
	Failed  int
	Errors  []string
}

// rebuildAll rebuilds every enabled collection, skipping ones built within
// refresh_hours unless forced.
func (s *runtimeServer) rebuildAll(ctx context.Context, forced bool) (*rebuildResult, error) {
	s.mu.Lock()
	cfg, st := s.cfg, s.state
	s.mu.Unlock()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cols, err := listCollections(cfg)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	res := &rebuildResult{}
	cutoff := time.Now().Add(-time.Duration(cfg.RefreshHours) * time.Hour)
	for _, col := range cols {
		if !cfg.enabled(col.ID, st) {
			continue
		}
		res.Total++
		if !forced {
			if rec := st.Builds[col.ID]; rec != nil && rec.Success && rec.At.After(cutoff) {
				res.Skipped++
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		rec, err := s.rebuildCollection(ctx, cfg, st, col.ID, forced)
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, col.Title+": "+redactSecrets(err.Error()))
			continue
		}
		if rec.Success {
			res.Rebuilt++
		} else {
			res.Failed++
			res.Errors = append(res.Errors, col.Title+": "+rec.Error)
		}
	}
	return res, nil
}

// rebuildCollection builds and uploads covers for one collection.
func (s *runtimeServer) rebuildCollection(ctx context.Context, cfg pluginConfig, st *pluginState, collectionID string, forced bool) (*buildRecord, error) {
	rec := &buildRecord{At: time.Now(), Forced: forced}
	vio := newVioClient(cfg.VioURL, cfg.VioAPIKey)

	cols, err := vio.listCollections()
	if err != nil {
		return rec, fmt.Errorf("list collections: %w", err)
	}
	var col *vioCollection
	for i := range cols {
		if cols[i].ID == collectionID {
			col = &cols[i]
			break
		}
	}
	if col == nil {
		return rec, fmt.Errorf("collection %q not found", collectionID)
	}

	members, err := vio.collectionMembers(collectionID, 60)
	if err != nil {
		return rec, fmt.Errorf("collection members: %w", err)
	}
	var titles []*vioTitle
	for _, m := range members {
		if err := ctx.Err(); err != nil {
			return rec, err
		}
		t, err := vio.catalogTitle(m.MediaItemID)
		if err != nil {
			continue
		}
		titles = append(titles, t)
		if len(titles) >= maxPosters {
			break
		}
	}
	posters := fetchPosterImages(titles, maxPosters)
	if len(posters) < minPosters {
		rec.Error = fmt.Sprintf("only %d usable posters (need %d)", len(posters), minPosters)
		s.recordBuild(st, collectionID, rec)
		return rec, nil
	}
	rec.MembersUsed = len(posters)

	overlayMode := cfg.overlayFor(collectionID, st)
	layout := cfg.layoutFor(collectionID, st)
	var logoPNG []byte
	logoSource := ""
	if overlayMode == "logo" {
		tmdb := newTMDBLogoClient(cfg.TMDBAPIKey)
		raw, src, err := tmdb.resolveCollectionLogo(col.Title)
		if err == nil && len(raw) > 0 {
			logoPNG, logoSource = raw, src
		} else {
			// Graceful fallback: no usable logo -> title text.
			overlayMode = "text"
		}
	}
	rec.OverlayUsed = overlayMode
	if logoSource != "" {
		rec.OverlayUsed = "logo(" + logoSource + ")"
	}

	portraitPNG, landscapePNG, err := renderCover(posters, col.Title, logoPNG, overlayMode, layout)
	if err != nil {
		rec.Error = fmt.Sprintf("render: %v", err)
		s.recordBuild(st, collectionID, rec)
		return rec, nil
	}

	posterOK, err := vio.uploadImage(collectionID, "poster", portraitPNG)
	if err != nil {
		rec.Error = fmt.Sprintf("poster upload: %v", err)
		s.recordBuild(st, collectionID, rec)
		return rec, nil
	}
	rec.PosterOK = posterOK

	backdropOK, err := vio.uploadImage(collectionID, "backdrop", landscapePNG)
	if err != nil {
		rec.Error = fmt.Sprintf("backdrop upload: %v", err)
		s.recordBuild(st, collectionID, rec)
		return rec, nil
	}
	rec.BackdropOK = backdropOK
	rec.Success = posterOK // poster is required; backdrop is best-effort
	if !posterOK {
		rec.Error = "poster endpoint missing"
	}
	s.recordBuild(st, collectionID, rec)
	return rec, nil
}

func (s *runtimeServer) recordBuild(st *pluginState, collectionID string, rec *buildRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.Builds == nil {
		st.Builds = map[string]*buildRecord{}
	}
	st.Builds[collectionID] = rec
	saveState(resolveStatePath(""), st)
}

// buildPreview renders a cover on demand without uploading.
func buildPreview(ctx context.Context, cfg pluginConfig, st *pluginState, collectionID, kind string) ([]byte, error) {
	vio := newVioClient(cfg.VioURL, cfg.VioAPIKey)
	cols, err := vio.listCollections()
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	var col *vioCollection
	for i := range cols {
		if cols[i].ID == collectionID {
			col = &cols[i]
			break
		}
	}
	if col == nil {
		return nil, fmt.Errorf("collection %q not found", collectionID)
	}
	members, err := vio.collectionMembers(collectionID, 60)
	if err != nil {
		return nil, fmt.Errorf("collection members: %w", err)
	}
	var titles []*vioTitle
	for _, m := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, err := vio.catalogTitle(m.MediaItemID)
		if err != nil {
			continue
		}
		titles = append(titles, t)
		if len(titles) >= maxPosters {
			break
		}
	}
	posters := fetchPosterImages(titles, maxPosters)
	if len(posters) < minPosters {
		return nil, fmt.Errorf("only %d usable posters (need %d)", len(posters), minPosters)
	}
	overlayMode := cfg.overlayFor(collectionID, st)
	layout := cfg.layoutFor(collectionID, st)
	var logoPNG []byte
	if overlayMode == "logo" {
		tmdb := newTMDBLogoClient(cfg.TMDBAPIKey)
		raw, _, _ := tmdb.resolveCollectionLogo(col.Title)
		if len(raw) > 0 {
			logoPNG = raw
		} else {
			overlayMode = "text"
		}
	}
	portraitPNG, landscapePNG, err := renderCover(posters, col.Title, logoPNG, overlayMode, layout)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(kind) == "landscape" {
		return landscapePNG, nil
	}
	return portraitPNG, nil
}
