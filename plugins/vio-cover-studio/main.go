package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtimedefault"
	"github.com/hashicorp/go-hclog"
)

//go:embed manifest.json
var manifestJSON []byte

const configKey = "covers"
const taskKey = "rebuild-covers"
const stateFileName = ".vio-cover-studio-state.json"

type collectionOverride struct {
	OverlayMode string `json:"overlay_mode,omitempty"`
	Layout      string `json:"layout,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

type buildRecord struct {
	At           time.Time `json:"at"`
	Success      bool      `json:"success"`
	Error        string    `json:"error,omitempty"`
	PosterOK     bool      `json:"poster_ok,omitempty"`
	BackdropOK   bool      `json:"backdrop_ok,omitempty"`
	MembersUsed  int       `json:"members_used,omitempty"`
	OverlayUsed  string    `json:"overlay_used,omitempty"`
	Forced       bool      `json:"forced,omitempty"`
}

type pluginState struct {
	Overrides map[string]*collectionOverride `json:"overrides,omitempty"`
	Builds    map[string]*buildRecord        `json:"builds,omitempty"`
}

type pluginConfig struct {
	VioURL       string
	VioAPIKey    string
	TMDBAPIKey   string
	Collections  []string // collection IDs, one per line; empty = all manual
	OverlayMode  string   // logo|text|none
	Layout       string   // grid|spotlight|columns
	RefreshHours int
}

func (c pluginConfig) validate() error {
	if strings.TrimSpace(c.VioURL) == "" {
		return fmt.Errorf("server URL is not configured")
	}
	if strings.TrimSpace(c.VioAPIKey) == "" {
		return fmt.Errorf("server API key is not configured")
	}
	if strings.TrimSpace(c.TMDBAPIKey) == "" {
		return fmt.Errorf("TMDB API key is not configured")
	}
	switch c.OverlayMode {
	case "logo", "text", "none":
	default:
		return fmt.Errorf("overlay mode must be logo, text, or none")
	}
	switch c.Layout {
	case "grid", "spotlight", "columns":
	default:
		return fmt.Errorf("layout must be grid, spotlight, or columns")
	}
	if c.RefreshHours < 1 {
		return fmt.Errorf("refresh hours must be at least 1")
	}
	return nil
}

func (c pluginConfig) manages(id string) bool {
	if len(c.Collections) == 0 {
		return true
	}
	for _, want := range c.Collections {
		if want == id {
			return true
		}
	}
	return false
}

func (c pluginConfig) enabled(id string, st *pluginState) bool {
	if ov := st.Overrides[id]; ov != nil && ov.Enabled != nil {
		return *ov.Enabled
	}
	return c.manages(id)
}

func (c pluginConfig) overlayFor(id string, st *pluginState) string {
	if ov := st.Overrides[id]; ov != nil && ov.OverlayMode != "" {
		return ov.OverlayMode
	}
	return c.OverlayMode
}

func (c pluginConfig) layoutFor(id string, st *pluginState) string {
	if ov := st.Overrides[id]; ov != nil && ov.Layout != "" {
		return ov.Layout
	}
	return c.Layout
}

type runtimeServer struct {
	runtimedefault.Server
	pb.UnimplementedScheduledTaskServer
	pb.UnimplementedHttpRoutesServer
	manifest *pb.PluginManifest
	mu       sync.Mutex
	cfg      pluginConfig
	state    *pluginState
	logger   hclog.Logger
}

func (s *runtimeServer) GetManifest(context.Context, *pb.GetManifestRequest) (*pb.GetManifestResponse, error) {
	return &pb.GetManifestResponse{Manifest: s.manifest}, nil
}

// Configure receives the "covers" global config entry from the host.
func (s *runtimeServer) Configure(_ context.Context, request *pb.ConfigureRequest) (*pb.ConfigureResponse, error) {
	cfg := pluginConfig{
		VioURL:       "http://127.0.0.1:8080",
		OverlayMode:  "logo",
		Layout:       "grid",
		RefreshHours: 24,
	}
	for _, entry := range request.GetConfig() {
		if entry.GetKey() != configKey {
			continue
		}
		values := entry.GetValue().AsMap()
		if v := strings.TrimSpace(strVal(values["vio_url"])); v != "" {
			cfg.VioURL = strings.TrimRight(v, "/")
		}
		cfg.VioAPIKey = strings.TrimSpace(strVal(values["vio_api_key"]))
		cfg.TMDBAPIKey = strings.TrimSpace(strVal(values["tmdb_api_key"]))
		cfg.Collections = parseLines(values["collections"])
		if v := strings.TrimSpace(strVal(values["overlay_mode"])); v != "" {
			cfg.OverlayMode = v
		}
		if v := strings.TrimSpace(strVal(values["layout"])); v != "" {
			cfg.Layout = v
		}
		if n, ok := intVal(values["refresh_hours"]); ok && n >= 1 {
			cfg.RefreshHours = n
		}
	}
	if err := cfg.validate(); err != nil {
		// Log but do not fail: the admin UI must stay usable so the
		// operator can fix the configuration.
		s.logger.Warn("Cover Studio configuration incomplete", "error", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	s.state = loadState(resolveStatePath(""))
	s.mu.Unlock()
	return &pb.ConfigureResponse{}, nil
}

// Run handles the scheduled task trigger from the host.
func (s *runtimeServer) Run(ctx context.Context, req *pb.RunScheduledTaskRequest) (*pb.RunScheduledTaskResponse, error) {
	if req.GetTaskKey() != "" && req.GetTaskKey() != taskKey {
		return nil, fmt.Errorf("unknown task key %q", req.GetTaskKey())
	}
	res, err := s.rebuildAll(ctx, false)
	out, _ := structpb.NewStruct(map[string]any{"summary": summarizeRebuild(res)})
	if err != nil {
		return &pb.RunScheduledTaskResponse{Output: out}, err
	}
	return &pb.RunScheduledTaskResponse{Output: out}, nil
}

func summarizeRebuild(res *rebuildResult) string {
	if res == nil {
		return "no result"
	}
	return fmt.Sprintf("collections=%d rebuilt=%d skipped=%d failed=%d",
		res.Total, res.Rebuilt, res.Skipped, res.Failed)
}

// --- admin HTTP routes ---

func (s *runtimeServer) Handle(ctx context.Context, req *pb.HandleHTTPRequest) (*pb.HandleHTTPResponse, error) {
	role := req.GetHeaders()["X-Vio-User-Role"]
	if role == "" {
		role = req.GetHeaders()["X-Silo-User-Role"]
	}
	if !strings.EqualFold(role, "admin") {
		return httpJSON(http.StatusForbidden, map[string]string{"error": "admin access required"}), nil
	}
	path := strings.TrimRight(req.GetPath(), "/")
	query := map[string]string{}
	if q := req.GetQuery(); q != nil {
		for k, v := range q.AsMap() {
			query[k] = strVal(v)
		}
	}
	switch {
	case path == "/admin/cover-studio" && req.GetMethod() == http.MethodGet:
		return httpHTML(adminPageHTML), nil
	case path == "/admin/cover-studio/status" && req.GetMethod() == http.MethodGet:
		return s.statusJSON(), nil
	case path == "/admin/cover-studio/preview" && req.GetMethod() == http.MethodGet:
		return s.previewPNG(ctx, query["collection_id"], query["kind"]), nil
	case path == "/admin/cover-studio/rebuild" && req.GetMethod() == http.MethodPost:
		return s.rebuildOneJSON(ctx, query["collection_id"]), nil
	case path == "/admin/cover-studio/options" && req.GetMethod() == http.MethodPost:
		return s.saveOptionsJSON(req), nil
	default:
		return httpJSON(http.StatusNotFound, map[string]string{"error": "not found"}), nil
	}
}

func (s *runtimeServer) statusJSON() *pb.HandleHTTPResponse {
	s.mu.Lock()
	cfg, st := s.cfg, s.state
	s.mu.Unlock()
	cols, err := listCollections(cfg)
	items := make([]map[string]any, 0)
	if err == nil {
		for _, c := range cols {
			id := c.ID
			rec := st.Builds[id]
			item := map[string]any{
				"id":      id,
				"title":   c.Title,
				"enabled": cfg.enabled(id, st),
				"overlay": cfg.overlayFor(id, st),
				"layout":  cfg.layoutFor(id, st),
			}
			if rec != nil {
				item["last_build"] = map[string]any{
					"at":           rec.At.Format(time.RFC3339),
					"success":      rec.Success,
					"error":        rec.Error,
					"poster_ok":    rec.PosterOK,
					"backdrop_ok":  rec.BackdropOK,
					"members_used": rec.MembersUsed,
					"overlay_used": rec.OverlayUsed,
				}
			}
			items = append(items, item)
		}
	}
	out := map[string]any{
		"overlay_mode":  cfg.OverlayMode,
		"layout":        cfg.Layout,
		"refresh_hours": cfg.RefreshHours,
		"collections":   items,
	}
	if err != nil {
		out["error"] = redactSecrets(err.Error())
	}
	return httpJSON(http.StatusOK, out)
}

func (s *runtimeServer) previewPNG(ctx context.Context, collectionID, kind string) *pb.HandleHTTPResponse {
	s.mu.Lock()
	cfg, st := s.cfg, s.state
	s.mu.Unlock()
	if err := cfg.validate(); err != nil {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if strings.TrimSpace(collectionID) == "" {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": "collection_id is required"})
	}
	png, err := buildPreview(ctx, cfg, st, collectionID, kind)
	if err != nil {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": redactSecrets(err.Error())})
	}
	return &pb.HandleHTTPResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "image/png"},
		Body:       png,
	}
}

func (s *runtimeServer) rebuildOneJSON(ctx context.Context, collectionID string) *pb.HandleHTTPResponse {
	s.mu.Lock()
	cfg, st := s.cfg, s.state
	s.mu.Unlock()
	if err := cfg.validate(); err != nil {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	rec, err := s.rebuildCollection(ctx, cfg, st, collectionID, true)
	if err != nil {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": redactSecrets(err.Error())})
	}
	return httpJSON(http.StatusOK, rec)
}

func (s *runtimeServer) saveOptionsJSON(req *pb.HandleHTTPRequest) *pb.HandleHTTPResponse {
	var body struct {
		CollectionID string `json:"collection_id"`
		Enabled      *bool  `json:"enabled"`
		OverlayMode  string `json:"overlay_mode"`
		Layout       string `json:"layout"`
	}
	if err := json.Unmarshal(req.GetBody(), &body); err != nil {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
	}
	if strings.TrimSpace(body.CollectionID) == "" {
		return httpJSON(http.StatusBadRequest, map[string]string{"error": "collection_id is required"})
	}
	if body.OverlayMode != "" {
		switch body.OverlayMode {
		case "logo", "text", "none":
		default:
			return httpJSON(http.StatusBadRequest, map[string]string{"error": "overlay_mode must be logo, text, or none"})
		}
	}
	if body.Layout != "" {
		switch body.Layout {
		case "grid", "spotlight", "columns":
		default:
			return httpJSON(http.StatusBadRequest, map[string]string{"error": "layout must be grid, spotlight, or columns"})
		}
	}
	s.mu.Lock()
	if s.state.Overrides == nil {
		s.state.Overrides = map[string]*collectionOverride{}
	}
	ov := s.state.Overrides[body.CollectionID]
	if ov == nil {
		ov = &collectionOverride{}
		s.state.Overrides[body.CollectionID] = ov
	}
	if body.Enabled != nil {
		ov.Enabled = body.Enabled
	}
	if body.OverlayMode != "" {
		ov.OverlayMode = body.OverlayMode
	}
	if body.Layout != "" {
		ov.Layout = body.Layout
	}
	saveState(resolveStatePath(""), s.state)
	s.mu.Unlock()
	return httpJSON(http.StatusOK, map[string]string{"ok": "true"})
}

func httpJSON(status int, v any) *pb.HandleHTTPResponse {
	body, _ := json.MarshalIndent(v, "", "  ")
	return &pb.HandleHTTPResponse{
		StatusCode: int32(status),
		Headers:    map[string]string{"Content-Type": "application/json; charset=utf-8"},
		Body:       body,
	}
}

func httpHTML(body string) *pb.HandleHTTPResponse {
	return &pb.HandleHTTPResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/html; charset=utf-8"},
		Body:       []byte(body),
	}
}

var secretParamPattern = regexp.MustCompile(`(?i)([?&](?:api_?key|apikey|token|secret|password|access_?token)=)[^&\s'"]+`)

func redactSecrets(s string) string {
	return secretParamPattern.ReplaceAllString(s, "${1}[redacted]")
}

// --- config value helpers ---

func strVal(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func intVal(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		if t > 0 && t == float64(int(t)) {
			return int(t), true
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func parseLines(v any) []string {
	var out []string
	add := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(strings.Trim(line, ","))
			if line != "" {
				out = append(out, line)
			}
		}
	}
	switch t := v.(type) {
	case string:
		add(t)
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				add(s)
			}
		}
	}
	return out
}

// --- manifest + serve ---

func loadManifest() (*pb.PluginManifest, error) {
	manifest, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		return nil, fmt.Errorf("load embedded manifest: %w", err)
	}
	if manifest.Checksum == "" {
		if executable, err := os.Executable(); err == nil {
			if binary, err := os.ReadFile(executable); err == nil {
				sum := sha256.Sum256(binary)
				manifest.Checksum = hex.EncodeToString(sum[:])
			}
		}
	}
	return manifest, nil
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "vio-cover-studio"})
	manifest, err := loadManifest()
	if err != nil {
		panic(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "manifest" {
		out, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(out))
		return
	}
	server := &runtimeServer{manifest: manifest, logger: logger, state: &pluginState{}}
	sdkruntime.Serve(sdkruntime.ServeConfig{
		Logger: logger,
		Servers: sdkruntime.CapabilityServers{
			Runtime:       server,
			ScheduledTask: server,
			HttpRoutes:    server,
		},
	})
}
