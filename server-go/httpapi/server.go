// Package httpapi is the transport. Chiron is a container of subjects; each has
// its own corpus and its own learner state.
//
// One transport-agnostic exchange endpoint per interaction: the app reads fully
// detached, and at a chapter boundary POSTs everything that happened (beat
// responses, check answers, confidence ratings, timings, override and catch-up
// requests) and receives grades, the gate result, the next chapter and updated
// state - over Wi-Fi directly, or pushed through iproxy on the USB path.
package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mjbraun/chiron/server/agent"
	"github.com/mjbraun/chiron/server/auth"
	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/devreq"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/pages"
	"github.com/mjbraun/chiron/server/primer"
	"github.com/mjbraun/chiron/server/state"
)

type SubjectSpec struct {
	ID        string `yaml:"id"`
	Title     string `yaml:"title"`
	CorpusDir string `yaml:"corpus_dir"`
	StateDir  string `yaml:"state_dir"`
}

type SessionConfig struct {
	MasteryGate          float64 `yaml:"mastery_gate"`
	ExtensionTrigger     float64 `yaml:"extension_trigger"`
	CheckItems           int     `yaml:"check_items"`
	CallbackFraction     float64 `yaml:"callback_fraction"`
	ChunkMinutes         float64 `yaml:"chunk_minutes"`
	BreakMinutes         float64 `yaml:"break_minutes"`
	LongBreakEveryChunks float64 `yaml:"long_break_every_chunks"`
}

type Config struct {
	Subjects  []SubjectSpec `yaml:"subjects"`
	StaticDir string        `yaml:"static_dir"`
	// KatexDir points at the KaTeX assets used when rendering chapters to
	// page images for e-ink clients (the same files the iPad bundles).
	KatexDir string `yaml:"katex_dir"`
	// FontsDir holds the bundled page faces (Source Serif 4, Source Sans 3)
	// referenced by the page stylesheet.
	FontsDir string `yaml:"fonts_dir"`
	// VisionModel transcribes handwritten ink submissions (loaded on demand
	// by the same OpenAI-compatible server as the text upstream).
	VisionModel string        `yaml:"vision_model"`
	Session     SessionConfig `yaml:"session"`
	AuthToken   string        `yaml:"auth_token"`
	// PrimersDir holds captured primers; empty means <parent>/state/primers.
	PrimersDir string `yaml:"primers_dir"`
	// ReadingsDir holds books the reader imported to read as they are;
	// empty means <parent>/state/readings.
	ReadingsDir string `yaml:"readings_dir"`
	// RequestsDir holds change requests from the app for the agent;
	// empty means <parent>/state/requests.
	RequestsDir string `yaml:"requests_dir"`
	// BuildsDir holds app builds the MacBook made, one directory per
	// build named by its token, `latest` a link to the current one;
	// empty means <parent>/builds.
	BuildsDir string `yaml:"builds_dir"`
	// Grade all free-text items of a check in one model call. Off by default:
	// the per-item path is the one verified end to end, and a check is the
	// moment a learner is most exposed to a regression.
	BatchGrading bool `yaml:"batch_grading"`

	Provider       string         `yaml:"provider"`
	AnthropicModel string         `yaml:"anthropic_model"`
	ClaudeCLIModel string         `yaml:"claude_cli_model"`
	Upstreams      []llm.Upstream `yaml:"upstreams"`
	LLM            llm.Config     `yaml:"llm"`
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Subject pairs a corpus with the learner state for it.
type Subject struct {
	ID    string
	Title string
	// Kind is "" for a book and KindPrimer for a captured primer, which has
	// no check and grows from margin notes.
	Kind     string
	Primer   *primer.Meta
	Corpus   *corpus.Corpus
	Learner  *state.Learner
	StateDir string
	Pages    *pages.Renderer

	// Background authoring state: one build at a time per subject, with the
	// failure kept for the pages meta to surface.
	buildMu    sync.Mutex
	building   bool
	buildErr   string
	buildStage string
	buildStart time.Time
}

func (sub *Subject) beginBuild() bool {
	sub.buildMu.Lock()
	defer sub.buildMu.Unlock()
	if sub.building {
		return false
	}
	sub.building = true
	sub.buildErr = ""
	sub.buildStage = "planning"
	sub.buildStart = time.Now()
	return true
}

// setStage names where the running build is: planning, writing, pages.
func (sub *Subject) setStage(stage string) {
	sub.buildMu.Lock()
	sub.buildStage = stage
	sub.buildMu.Unlock()
}

// buildProgress is the running build's stage and age in seconds, for the
// status the reader polls while waiting.
func (sub *Subject) buildProgress() (string, int) {
	sub.buildMu.Lock()
	defer sub.buildMu.Unlock()
	if !sub.building {
		return "", 0
	}
	return sub.buildStage, int(time.Since(sub.buildStart).Seconds())
}

func (sub *Subject) endBuild(err string) {
	sub.buildMu.Lock()
	sub.building = false
	sub.buildErr = err
	sub.buildMu.Unlock()
}

func (sub *Subject) buildStatus() (bool, string) {
	sub.buildMu.Lock()
	defer sub.buildMu.Unlock()
	return sub.building, sub.buildErr
}

type Server struct {
	cfg  *Config
	root string // directory config.yaml lives in
	// buildsDir is where app builds are offered from (Config.BuildsDir resolved).
	buildsDir string
	// requests is the change-request queue the app fills and the agent drains.
	requests *devreq.Store
	// transcribe overrides the ink vision transcriber; tests inject one.
	transcribe func(hint string, png []byte) (string, error)
	chain      llm.Chain
	token      string
	// authorizedKeys is sshd's file on the sprite; empty means device keys
	// cannot be enrolled here.
	authorizedKeys string
	// hub is the sprite agent's line to the app.
	hub *agent.Hub
	// primers are the captured documents by id, including those still
	// authoring or failed, which are not subjects.
	primersMu sync.Mutex
	primers   map[string]*primer.Meta
	rng       *rand.Rand
	rngMu     sync.Mutex

	mu       sync.RWMutex
	subjects map[string]*Subject

	// One check of the blogs at a time: every device asks for one when
	// the reader opens it, and they would otherwise take the same post
	// twice.
	feedMu sync.Mutex
	// One writer of the alerts file at a time.
	alertMu sync.Mutex

	// The book the reader last had open, so the client can reopen on it.
	// Persisted beside the subject state dirs: the client has no writable
	// storage and the server suspends between sessions.
	activeMu   sync.Mutex
	active     string
	activePath string
	shelves    *shelves
	// When each subject was last opened, for ordering the shelf by recency.
	opened *opened

	jobsMu sync.Mutex
	jobs   map[string]*Job
	// startGenerate begins a book's generation for a job already listed;
	// tests replace it with a recorder.
	startGenerate func(slug, title, brief string, named []string, planOnly bool)

	// renders tracks eager background page renders so tests (and shutdown)
	// can wait for them instead of racing temp-dir cleanup.
	renders sync.WaitGroup
}

func New(cfg *Config, root string) (*Server, error) {
	// CHIRON_AUTH_TOKEN is preferred over the config key so the secret lives in
	// the service environment rather than in a file on disk.
	token := strings.TrimSpace(os.Getenv("CHIRON_AUTH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(cfg.AuthToken)
	}
	s := &Server{
		cfg:            cfg,
		root:           root,
		buildsDir:      buildsRoot(cfg, root),
		requests:       devreq.Open(requestsRoot(cfg, root)),
		token:          token,
		authorizedKeys: strings.TrimSpace(os.Getenv("CHIRON_AUTHORIZED_KEYS")),
		hub:            agent.NewHub(),
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
		subjects:       map[string]*Subject{},
		jobs:           map[string]*Job{},

		primers: map[string]*primer.Meta{},
		chain: llm.New(llm.FactoryConfig{
			Provider: cfg.Provider, AnthropicModel: cfg.AnthropicModel,
			ClaudeCLIModel: cfg.ClaudeCLIModel, Upstreams: cfg.Upstreams, LLM: cfg.LLM,
			CLIConfigDir: filepath.Join(root, "claude"),
		}),
	}
	s.startGenerate = func(slug, title, brief string, named []string, planOnly bool) {
		go s.generate(slug, title, brief, named, planOnly)
	}
	for _, spec := range cfg.Subjects {
		if err := s.register(spec.ID, spec.Title,
			resolve(root, spec.CorpusDir), resolve(root, spec.StateDir)); err != nil {
			return nil, fmt.Errorf("subject %s: %w", spec.ID, err)
		}
	}
	s.Discover()
	s.loadPrimers()
	s.loadReadings()
	if len(cfg.Subjects) > 0 {
		dir := filepath.Dir(resolve(root, cfg.Subjects[0].StateDir))
		s.activePath = filepath.Join(dir, "active-subject")
		if raw, err := os.ReadFile(s.activePath); err == nil {
			if id := strings.TrimSpace(string(raw)); id != "" {
				if _, ok := s.subject(id); ok {
					s.active = id
				}
			}
		}
	}
	s.shelves = loadShelves(shelvesPath(s.activePath))
	s.opened = loadOpened(s.activePath)
	return s, nil
}

// markActive records id as the open book. Best-effort persistence: a failed
// write only costs the reopen-on-last-book nicety after a restart.
func (s *Server) markActive(id string) {
	s.opened.mark(id)
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active == id {
		return
	}
	s.active = id
	if s.activePath != "" {
		if err := os.WriteFile(s.activePath, []byte(id+"\n"), 0o644); err != nil {
			log.Printf("active-subject: %v", err)
		}
	}
}

func (s *Server) activeSubject() string {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.active
}

// resolve interprets a config path relative to the config file, leaving an
// absolute path alone. filepath.Join would otherwise glue the two together,
// which pathlib in the Python implementation does not do.
func resolve(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func (s *Server) register(id, title, corpusDir, stateDir string) error {
	c, err := corpus.Load(corpusDir)
	if err != nil {
		return err
	}
	l, err := state.Open(stateDir, c)
	if err != nil {
		return err
	}
	fonts := ""
	if s.cfg.FontsDir != "" {
		fonts = resolve(s.root, s.cfg.FontsDir)
	}
	s.mu.Lock()
	s.subjects[id] = &Subject{ID: id, Title: title, Corpus: c, Learner: l,
		StateDir: stateDir,
		Pages: &pages.Renderer{
			Off:      renderOff(),
			KatexDir: resolve(s.root, s.cfg.KatexDir),
			FontsDir: fonts,
			CacheDir: filepath.Join(stateDir, "pages"),
		}}
	s.mu.Unlock()
	return nil
}

// Discover registers every corpus-<slug>/ sibling directory not already
// configured.
//
// A subject the learner asked for through Teach-me has to survive a restart,
// and the alternative - rewriting config.yaml - would strip the comments that
// explain the flight settings. The corpus directory on disk is the record.
//
// Only fully-authored corpora register. A syllabus exists from the moment
// planning finishes, so a half-generated subject would otherwise appear in the
// library as a book whose chapters are missing.
func (s *Server) Discover() []string {
	parent := filepath.Dir(s.root)
	entries, err := filepath.Glob(filepath.Join(parent, "corpus-*"))
	if err != nil {
		return nil
	}
	sort.Strings(entries)

	s.mu.RLock()
	configured := map[string]bool{}
	for _, sub := range s.subjects {
		if abs, err := filepath.Abs(sub.Corpus.Dir); err == nil {
			configured[abs] = true
		}
	}
	s.mu.RUnlock()

	var found []string
	for _, dir := range entries {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "syllabus.yaml")); err != nil {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil || configured[abs] {
			continue
		}
		c, err := corpus.Load(dir)
		if err != nil {
			continue // a malformed corpus must not stop boot
		}
		if len(c.Units) < len(c.Syllabus.Units) {
			continue
		}
		id := strings.TrimPrefix(filepath.Base(dir), "corpus-")
		title := c.Syllabus.Title
		if title == "" {
			title = strings.Title(strings.ReplaceAll(id, "-", " ")) //nolint:staticcheck
		}
		if err := s.register(id, title, dir, filepath.Join(parent, "state", id)); err != nil {
			continue
		}
		found = append(found, id)
	}
	return found
}

func (s *Server) subject(id string) (*Subject, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subjects[id]
	return sub, ok
}

func (s *Server) allSubjects() []*Subject {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Subject, 0, len(s.subjects))
	for _, sub := range s.subjects {
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ---------- transport ----------

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.handlePing)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /subjects", s.handleSubjects)
	mux.HandleFunc("PUT /subjects/{subject}/shelf", s.handleSubjectShelf)
	mux.HandleFunc("GET /annotations/{subject}/{unit}", s.handleAnnotationsGet)
	mux.HandleFunc("PUT /annotations/{subject}/{unit}", s.handleAnnotationsPut)
	mux.HandleFunc("POST /annotations/{subject}/{unit}/reconcile", s.handleAnnotationsReconcile)
	mux.HandleFunc("POST /documents", s.handleDocumentUpload)
	mux.HandleFunc("POST /readings", s.handleReadingImport)
	mux.HandleFunc("POST /readings/page", s.handleReadingPage)
	mux.HandleFunc("POST /feeds", s.handleFeedFollow)
	mux.HandleFunc("GET /feeds", s.handleFeedList)
	mux.HandleFunc("POST /feeds/refresh", s.handleFeedRefresh)
	mux.HandleFunc("POST /readings/{id}/read", s.handleReadingRead)
	mux.HandleFunc("DELETE /readings/{id}", s.handleReadingDelete)
	mux.HandleFunc("GET /readings/{id}/assets", s.handleReadingAssetList)
	mux.HandleFunc("GET /readings/{id}/assets/{name}", s.handleReadingAsset)
	mux.HandleFunc("POST /dev/requests", s.handleRequestCreate)
	mux.HandleFunc("GET /dev/requests", s.handleRequestList)
	mux.HandleFunc("GET /dev/requests/{id}", s.handleRequestGet)
	mux.HandleFunc("POST /dev/requests/{id}/answer", s.handleRequestAnswer)
	mux.HandleFunc("POST /alerts", s.handleAlertPost)
	mux.HandleFunc("GET /alerts", s.handleAlertList)
	mux.HandleFunc("GET /builds/latest", s.handleBuildLatest)
	mux.HandleFunc("GET /builds/{token}/{file}", s.handleBuildFile)
	mux.HandleFunc("GET /documents/{doc}", s.handleDocumentGet)
	mux.HandleFunc("GET /documents/{doc}/file", s.handleDocumentFile)
	mux.HandleFunc("PUT /documents/{doc}/position", s.handleDocumentPosition)
	mux.HandleFunc("DELETE /documents/{doc}", s.handleDocumentDelete)
	mux.HandleFunc("GET /documents/{doc}/ink", s.handleDocumentInkAll)
	mux.HandleFunc("PUT /documents/{doc}/ink/{page}", s.handleDocumentInkPut)
	mux.HandleFunc("GET /shelves", s.handleShelves)
	mux.HandleFunc("POST /shelves", s.handleShelfCreate)
	mux.HandleFunc("PUT /shelves/{shelf}", s.handleShelfRename)
	mux.HandleFunc("DELETE /shelves/{shelf}", s.handleShelfDelete)
	mux.HandleFunc("GET /state", s.handleState)
	mux.HandleFunc("GET /review-schedule", s.handleReviewSchedule)
	mux.HandleFunc("GET /chapter/{subject}", s.handleChapter)
	mux.HandleFunc("GET /pages/{subject}", s.handlePagesMeta)
	mux.HandleFunc("GET /pages/{subject}/{page}", s.handlePage)
	mux.HandleFunc("GET /pages/{subject}/results", s.handleResultsMeta)
	mux.HandleFunc("GET /pages/{subject}/results/{page}", s.handleResultsPage)
	mux.HandleFunc("GET /pages/{subject}/contents", s.handleContentsMeta)
	mux.HandleFunc("GET /pages/{subject}/contents/{page}", s.handleContentsPage)
	mux.HandleFunc("POST /ink/{subject}", s.handleInk)
	mux.HandleFunc("POST /ask/{subject}", s.handleAsk)
	mux.HandleFunc("POST /primer/capture", s.handlePrimerCapture)
	mux.HandleFunc("GET /primer/{subject}/plan", s.handlePrimerPlan)
	mux.HandleFunc("POST /primer/{subject}/plan", s.handlePrimerPlanTurn)
	mux.HandleFunc("POST /primer/{subject}/build", s.handlePrimerBuild)
	mux.HandleFunc("POST /primer/{subject}/discard", s.handlePrimerDiscard)
	mux.HandleFunc("DELETE /primer/{subject}", s.handlePrimerDelete)
	mux.HandleFunc("POST /primer/{subject}/extend", s.handlePrimerExtend)
	mux.HandleFunc("POST /agent/pubkey", s.handleEnrolKey)
	mux.HandleFunc("GET /agent/keys", s.handleListKeys)
	mux.HandleFunc("POST /agent/keys/revoke", s.handleRevokeKey)
	mux.HandleFunc("GET /agent/app", s.handleAgentApp)
	mux.HandleFunc("POST /agent/cmd", s.handleAgentCmd)
	mux.HandleFunc("GET /agent/status", s.handleAgentStatus)
	mux.HandleFunc("POST /drive/cmd", s.handleDriveCmd)
	mux.HandleFunc("GET /drive/next", s.handleDriveNext)
	mux.HandleFunc("POST /drive/ack", s.handleDriveAck)
	mux.HandleFunc("GET /drive/ack", s.handleDriveAck)
	mux.HandleFunc("POST /exchange", s.handleExchange)
	mux.HandleFunc("POST /reset", s.handleReset)
	mux.HandleFunc("POST /teach/turn", s.handleTeachTurn)
	mux.HandleFunc("POST /teach/create", s.handleTeachCreate)
	mux.HandleFunc("GET /teach/jobs", s.handleTeachJobs)
	return s.requireToken(mux)
}

// requireToken guards everything except /ping.
//
// An empty token leaves the server open, which is correct on the flight LAN
// where the only client is the iPad on a Mac-hosted network. It is not optional
// on a public URL: without it, anyone who finds the endpoint spends the tutor's
// model budget.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.URL.Path != "/ping" && !buildFileOpen(r.URL.Path) && !auth.Authorized(r, s.token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// renderOff reads CHIRON_RENDER: "0", "off" or "false" switch page images
// off for the whole server.
func renderOff() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CHIRON_RENDER"))) {
	case "0", "off", "false":
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, format string, a ...any) {
	// The response is the error's only copy unless we log it too: a client
	// that gave up (or an edge proxy that cut the connection) makes the
	// failure invisible.
	detail := fmt.Sprintf(format, a...)
	log.Printf("http %d: %s", status, detail)
	writeJSON(w, status, map[string]string{"detail": detail})
}

// handlePing is an unauthenticated liveness probe that deliberately reveals
// nothing.
func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	subjects := map[string][]string{}
	for _, sub := range s.allSubjects() {
		var units []string
		for _, uid := range sub.Corpus.UnitOrder() {
			if _, ok := sub.Corpus.Units[uid]; ok {
				units = append(units, uid)
			}
		}
		subjects[sub.ID] = units
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "llm": s.chain.Status(), "subjects": subjects,
	})
}

func (s *Server) handleSubjects(w http.ResponseWriter, _ *http.Request) {
	type row struct {
		ID           string  `json:"id"`
		Title        string  `json:"title"`
		Kind         string  `json:"kind"`
		Unread       int     `json:"unread,omitempty"`
		UnitsTotal   int     `json:"units_total"`
		UnitsCleared int     `json:"units_cleared"`
		CurrentUnit  *string `json:"current_unit"`
		Debt         int     `json:"debt"`
		// Primers only: how the capture is doing and where it came from.
		Status     string         `json:"status,omitempty"`
		Error      string         `json:"error,omitempty"`
		Source     *primer.Source `json:"source,omitempty"`
		CapturedAt string         `json:"captured_at,omitempty"`
		// Drafts only: what the capture is to become, the book it is
		// becoming, and how far that is.
		Scale    string `json:"scale,omitempty"`
		Book     string `json:"book,omitempty"`
		Progress string `json:"progress,omitempty"`
		// Documents only: pages in the PDF and the page last read.
		Pages int `json:"pages,omitempty"`
		Page  int `json:"page,omitempty"`
		// The shelf it is on, if any.
		Shelf string `json:"shelf,omitempty"`
		// When it last changed or was last opened, for ordering by recency.
		UpdatedAt string `json:"updated_at,omitempty"`
	}
	out := []row{}
	for _, sub := range s.allSubjects() {
		r := row{
			ID: sub.ID, Title: sub.Title, Kind: "book",
			UnitsTotal:   len(sub.Corpus.UnitOrder()),
			UnitsCleared: len(sub.Learner.ClearedUnits()),
			CurrentUnit:  sub.Learner.Snapshot().CurrentUnit,
			Debt:         len(sub.Learner.OpenDebt()),
			Shelf:        s.shelves.shelfOf(sub.ID),
			UpdatedAt:    s.subjectUpdated(sub),
		}
		if sub.Kind == KindReading || sub.Kind == KindFeed {
			r.Kind = sub.Kind
		}
		if sub.Kind == KindFeed {
			r.Unread = s.unreadFor(s.readingsRoot(), sub.ID)
		}
		if sub.Kind == KindPrimer && sub.Primer != nil {
			r.Kind, r.Status = KindPrimer, sub.Primer.Status
			src := sub.Primer.Source
			src.Text = clipText(src.Text, 200)
			r.Source = &src
			r.CapturedAt = sub.Primer.CapturedAt.Format(time.RFC3339)
		}
		out = append(out, r)
	}
	for _, m := range s.pendingPrimers() {
		src := m.Source
		src.Text = clipText(src.Text, 200)
		out = append(out, row{
			ID: m.ID, Title: m.Title, Kind: KindPrimer, Status: m.Status, Error: m.Error,
			Source: &src, CapturedAt: m.CapturedAt.Format(time.RFC3339),
			Scale: m.Scale, Book: m.Book, Progress: s.progressOf(m),
			Shelf: s.shelves.shelfOf(m.ID), UpdatedAt: s.updatedAt(m.ID, m.CapturedAt),
		})
	}
	for _, d := range s.documents() {
		out = append(out, row{ID: d.ID, Title: d.Title, Kind: "pdf", Pages: d.Pages, Page: d.Page, Shelf: s.shelves.shelfOf(d.ID),
			UpdatedAt: s.updatedAt(d.ID, stamp(d.ImportedAt), mtime(s.documentPath(d.ID)))})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subjects": out,
		"shelves":  s.shelves.view(s.subjectExists),
		"active":   s.activeSubject(),
	})
}

func (s *Server) subjectParam(r *http.Request) string {
	if id := r.URL.Query().Get("subject"); id != "" {
		return id
	}
	return "ai"
}
