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

	pb "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtimedefault"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/types/known/structpb"
)

//go:embed manifest.json
var manifestJSON []byte

const configKey = "sync"
const taskKey = "sync-pmdb-lists"

type pluginConfig struct {
	PMDBAPIKey          string
	PMDBListIDs         []string
	TMDBAPIKey          string
	SyncIntervalMinutes int
	StateFile           string
}

func (c pluginConfig) validate() error {
	if strings.TrimSpace(c.PMDBAPIKey) == "" {
		return fmt.Errorf("PublicMetaDB API key is not configured")
	}
	if len(c.PMDBListIDs) == 0 {
		return fmt.Errorf("no PublicMetaDB list IDs configured")
	}
	if strings.TrimSpace(c.TMDBAPIKey) == "" {
		return fmt.Errorf("TMDB API key is not configured")
	}
	return nil
}

type runtimeServer struct {
	runtimedefault.Server
	pb.UnimplementedScheduledTaskServer
	pb.UnimplementedHttpRoutesServer
	manifest    *pb.PluginManifest
	mu          sync.Mutex
	cfg         pluginConfig
	lastSync    time.Time
	lastSummary *syncSummary
	logger      hclog.Logger
}

func (s *runtimeServer) GetManifest(context.Context, *pb.GetManifestRequest) (*pb.GetManifestResponse, error) {
	return &pb.GetManifestResponse{Manifest: s.manifest}, nil
}

// Configure receives the "sync" global config entry from Silo.
func (s *runtimeServer) Configure(_ context.Context, request *pb.ConfigureRequest) (*pb.ConfigureResponse, error) {
	cfg := pluginConfig{SyncIntervalMinutes: 720}
	for _, entry := range request.GetConfig() {
		if entry.GetKey() != configKey {
			continue
		}
		values := entry.GetValue().AsMap()
		cfg.PMDBAPIKey = strings.TrimSpace(strVal(values["pmdb_api_key"]))
		cfg.TMDBAPIKey = strings.TrimSpace(strVal(values["tmdb_api_key"]))
		cfg.PMDBListIDs = parseListIDs(values["pmdb_list_ids"])
		if n, ok := intVal(values["sync_interval_minutes"]); ok && n >= 15 {
			cfg.SyncIntervalMinutes = n
		}
		cfg.StateFile = strings.TrimSpace(strVal(values["state_file"]))
	}
	if err := cfg.validate(); err != nil {
		s.logger.Warn("PMDB Lists configuration incomplete", "error", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	if c := loadCache(resolveStatePath(cfg.StateFile)); !c.SyncedAt.IsZero() {
		s.lastSync = c.SyncedAt
	}
	s.mu.Unlock()
	return &pb.ConfigureResponse{}, nil
}

// Run handles the scheduled task trigger from Silo.
func (s *runtimeServer) Run(ctx context.Context, req *pb.RunScheduledTaskRequest) (*pb.RunScheduledTaskResponse, error) {
	if req.GetTaskKey() != "" && req.GetTaskKey() != taskKey {
		return nil, fmt.Errorf("unknown task key %q", req.GetTaskKey())
	}
	sum, err := s.doSync(ctx, false)
	if err != nil {
		out, _ := structpb.NewStruct(map[string]any{"error": fmt.Sprintf("sync failed: %v", err)})
		return &pb.RunScheduledTaskResponse{Output: out}, err
	}
	out, _ := structpb.NewStruct(map[string]any{
		"summary": fmt.Sprintf("lists=%d titles=%d errors=%d skipped=%v", sum.Lists, sum.Titles, len(sum.Errors), sum.Skipped),
	})
	return &pb.RunScheduledTaskResponse{Output: out}, nil
}

// --- HTTP routes ---

var secretParamPattern = regexp.MustCompile(`(?i)([?&](?:api_?key|apikey|token|secret|password|access_?token)=)[^&\s'"]+`)

func redactSecrets(s string) string {
	return secretParamPattern.ReplaceAllString(s, "${1}[redacted]")
}

func userRole(req *pb.HandleHTTPRequest) string {
	if r := req.GetHeaders()["X-Silo-User-Role"]; r != "" {
		return r
	}
	return req.GetHeaders()["X-Vio-User-Role"]
}

func (s *runtimeServer) Handle(ctx context.Context, req *pb.HandleHTTPRequest) (*pb.HandleHTTPResponse, error) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	cache := loadCache(resolveStatePath(cfg.StateFile))

	path := strings.TrimRight(req.GetPath(), "/")
	method := req.GetMethod()

	switch {
	case path == "/pmdb-lists" && method == http.MethodGet:
		lastErr := ""
		if s.lastSummary != nil && len(s.lastSummary.Errors) > 0 {
			lastErr = "Last sync had " + strconv.Itoa(len(s.lastSummary.Errors)) + " errors; see status for details."
		}
		return httpHTML(renderIndex(cache, lastErr)), nil
	case strings.HasPrefix(path, "/pmdb-lists/api/status") && method == http.MethodGet:
		return s.statusJSON(cache), nil
	case strings.HasPrefix(path, "/pmdb-lists/api/sync") && method == http.MethodPost:
		if !strings.EqualFold(userRole(req), "admin") {
			return httpJSON(http.StatusForbidden, map[string]string{"error": "admin access required"}), nil
		}
		sum, err := s.doSync(ctx, true)
		if err != nil {
			return httpJSON(http.StatusBadRequest, map[string]string{"error": err.Error()}), nil
		}
		return httpJSON(http.StatusOK, sum), nil
	case strings.HasPrefix(path, "/pmdb-lists/") && method == http.MethodGet:
		listID := strings.TrimPrefix(path, "/pmdb-lists/")
		if strings.Contains(listID, "/") {
			break
		}
		cl, ok := cache.Lists[listID]
		if !ok {
			return httpHTML(pageShell("Not found", `<header><h1><a href="/pmdb-lists">PMDB Lists</a></h1></header><p class="meta">Unknown list. It may not have synced yet.</p>`)), nil
		}
		filter := ""
		if q := req.GetQuery(); q != nil {
			filter = q.GetFields()["filter"].GetStringValue()
		}
		if filter != "missing" {
			filter = "all"
		}
		presence := s.checkPresence(ctx, cl)
		return httpHTML(renderListDetail(cl, presence, filter, cache.SyncedAt.Format("Jan 2, 2006 15:04"))), nil
	}
	return httpJSON(http.StatusNotFound, map[string]string{"error": "not found"}), nil
}

// checkPresence badges each cached title with whether Silo already has it,
// using TMDB ids (the only v1 provider). Failures are non-fatal: the page
// simply renders without badges.
func (s *runtimeServer) checkPresence(ctx context.Context, cl *cachedList) map[string]bool {
	out := map[string]bool{}
	host := sdkruntime.Host()
	if host == nil {
		return out
	}
	byType := map[string][]string{"movie": {}, "tv": {}}
	for _, it := range cl.Items {
		byType[it.MediaType] = append(byType[it.MediaType], strconv.Itoa(it.TMDBID))
	}
	for mediaType, ids := range byType {
		if len(ids) == 0 {
			continue
		}
		present, err := host.CheckMediaPresence(ctx, "tmdb", mediaType, ids)
		if err != nil {
			s.logger.Warn("presence check failed", "error", err)
			continue
		}
		for id := range present {
			out[id] = true
		}
	}
	return out
}

func (s *runtimeServer) statusJSON(cache *listCache) *pb.HandleHTTPResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	lists := map[string]any{}
	for id, cl := range cache.Lists {
		lists[id] = map[string]any{"name": cl.Name, "titles": len(cl.Items)}
	}
	out := map[string]any{
		"configured_lists":      len(s.cfg.PMDBListIDs),
		"synced_lists":          lists,
		"sync_interval_minutes": s.cfg.SyncIntervalMinutes,
		"last_sync":             nil,
		"last_summary":          nil,
	}
	if !s.lastSync.IsZero() {
		out["last_sync"] = s.lastSync.Format(time.RFC3339)
	}
	if s.lastSummary != nil {
		out["last_summary"] = s.lastSummary
	}
	return httpJSON(http.StatusOK, out)
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

func parseListIDs(v any) []string {
	var ids []string
	add := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(strings.Trim(line, ","))
			if line != "" {
				ids = append(ids, line)
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
	return ids
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
	logger := hclog.New(&hclog.LoggerOptions{Name: "silo-pmdb-lists"})
	manifest, err := loadManifest()
	if err != nil {
		panic(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "manifest" {
		out, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(out))
		return
	}
	server := &runtimeServer{manifest: manifest, logger: logger}
	sdkruntime.Serve(sdkruntime.ServeConfig{
		Logger: logger,
		Servers: sdkruntime.CapabilityServers{
			Runtime:       server,
			ScheduledTask: server,
			HttpRoutes:    server,
		},
	})
}
