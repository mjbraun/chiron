package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/primer"
	"github.com/mjbraun/chiron/server/render"
	"github.com/mjbraun/chiron/server/roles"
	"github.com/mjbraun/chiron/server/sources"
)

// Primers: the reader captures something anywhere on the iPad, asks a
// question, and says how much they want back. A summary or a detail
// is answered in the card. A primer or a smart book is a draft first: a
// short planning conversation the reader can leave and come back to, then
// a build. A primer is a one-unit subject with no check that margin notes
// extend; a book goes through the same generation as "Teach me".

const KindPrimer = "primer"

var primersMu sync.Mutex

func (s *Server) primersRoot() string {
	if s.cfg.PrimersDir != "" {
		return resolve(s.root, s.cfg.PrimersDir)
	}
	return filepath.Join(filepath.Dir(s.root), "state", "primers")
}

// loadPrimers registers the ready ones at boot. One caught mid-authoring
// by a restart is marked failed: the reader sees why the card is grey.
func (s *Server) loadPrimers() {
	metas, err := primer.LoadAll(s.primersRoot())
	if err != nil {
		log.Printf("primers: %v", err)
		return
	}
	for _, m := range metas {
		if m.Status == primer.StatusAuthoring {
			m.Status, m.Error = primer.StatusFailed, "authoring was interrupted by a restart"
			primer.Save(s.primersRoot(), m)
		}
		if m.Status == primer.StatusBuilding {
			m.Status, m.Error = primer.StatusFailed, "the book's generation was interrupted by a restart"
			primer.Save(s.primersRoot(), m)
		}
		s.primersMu.Lock()
		s.primers[m.ID] = m
		s.primersMu.Unlock()
		if m.Status == primer.StatusReady {
			if err := s.registerPrimer(m); err != nil {
				log.Printf("primer %s: %v", m.ID, err)
			}
		}
	}
}

func (s *Server) registerPrimer(m *primer.Meta) error {
	root := s.primersRoot()
	if err := s.register(m.ID, m.Title, primer.CorpusDir(root, m.ID), primer.StateDir(root, m.ID)); err != nil {
		return err
	}
	s.mu.Lock()
	if sub := s.subjects[m.ID]; sub != nil {
		sub.Kind = KindPrimer
		sub.Primer = m
	}
	s.mu.Unlock()
	return nil
}

// captureLink is the page a capture is of, when the capture is a link and
// nothing else: the text is one address, or there is no text at all and the
// capture says where it came from. A passage quoted from a page is not this -
// the passage is the capture, and the URL is only its provenance.
func captureLink(text, sourceURL string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return webAddress(sourceURL)
	}
	if strings.ContainsAny(text, " \t\n") {
		return ""
	}
	return webAddress(text)
}

// webAddress is the page a single token names, if it names one. A link is
// as often pasted without its scheme as with it - that is how a newsletter
// prints one - so a bare host and path counts, and is read over https.
func webAddress(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if u.Scheme == "http" || u.Scheme == "https" {
			return s
		}
		// Some other scheme entirely: a file, a chiron:// link, a mailto.
		if u.Scheme != "" {
			return ""
		}
	}
	if !schemeless.MatchString(s) {
		return ""
	}
	return "https://" + s
}

// A host with a real top-level domain, then anything: uber.com/blog/x, but
// not notes.md, not 1.25, and not a sentence.
var schemeless = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*\.(com|org|net|edu|gov|io|dev|ai|app|co|sh|me|blog|news|xyz|info|to|uk|us|ca|de|fr|jp|au|eu)(/[^ ]*)?$`)

type captureRequest struct {
	Text      string `json:"text"`
	ImagePNG  string `json:"image_png_b64"`
	SourceURL string `json:"source_url"`
	SourceApp string `json:"source_app"`
	Prompt    string `json:"prompt"`
	Title     string `json:"title"`
	// Scale is summary, detail, primer or book; a primer without one.
	Scale string `json:"scale"`
}

func (s *Server) handlePrimerCapture(w http.ResponseWriter, r *http.Request) {
	var req captureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request: %v", err)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Text = strings.TrimSpace(req.Text)
	if req.Prompt == "" {
		writeError(w, http.StatusUnprocessableEntity, "prompt is required: what do you want to know about this?")
		return
	}
	if req.Scale == "" {
		req.Scale = roles.ScalePrimer
	}
	switch req.Scale {
	case roles.ScaleSummary, roles.ScaleDetail, roles.ScalePrimer, roles.ScaleBook:
	default:
		writeError(w, http.StatusUnprocessableEntity, "scale must be summary, detail, primer or book")
		return
	}
	var png []byte
	if req.ImagePNG != "" {
		var err error
		if png, err = base64.StdEncoding.DecodeString(req.ImagePNG); err != nil {
			writeError(w, http.StatusBadRequest, "image_png_b64 is not base64: %v", err)
			return
		}
	}
	// A capture that is only a link is a link to something worth reading.
	// Fetch it and capture the article: a tutor handed a bare URL cannot
	// read it, and writes around the gap instead of from the piece.
	if link := captureLink(req.Text, req.SourceURL); link != "" {
		if req.SourceURL == "" {
			req.SourceURL = link
		}
		page, err := sources.NewClient("").Page(r.Context(), link)
		switch {
		case err != nil:
			// Still a capture: the tutor is told the address and that the
			// page would not come, which is better than silence. Unless the
			// text was never meant as a link - something that merely looked
			// like a host - in which case it is left as it was written.
			log.Printf("capture %s: %v", link, err)
			if strings.HasPrefix(strings.TrimSpace(req.Text), "http") || strings.TrimSpace(req.Text) == "" {
				req.Text = strings.TrimSpace(req.Text + "\n\n" + link +
					"\n\n(This page could not be fetched, so only its address was captured.)")
			}
		default:
			if req.Title == "" {
				req.Title = page.Title
			}
			req.Text = page.Markdown
		}
	}
	if req.Text == "" && len(png) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "nothing captured: send text or image_png_b64")
		return
	}
	// An image becomes words first, whatever the scale; the same vision
	// path the ink check-in uses.
	if req.Text == "" {
		text, err := s.transcribeCapture(png)
		if err != nil {
			writeError(w, http.StatusBadGateway, "transcribe the image: %v", err)
			return
		}
		req.Text = text
	}
	cap := roles.Capture{Text: req.Text, URL: req.SourceURL, App: req.SourceApp, Prompt: req.Prompt}

	if req.Scale == roles.ScaleSummary || req.Scale == roles.ScaleDetail {
		rec := llm.Record(s.chain)
		md, err := roles.AnswerCapture(rec, cap, req.Scale)
		if err != nil && driveEnabled() && !s.chain.Status().Connected {
			md, err = fmt.Sprintf("[stub %s] You asked: %s\n\nAbout: %s", req.Scale, cap.Prompt, clipText(cap.Text, 300)), nil
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "no answer: %v", err)
			return
		}
		out := map[string]any{"scale": req.Scale, "answer_md": md}
		if calls := rec.Calls(); len(calls) > 0 {
			// The card is the only place this is read, so the whole
			// record rides with it.
			out["generated"] = calls[0]
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	seed := req.Title
	if seed == "" {
		seed = req.Prompt
	}
	id := s.uniquePrimerID(seed)
	m := &primer.Meta{
		ID: id, Title: strings.TrimSpace(req.Title), Prompt: req.Prompt,
		Source:     primer.Source{Text: req.Text, URL: req.SourceURL, App: req.SourceApp, HasImage: len(png) > 0},
		Status:     primer.StatusPlanning,
		Scale:      req.Scale,
		CapturedAt: time.Now().UTC(),
	}
	m.Named = m.Title != ""
	if m.Title == "" {
		m.Title = workingTitle(req.Prompt)
	}
	s.primersMu.Lock()
	s.primers[id] = m
	s.primersMu.Unlock()
	// The tutor opens the conversation.
	turn, err := s.planTurn(m, "")
	if err != nil {
		s.primersMu.Lock()
		delete(s.primers, id)
		s.primersMu.Unlock()
		writeError(w, http.StatusBadGateway, "no plan: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject": id, "status": m.Status, "title": m.Title, "scale": m.Scale,
		"reply_md": turn.ReplyMD, "done": turn.Done, "brief": turn.Brief,
	})
}

func (s *Server) transcribeCapture(png []byte) (string, error) {
	tr := s.transcribe
	if tr == nil {
		tr = s.transcriberFor()
	}
	return tr("captured image", png)
}

// draft finds a capture still being planned or built, by id.
func (s *Server) draft(id string) (*primer.Meta, bool) {
	s.primersMu.Lock()
	defer s.primersMu.Unlock()
	m, ok := s.primers[id]
	if !ok || m.Scale == "" || !m.Draft() {
		return nil, false
	}
	return m, true
}

// planTurn adds the reader's words (if any) and the tutor's reply to the
// draft's conversation, and keeps it. The plan itself is one model call.
func (s *Server) planTurn(m *primer.Meta, learner string) (roles.Elicitation, error) {
	primersMu.Lock()
	defer primersMu.Unlock()
	if learner != "" {
		m.Plan = append(m.Plan, primer.Turn{Role: "learner", Text: learner})
	}
	msgs := make([]roles.Message, 0, len(m.Plan))
	for _, t := range m.Plan {
		msgs = append(msgs, roles.Message{Role: t.Role, Text: t.Text})
	}
	cap := roles.Capture{Text: m.Source.Text, URL: m.Source.URL, App: m.Source.App, Prompt: m.Prompt}
	turn, err := roles.PlanCapture(s.chain, cap, m.Scale, msgs)
	if err != nil && driveEnabled() && !s.chain.Status().Connected {
		turn, err = stubPlan(m), nil
	}
	if err != nil {
		if learner != "" {
			m.Plan = m.Plan[:len(m.Plan)-1]
		}
		return roles.Elicitation{}, err
	}
	m.Plan = append(m.Plan, primer.Turn{Role: "tutor", Text: turn.ReplyMD})
	if turn.Done {
		m.Done, m.Brief = true, strings.TrimSpace(turn.Brief)
		if t := strings.TrimSpace(turn.Title); t != "" && !m.Named {
			m.Title = t
		}
	}
	if err := primer.Save(s.primersRoot(), m); err != nil {
		return roles.Elicitation{}, err
	}
	turn.Title = m.Title
	return turn, nil
}

// stubPlan stands in for the planner on a dev server: one question, then
// a brief made of what the reader said.
func stubPlan(m *primer.Meta) roles.Elicitation {
	learner := 0
	for _, t := range m.Plan {
		if t.Role == "learner" {
			learner++
		}
	}
	if learner == 0 {
		return roles.Elicitation{ReplyMD: "[stub] What should it focus on?"}
	}
	return roles.Elicitation{ReplyMD: "[stub] Enough to build from.", Done: true,
		Brief: m.Prompt + " " + m.Plan[len(m.Plan)-1].Text, Title: workingTitle(m.Prompt), Slug: slugify(m.Prompt)}
}

// The plan as the card shows it, for a draft reopened from the shelf.
func (s *Server) handlePrimerPlan(w http.ResponseWriter, r *http.Request) {
	m, ok := s.draft(r.PathValue("subject"))
	if !ok {
		writeError(w, http.StatusNotFound, "no draft %q", r.PathValue("subject"))
		return
	}
	primersMu.Lock()
	defer primersMu.Unlock()
	writeJSON(w, http.StatusOK, planView(m))
}

func planView(m *primer.Meta) map[string]any {
	plan := m.Plan
	if plan == nil {
		plan = []primer.Turn{}
	}
	src := m.Source
	src.Text = clipText(src.Text, 600)
	return map[string]any{
		"id": m.ID, "title": m.Title, "scale": m.Scale, "status": m.Status, "error": m.Error,
		"prompt": m.Prompt, "source": src, "brief": m.Brief, "done": m.Done, "plan": plan, "book": m.Book,
	}
}

type planTurnRequest struct {
	Text string `json:"text"`
}

// The reader's next line in the planning conversation.
func (s *Server) handlePrimerPlanTurn(w http.ResponseWriter, r *http.Request) {
	m, ok := s.draft(r.PathValue("subject"))
	if !ok {
		writeError(w, http.StatusNotFound, "no draft %q", r.PathValue("subject"))
		return
	}
	if m.Status == primer.StatusBuilding {
		writeError(w, http.StatusConflict, "%q is being built", m.ID)
		return
	}
	var req planTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request: %v", err)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "text is required")
		return
	}
	turn, err := s.planTurn(m, req.Text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no reply: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject": m.ID, "status": m.Status, "title": m.Title, "scale": m.Scale,
		"reply_md": turn.ReplyMD, "done": turn.Done, "brief": turn.Brief,
	})
}

// briefFor is what the writer gets: the brief the plan settled on, or,
// when the reader builds before that, the question and what they said.
func briefFor(m *primer.Meta) string {
	if m.Brief != "" {
		return m.Brief
	}
	var said []string
	for _, t := range m.Plan {
		if t.Role == "learner" {
			said = append(said, t.Text)
		}
	}
	if len(said) == 0 {
		return ""
	}
	return "The reader also said: " + strings.Join(said, " ")
}

// Build: a primer is written now; a book's generation starts.
func (s *Server) handlePrimerBuild(w http.ResponseWriter, r *http.Request) {
	m, ok := s.draft(r.PathValue("subject"))
	if !ok {
		writeError(w, http.StatusConflict, "%q is not a draft to build", r.PathValue("subject"))
		return
	}
	if m.Status == primer.StatusBuilding {
		writeError(w, http.StatusConflict, "%q is already being built", m.ID)
		return
	}
	// A build may bring its brief, in place of the planning turns: the
	// reader (or an agent acting for them) already knows what they want.
	var req struct {
		Brief string `json:"brief"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request: %v", err)
			return
		}
	}
	primersMu.Lock()
	defer primersMu.Unlock()
	root := s.primersRoot()
	if b := strings.TrimSpace(req.Brief); b != "" {
		m.Brief, m.Done = b, true
	}
	brief := briefFor(m)
	switch m.Scale {
	case roles.ScaleBook:
		slug := s.uniqueSlug(slugify(m.Title))
		s.jobsMu.Lock()
		s.jobs[slug] = &Job{Slug: slug, Title: m.Title, Stage: "queued"}
		s.jobsMu.Unlock()
		full := brief
		if full == "" {
			full = m.Prompt
		}
		full += "\n\nThe book grows from this material the reader captured:\n" + clipText(m.Source.Text, 8000)
		m.Status, m.Error, m.Book = primer.StatusBuilding, "", slug
		if err := primer.Save(root, m); err != nil {
			writeError(w, http.StatusInternalServerError, "save draft: %v", err)
			return
		}
		s.startGenerate(slug, m.Title, full, nil, false)
		writeJSON(w, http.StatusOK, map[string]any{"subject": m.ID, "status": m.Status, "book": slug})
	default:
		m.Status, m.Error = primer.StatusAuthoring, ""
		if err := primer.Save(root, m); err != nil {
			writeError(w, http.StatusInternalServerError, "save draft: %v", err)
			return
		}
		s.renders.Add(1)
		go func() {
			defer s.renders.Done()
			s.authorPrimer(m, brief)
		}()
		writeJSON(w, http.StatusOK, map[string]any{"subject": m.ID, "status": m.Status, "title": m.Title})
	}
}

// uniqueSlug: the slug, or the slug with a counter when a subject or a
// job has it.
func (s *Server) uniqueSlug(base string) string {
	if !slugOK.MatchString(base) {
		base = "book"
	}
	slug := base
	for n := 2; ; n++ {
		_, taken := s.subject(slug)
		s.jobsMu.Lock()
		_, running := s.jobs[slug]
		s.jobsMu.Unlock()
		if !taken && !running {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}
}

// A draft the reader does not want after all.
// handlePrimerDelete takes a primer off the shelf for good: the written
// one as well as the draft. Discard only ever took drafts, which left a
// primer that missed the point with nowhere to go.
func (s *Server) handlePrimerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("subject")
	s.primersMu.Lock()
	m := s.primers[id]
	s.primersMu.Unlock()
	if m == nil {
		writeError(w, http.StatusNotFound, "no primer %q", id)
		return
	}
	if m.Status == primer.StatusBuilding {
		writeError(w, http.StatusConflict, "%q is being built; it can go when that is done", id)
		return
	}
	s.mu.Lock()
	delete(s.subjects, id)
	s.mu.Unlock()
	s.dropDraft(m)
	writeJSON(w, http.StatusOK, map[string]any{"subject": id, "deleted": true})
}

func (s *Server) handlePrimerDiscard(w http.ResponseWriter, r *http.Request) {
	m, ok := s.draft(r.PathValue("subject"))
	if !ok {
		writeError(w, http.StatusConflict, "%q is not a draft to discard", r.PathValue("subject"))
		return
	}
	if m.Status == primer.StatusBuilding {
		writeError(w, http.StatusConflict, "%q is being built; it can be discarded when that is done", m.ID)
		return
	}
	s.dropDraft(m)
	writeJSON(w, http.StatusOK, map[string]any{"subject": m.ID, "discarded": true})
}

func (s *Server) dropDraft(m *primer.Meta) {
	primersMu.Lock()
	defer primersMu.Unlock()
	if err := primer.Delete(s.primersRoot(), m.ID); err != nil {
		log.Printf("discard %s: %v", m.ID, err)
	}
	s.primersMu.Lock()
	delete(s.primers, m.ID)
	s.primersMu.Unlock()
}

// sweepDrafts follows each book draft's generation: a failed job fails
// the draft with the job's reason; a book that has arrived on the shelf
// takes the draft's place.
func (s *Server) sweepDrafts() {
	s.primersMu.Lock()
	var building []*primer.Meta
	for _, m := range s.primers {
		if m.Status == primer.StatusBuilding && m.Book != "" {
			building = append(building, m)
		}
	}
	s.primersMu.Unlock()
	for _, m := range building {
		if _, arrived := s.subject(m.Book); arrived {
			s.dropDraft(m)
			continue
		}
		s.jobsMu.Lock()
		job, ok := s.jobs[m.Book]
		var failed bool
		var reason string
		if ok && job.Done && job.Stage == "failed" {
			failed, reason = true, job.Error
		}
		if !ok {
			failed, reason = true, "the book's generation is gone"
		}
		s.jobsMu.Unlock()
		if failed {
			primersMu.Lock()
			m.Status, m.Error = primer.StatusFailed, reason
			primer.Save(s.primersRoot(), m)
			primersMu.Unlock()
		}
	}
}

// progressOf says how far a book draft's generation is.
func (s *Server) progressOf(m *primer.Meta) string {
	if m.Status != primer.StatusBuilding {
		return ""
	}
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job, ok := s.jobs[m.Book]
	if !ok {
		return ""
	}
	switch job.Stage {
	case "planning":
		return "designing the syllabus"
	case "authoring":
		if job.UnitsTotal > 0 {
			return fmt.Sprintf("writing chapter %d of %d", job.UnitsDone+1, job.UnitsTotal)
		}
		return "writing chapters"
	}
	return job.Stage
}

// uniquePrimerID: "primer-" plus a slug of the seed, with a counter when
// the reader captures the same thing twice.
func (s *Server) uniquePrimerID(seed string) string {
	// Whole words up to the slug's length, so an id reads as a phrase.
	slug := ""
	for _, w := range strings.Fields(seed) {
		word := slugify(w)
		if word == "" {
			continue
		}
		next := strings.TrimPrefix(slug+"-"+word, "-")
		if len(next) > 32 {
			break
		}
		slug = next
	}
	if slug == "" {
		slug = "capture"
	}
	base := "primer-" + slug
	id := base
	for n := 2; ; n++ {
		_, taken := s.subject(id)
		s.primersMu.Lock()
		_, pending := s.primers[id]
		s.primersMu.Unlock()
		if !taken && !pending {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// workingTitle names a primer until the author does: the question, whole
// if it is short, otherwise cut at a word.
func workingTitle(prompt string) string {
	t := strings.Join(strings.Fields(prompt), " ")
	if t == "" {
		return "Capture"
	}
	if len(t) > 72 {
		cut := strings.LastIndex(t[:72], " ")
		if cut < 24 {
			cut = 72
		}
		t = t[:cut]
	}
	return strings.ToUpper(t[:1]) + t[1:]
}

func (s *Server) authorPrimer(m *primer.Meta, brief string) {
	root := s.primersRoot()
	fail := func(err error) {
		m.Status, m.Error = primer.StatusFailed, err.Error()
		primer.Save(root, m)
		log.Printf("primer %s: %v", m.ID, err)
	}
	cap := roles.Capture{Text: m.Source.Text, URL: m.Source.URL, App: m.Source.App, Prompt: m.Prompt, Brief: brief}
	rec := llm.Record(s.chain)
	title, doc, err := roles.AuthorPrimer(rec, cap)
	if err != nil && driveEnabled() && !s.chain.Status().Connected {
		title, doc, err = stubPrimer(cap)
	}
	if err != nil {
		fail(err)
		return
	}
	m.Provenance = rec.Calls()
	if !m.Named {
		m.Title = title
	}
	if err := primer.WriteDoc(root, m.ID, m.Title, doc); err != nil {
		fail(err)
		return
	}
	if err := s.registerPrimer(m); err != nil {
		fail(err)
		return
	}
	m.Status, m.Error = primer.StatusReady, ""
	if err := primer.Save(root, m); err != nil {
		log.Printf("primer %s: save: %v", m.ID, err)
	}
}

// stubPrimer stands in for the model on a dev server: the capture and the
// question come back as a document that names itself a stub.
func stubPrimer(cap roles.Capture) (string, string, error) {
	title := workingTitle(cap.Prompt)
	doc := fmt.Sprintf("[stub primer] You asked: %s\n\n## What you captured\n\n%s\n\n## The answer\n\nA dev server without a model cannot write this; a real one would answer the question from the captured material.\n",
		cap.Prompt, clipText(cap.Text, 2000))
	return title, doc, nil
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " ..."
}

type extendRequest struct {
	Quote string `json:"quote"`
	Note  string `json:"note"`
}

func (s *Server) handlePrimerExtend(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r.PathValue("subject"))
	if !ok || sub.Kind != KindPrimer || sub.Primer == nil {
		writeError(w, http.StatusNotFound, "not a primer: %q", r.PathValue("subject"))
		return
	}
	var req extendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request: %v", err)
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.Note == "" {
		writeError(w, http.StatusUnprocessableEntity, "note is required")
		return
	}
	s.markActive(sub.ID)
	root := s.primersRoot()
	m := sub.Primer

	primersMu.Lock()
	defer primersMu.Unlock()
	doc, err := primer.ReadDoc(root, m.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read primer: %v", err)
		return
	}
	rec := llm.Record(s.chain)
	heading, body, err := roles.ExtendPrimer(rec, doc, req.Quote, req.Note)
	if err != nil && driveEnabled() && !s.chain.Status().Connected {
		heading, body, err = "[stub] "+workingTitle(req.Note), fmt.Sprintf("On \"%s\" you noted: %s\n\nA dev server without a model cannot write this section.", clipText(req.Quote, 200), req.Note), nil
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "no section: %v", err)
		return
	}
	if _, err := primer.AppendSection(root, m.ID, m.Title, heading, body); err != nil {
		writeError(w, http.StatusInternalServerError, "extend primer: %v", err)
		return
	}
	c, err := corpus.Load(primer.CorpusDir(root, m.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reload primer: %v", err)
		return
	}
	s.mu.Lock()
	sub.Corpus = c
	m.Provenance = append(m.Provenance, rec.Calls()...)
	s.mu.Unlock()
	os.Remove(chapterPath(sub, primer.UnitID))

	ch, err := s.buildChapter(sub, primer.UnitID, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rebuild primer: %v", err)
		return
	}
	if err := persistChapter(sub, ch); err != nil {
		log.Printf("persist primer %s: %v", m.ID, err)
	}
	m.Entries = append(m.Entries, primer.Entry{Quote: req.Quote, Note: req.Note, Heading: heading, At: time.Now().UTC()})
	if err := primer.Save(root, m); err != nil {
		log.Printf("primer %s: save: %v", m.ID, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chapter": ch, "heading": heading, "entries": len(m.Entries)})
}

// buildPrimerChapter is the primer's whole reading contract: the document
// as written, no planner, no pretest, no check.
func (s *Server) buildPrimerChapter(sub *Subject, unitID string) (*render.Chapter, error) {
	unit, ok := sub.Corpus.Units[unitID]
	if !ok {
		return nil, fmt.Errorf("unit %s not authored", unitID)
	}
	d := render.Directives{NextAction: "read"}
	if sub.Primer != nil {
		// A primer is a model's writing; a page read as it is is not.
		d.Generated = sub.Primer.Provenance
	}
	ch, err := render.RenderChapter(unit, roles.VerbatimSections(unit), d, nil, nil)
	if err != nil {
		return nil, err
	}
	s.markUnitStarted(sub, unit, "")
	return ch, nil
}

// primerRows lists primers still authoring or failed, which are not
// subjects yet but belong on the shelf.
func (s *Server) pendingPrimers() []*primer.Meta {
	s.sweepDrafts()
	s.primersMu.Lock()
	defer s.primersMu.Unlock()
	var out []*primer.Meta
	for _, m := range s.primers {
		if m.Status != primer.StatusReady {
			out = append(out, m)
		}
	}
	return out
}
