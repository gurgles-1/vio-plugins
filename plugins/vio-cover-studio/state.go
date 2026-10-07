package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// resolveStatePath mirrors the reference plugin: the state file lives under
// SILO_PLUGIN_CACHE_DIR/<plugin-id>/, falling back to the working directory.
func resolveStatePath(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		file = stateFileName
	}
	if filepath.IsAbs(file) {
		return file
	}
	if base := strings.TrimSpace(os.Getenv("SILO_PLUGIN_CACHE_DIR")); base != "" {
		return filepath.Join(base, "com.gurgles-1.vio-cover-studio", file)
	}
	return file
}

func loadState(path string) *pluginState {
	st := &pluginState{}
	if path == "" {
		return st
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(raw, st); err != nil {
		return &pluginState{}
	}
	if st.Overrides == nil {
		st.Overrides = map[string]*collectionOverride{}
	}
	if st.Builds == nil {
		st.Builds = map[string]*buildRecord{}
	}
	return st
}

func saveState(path string, st *pluginState) {
	if path == "" || st == nil {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644)
}
