package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sort"

	"github.com/mjbraun/chiron/server/checkers"
	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/pages"
	"github.com/mjbraun/chiron/server/render"
	"github.com/mjbraun/chiron/server/review"
	"github.com/mjbraun/chiron/server/roles"
	"github.com/mjbraun/chiron/server/state"
	"time"
)

type BeatResponse struct {
	BeatID   string `json:"beat_id"`
	Response string `json:"response"`
	// The learner's own pass/fail after the reveal, on self-explain beats.
	SelfVerdict string `json:"self_verdict,omitempty"`
	// The JS-graded result for compute beats, graded offline while reading.
	MechanicalVerdict string `json:"mechanical_verdict,omitempty"`
	// The option tapped on a choice beat.
	SelectedIndex *int `json:"selected_index,omitempty"`
}

type ItemResponse struct {
	ItemID        string `json:"item_id"`
	Response      string `json:"response,omitempty"`
	SelectedIndex *int   `json:"selected_index,omitempty"`
	Confidence    int    `json:"confidence"` // 1-4, captured BEFORE the reveal
	// IDK is an explicit "I don't know - move on". It grades as a fail
	// without a model call: pretests expect misses, and typing filler to
	// satisfy a required answer field is worse signal than saying so.
	IDK bool `json:"idk,omitempty"`
}

type Exchange struct {
	Subject          string         `json:"subject"`
	Phase            string         `json:"phase"` // boundary | pretest | start
	Unit             string         `json:"unit,omitempty"`
	BeatResponses    []BeatResponse `json:"beat_responses"`
	PretestResponses []ItemResponse `json:"pretest_responses"`
	CheckResponses   []ItemResponse `json:"check_responses"`
	Override         bool           `json:"override"`
	SkippedCheck     bool           `json:"skipped_check"`
	CatchMeUp        bool           `json:"catch_me_up"`
	Choice           string         `json:"choice,omitempty"`
	ChunkMinutes     float64        `json:"chunk_minutes,omitempty"`
	BreakMinutes     float64        `json:"break_minutes,omitempty"`
	// Async returns a graded check at once, with the next chapter authored
	// in the background and fetched from /chapter/{subject}: a client whose
	// request dies when the OS suspends it cannot wait minutes for a model.
	Async bool `json:"async,omitempty"`
}

type Result struct {
	ItemID string `json:"item_id"`
	roles.Grade
	Confidence  *int   `json:"confidence,omitempty"`
	SelfVerdict string `json:"self_verdict,omitempty"`
}

type Gate struct {
	Score             *float64 `json:"score"`
	Passed            bool     `json:"passed"`
	Gate              float64  `json:"gate"`
	ExtensionUnlocked bool     `json:"extension_unlocked"`
	// Calibration marks a measuring unit: the gate always passes and the
	// score is information for the planner, not a verdict on the learner.
	Calibration bool `json:"calibration,omitempty"`
}

type BreakSuggestion struct {
	Minutes int    `json:"minutes"`
	Kind    string `json:"kind"`
	Note    string `json:"note"`
}

func passedVerdict(v string) bool {
	return v == "pass" || v == "valid_alternative_path"
}

// gradeItems grades item responses - mechanically in code, through the model
// only for free text. It returns (passed, total).
func (s *Server) gradeItems(sub *Subject, responses []ItemResponse, results *[]Result) (int, int) {
	type resolved struct {
		r      ItemResponse
		q      *corpus.Question
		unitID string
	}
	// Resolve everything first so free-text items can optionally be graded in a
	// single call. Submission order is preserved for both events and results.
	var items []resolved
	for _, r := range responses {
		q, unitID := sub.Corpus.FindQuestion(r.ItemID)
		if q != nil {
			items = append(items, resolved{r, q, unitID})
		}
	}

	var freeText []roles.BatchItem
	for _, it := range items {
		if it.r.IDK {
			continue // graded without a model call below
		}
		if it.q.Kind != "mcq" && !checkers.IsMechanical(it.q.Check) {
			freeText = append(freeText, roles.BatchItem{
				ItemID: it.r.ItemID, Question: it.q, Answer: it.r.Response,
			})
		}
	}
	batched := map[string]roles.Grade{}
	if s.cfg.BatchGrading && len(freeText) > 1 {
		batched = roles.GradeFreeTextBatch(s.chain, freeText, sub.Corpus.Misconceptions)
	}

	passed := 0
	for _, it := range items {
		var g roles.Grade
		if it.q.Check == "screener" {
			rating := parseRating(it.r)
			sc := float64(rating)
			sub.Learner.Apply(state.Event{Kind: "self_rating", Score: &sc})
			g = roles.Grade{Verdict: "pass",
				FeedbackMD: fmt.Sprintf("Placed at level %d.", rating)}
			*results = append(*results, Result{ItemID: it.r.ItemID, Grade: g})
			passed++
			continue
		}
		switch {
		case it.r.IDK:
			fb := "Marked \"I don't know\"."
			if it.q.Kind != "mcq" && it.q.Answer.String() != "" {
				fb += " Reference: " + it.q.Answer.String()
			}
			g = roles.Grade{Verdict: "fail", Misconceptions: []string{}, FeedbackMD: fb}
		case it.q.Kind == "mcq":
			idx := -1
			if it.r.SelectedIndex != nil {
				idx = *it.r.SelectedIndex
			}
			m := checkers.CheckMCQ(it.q.Options, idx)
			g = roles.Grade{Verdict: m.Verdict, Misconceptions: m.Misconceptions,
				FeedbackMD: m.Explain}
		case checkers.IsMechanical(it.q.Check):
			ok, err := checkers.CheckAnswer(it.q.Check, it.q.Answer.String(), it.r.Response)
			verdict := "fail"
			if err != nil {
				// A check spec the server cannot evaluate is a corpus bug.
				// Marking it wrong would blame the learner for it.
				verdict = "ungraded"
			} else if ok {
				verdict = "pass"
			}
			g = roles.Grade{Verdict: verdict, Misconceptions: []string{},
				FeedbackMD: "Reference: " + it.q.Answer.String()}
		default:
			// Fall back per item for anything the batch did not return, so a
			// partial batch degrades rather than silently dropping grades.
			if b, ok := batched[it.r.ItemID]; ok {
				g = b
			} else {
				g = roles.GradeFreeText(s.chain, it.q, it.r.Response, sub.Corpus.Misconceptions)
			}
		}
		if passedVerdict(g.Verdict) {
			passed++
		}
		conf := it.r.Confidence
		if _, err := sub.Learner.Apply(state.Event{
			Kind: "item_graded", Item: it.r.ItemID, Concept: it.q.Concept,
			Unit: it.unitID, Verdict: g.Verdict, Confidence: &conf, Band: it.q.Band,
			Misconceptions: g.Misconceptions, Evidence: g.Evidence,
		}); err != nil {
			return passed, len(responses)
		}
		*results = append(*results, Result{ItemID: it.r.ItemID, Grade: g, Confidence: &conf})
	}
	return passed, len(responses)
}

func (s *Server) gradeBeats(sub *Subject, responses []BeatResponse, results *[]Result) {
	for _, r := range responses {
		beat, unitID := sub.Corpus.FindBeat(r.BeatID)
		if beat == nil {
			continue
		}
		check := beat.Check
		if check == "" {
			check = "llm"
		}
		var g roles.Grade
		if len(beat.Options) > 0 {
			idx := -1
			if r.SelectedIndex != nil {
				idx = *r.SelectedIndex
			}
			m := checkers.CheckMCQ(beat.Options, idx)
			g = roles.Grade{Verdict: m.Verdict, Misconceptions: m.Misconceptions, FeedbackMD: m.Explain}
		} else if checkers.IsMechanical(check) {
			ok, err := checkers.CheckAnswer(check, beat.Answer.String(), r.Response)
			verdict := "fail"
			if err != nil {
				verdict = "ungraded"
			} else if ok {
				verdict = "pass"
			}
			g = roles.Grade{Verdict: verdict, Misconceptions: []string{}}
		} else {
			g = roles.GradeFreeText(s.chain, &corpus.Question{
				Prompt: beat.Prompt, Answer: beat.Answer, Rubric: beat.Rubric,
			}, r.Response, sub.Corpus.Misconceptions)
		}
		if _, err := sub.Learner.Apply(state.Event{
			Kind: "item_graded", Item: r.BeatID, Concept: beat.Concept, Unit: unitID,
			Verdict: g.Verdict, Misconceptions: g.Misconceptions, Evidence: g.Evidence,
		}); err != nil {
			return
		}
		*results = append(*results, Result{ItemID: r.BeatID, Grade: g, SelfVerdict: r.SelfVerdict})
	}
}

// calibrationSummary is the one line the planner gets about a calibration
// series: what the learner claimed, and how each band actually went. The
// bands are what make the claim checkable - a self-rated novice who cleared
// bands 1 and 2 but nothing at 3 is a different learner from one who cleared
// 3, and "40% overall" cannot tell them apart.
func calibrationSummary(unit *corpus.Unit, rating int, score float64, results []Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Calibration %s: ", unit.ID)
	if rating > 0 {
		label := ""
		if sc := unit.Questions.Screener; sc != nil && rating <= len(sc.Options) {
			label = " (" + sc.Options[rating-1].Text + ")"
		}
		fmt.Fprintf(&b, "self-rated level %d of 5%s; ", rating, label)
	}
	fmt.Fprintf(&b, "%.0f%% overall", score*100)
	byID := map[string]corpus.Question{}
	for _, q := range unit.Questions.Check {
		byID[q.ID] = q
	}
	right, total := map[int]int{}, map[int]int{}
	for _, r := range results {
		q, ok := byID[r.ItemID]
		if !ok || q.Band == 0 {
			continue
		}
		total[q.Band]++
		if passedVerdict(r.Verdict) {
			right[q.Band]++
		}
	}
	var parts []string
	for band := 1; band <= 5; band++ {
		if total[band] > 0 {
			parts = append(parts, fmt.Sprintf("band %d: %d/%d", band, right[band], total[band]))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "; correct by band (1 = arithmetic floor, 3 = can do it slowly, 5 = expert): %s",
			strings.Join(parts, ", "))
	}
	b.WriteString("; per-concept levels are in the state. ")
	return b.String()
}

// parseRating reads a 1-5 self-placement from a screener response. Anything
// unreadable (including "I don't know") lands in the middle.
func parseRating(r ItemResponse) int {
	if r.SelectedIndex != nil {
		if n := *r.SelectedIndex + 1; n >= 1 && n <= 5 {
			return n
		}
	}
	for _, c := range r.Response {
		if c >= '1' && c <= '5' {
			return int(c - '0')
		}
	}
	return 3
}

// calibrationItems selects a calibration unit's delivery for a self-rating:
// the screener alone before a rating exists, the level's pre-computed series
// after, the whole bank if no set is defined for the level.
func calibrationItems(unit *corpus.Unit, rating int) []corpus.Question {
	if rating == 0 && unit.Questions.Screener != nil {
		q := *unit.Questions.Screener
		q.Unit = unit.ID
		return []corpus.Question{q}
	}
	var items []corpus.Question
	if ids := unit.Questions.CalibrationSets[rating]; len(ids) > 0 {
		byID := map[string]corpus.Question{}
		for _, q := range unit.Questions.Check {
			byID[q.ID] = q
		}
		for _, id := range ids {
			if q, ok := byID[id]; ok {
				q.Unit = unit.ID
				items = append(items, q)
			}
		}
		return items
	}
	for _, q := range unit.Questions.Check {
		q.Unit = unit.ID
		items = append(items, q)
	}
	return items
}

// prerenderCalibrationSets renders the page stack for every level, middle
// levels first (they are the most likely picks). Chapter composition here
// must stay byte-identical to what buildChapter will produce once the
// rating is recorded, or the cache keys will not line up.
func (s *Server) prerenderCalibrationSets(sub *Subject, unit *corpus.Unit, checkSummary string) {
	sub.setStage("planning")
	d := roles.PlanDirectives(s.chain, sub.Learner, unit, checkSummary)
	sub.setStage("writing")
	sections, _ := roles.AuthorChapter(s.chain, unit, d, sub.Corpus)
	var wg sync.WaitGroup
	for rating := 1; rating <= 5; rating++ {
		if len(unit.Questions.CalibrationSets[rating]) == 0 {
			continue
		}
		cp := *unit
		cp.IntroMD = "" // must match buildChapter's series composition
		ch, err := render.RenderChapter(&cp, sections,
			render.Directives{OpeningNoteMD: d.OpeningNoteMD, NextAction: d.NextAction},
			unit.Questions.Pretest, calibrationItems(unit, rating))
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(rating int, ch *render.Chapter) {
			defer wg.Done()
			if _, err := sub.Pages.Render(ch); err != nil {
				log.Printf("prerender calibration level %d: %v", rating, err)
			}
		}(rating, ch)
	}
	wg.Wait()
}

// followsCalibration reports whether the unit sits directly after a
// calibration unit in the syllabus.
func followsCalibration(c *corpus.Corpus, unitID string) bool {
	order := c.UnitOrder()
	for i, id := range order {
		if id == unitID && i > 0 {
			prev, ok := c.Units[order[i-1]]
			return ok && prev.IsCalibration()
		}
	}
	return false
}

func (s *Server) nextUnit(sub *Subject, choice string) string {
	fringe := sub.Learner.Fringe()
	if len(fringe) == 0 {
		return ""
	}
	for _, uid := range fringe {
		if uid == choice {
			return choice
		}
	}
	return fringe[0]
}

// authorCachePath keys test-mode chapter caching. The cache trades
// adaptivity for speed - UI runs replay a previously authored chapter
// instantly instead of waiting on the model - so it exists only in drive
// (test) mode and never on a real learner's server.
func authorCachePath(subjectID, unitID string) string {
	return filepath.Join(os.TempDir(), "chiron-author-cache",
		subjectID+"-"+unitID+".json")
}

type authorCacheEntry struct {
	Chapter *render.Chapter `json:"chapter"`
	Summary string          `json:"summary"`
}

func (s *Server) buildChapter(sub *Subject, unitID, checkSummary string) (*render.Chapter, error) {
	// A primer and an imported book are read as they are: the unit's own
	// prose, whole, with nothing to answer.
	if sub.Kind == KindPrimer || sub.Kind == KindReading {
		return s.buildPrimerChapter(sub, unitID)
	}
	unit, ok := sub.Corpus.Units[unitID]
	if !ok {
		return nil, fmt.Errorf("unit %s not authored", unitID)
	}
	if driveEnabled() && !unit.IsCalibration() {
		if data, err := os.ReadFile(authorCachePath(sub.ID, unitID)); err == nil {
			var entry authorCacheEntry
			if json.Unmarshal(data, &entry) == nil && entry.Chapter != nil {
				log.Printf("author cache hit: %s/%s", sub.ID, unitID)
				if followsCalibration(sub.Corpus, unitID) {
					entry.Chapter.Pretest = nil
				}
				s.markUnitStarted(sub, unit, entry.Summary)
				return entry.Chapter, nil
			}
		}
	}
	sub.setStage("planning")
	d := roles.PlanDirectives(s.chain, sub.Learner, unit, checkSummary)
	sub.setStage("writing")
	sections, _ := roles.AuthorChapter(s.chain, unit, d, sub.Corpus)

	var items []corpus.Question
	if unit.IsCalibration() {
		// Self-placement first; then the pre-computed series for that
		// level, complete and in authored order - no shuffle, no cap.
		rating := sub.Learner.Data.Profile.SelfRating
		items = calibrationItems(unit, rating)
		if rating == 0 {
			// Every level's series is already known: render all five page
			// stacks while the learner reads the placement question, so
			// whichever they pick is on disk before they ask for it.
			s.renders.Add(1)
			go func() {
				defer s.renders.Done()
				s.prerenderCalibrationSets(sub, unit, checkSummary)
			}()
		}
	} else {
		s.rngMu.Lock()
		items = roles.ComposeCheck(unit, sub.Learner, sub.Corpus,
			s.cfg.Session.CheckItems, s.cfg.Session.CallbackFraction, s.rng)
		s.rngMu.Unlock()
	}

	if err := s.markUnitStarted(sub, unit, d.Summary); err != nil {
		return nil, err
	}
	ru := unit
	if unit.IsCalibration() && sub.Learner.Data.Profile.SelfRating > 0 {
		// The intro frames the placement step; once placed, the series
		// opens on its first question, not on stale framing.
		cp := *unit
		cp.IntroMD = ""
		ru = &cp
	}
	pretest := unit.Questions.Pretest
	if followsCalibration(sub.Corpus, unitID) {
		// The calibration series just measured this ground minutes ago; a
		// pretest here would be two question blocks back to back.
		pretest = nil
	}
	ch, err := render.RenderChapter(ru, sections,
		render.Directives{OpeningNoteMD: d.OpeningNoteMD, NextAction: d.NextAction},
		pretest, items)
	if err == nil && driveEnabled() && !unit.IsCalibration() {
		if data, merr := json.Marshal(authorCacheEntry{Chapter: ch, Summary: d.Summary}); merr == nil {
			_ = os.MkdirAll(filepath.Dir(authorCachePath(sub.ID, unitID)), 0o755)
			_ = os.WriteFile(authorCachePath(sub.ID, unitID), data, 0o644)
		}
	}
	return ch, err
}

// markUnitStarted applies the delivery events for a unit build.
func (s *Server) markUnitStarted(sub *Subject, unit *corpus.Unit, summary string) error {
	if _, err := sub.Learner.Apply(state.Event{Kind: "unit_started", Unit: unit.ID}); err != nil {
		return err
	}
	if _, err := sub.Learner.Apply(state.Event{Kind: "exposed", Unit: unit.ID,
		Concepts: unit.ConceptIDs()}); err != nil {
		return err
	}
	if _, err := sub.Learner.Apply(state.Event{Kind: "summary", Text: summary}); err != nil {
		return err
	}
	if assumes, ok := unit.Front["assumes"].([]any); ok && len(assumes) > 0 {
		var concepts []string
		for _, a := range assumes {
			if s, ok := a.(string); ok {
				concepts = append(concepts, s)
			}
		}
		if len(concepts) > 0 {
			if _, err := sub.Learner.Apply(state.Event{Kind: "assumed_known",
				Concepts: concepts}); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildCatchup is the comprehensive backfill from the whole debt ledger:
// representation-switched sections for every debted concept, then a combined
// check that can retire the debt.
func (s *Server) buildCatchup(sub *Subject) (*render.Chapter, error) {
	debt := sub.Learner.OpenDebt()
	if len(debt) == 0 {
		return nil, nil
	}
	var sections []render.AssembledSection
	var checkPool []corpus.Question

	for _, d := range debt {
		unit, ok := sub.Corpus.Units[d.Unit]
		if !ok {
			continue
		}
		// Remediation must switch representation, never re-present the same
		// text more slowly - that is the documented failure of mastery
		// programs.
		variant := ""
		for _, v := range []string{"more-intuition", "se-analogies", "deeper-math"} {
			if _, ok := unit.Depths[v]; ok {
				variant = v
				break
			}
		}
		debted := map[string]bool{}
		for _, c := range d.Concepts {
			debted[c] = true
		}
		for _, sec := range unit.Sections {
			covers := len(d.Concepts) == 0
			for _, seg := range sec.Segments {
				if seg.Type == "beat" && debted[seg.Beat.Concept] {
					covers = true
				}
			}
			if !covers {
				continue
			}
			md := sec.Markdown()
			if variant != "" {
				if text, ok := unit.Depths[variant][sec.Heading]; ok && len(text) > 400 {
					md = "## " + sec.Heading + "\n\n" + text
				}
			}
			sections = append(sections, render.AssembledSection{
				Heading: fmt.Sprintf("[%s] %s", unit.ID, sec.Heading), Markdown: md,
			})
		}
		missed := map[string]bool{}
		for _, id := range d.ItemsMissed {
			missed[id] = true
		}
		for _, q := range unit.Questions.Check {
			if missed[q.ID] || debted[q.Concept] {
				q.Unit = unit.ID
				checkPool = append(checkPool, q)
			}
		}
	}
	if len(sections) == 0 {
		return nil, nil
	}

	minutes := 8 * len(debt)
	if minutes < 15 {
		minutes = 15
	}
	catchup := &corpus.Unit{
		ID: "catchup", Title: "Catch-up: closing the gaps", Minutes: minutes,
		IntroMD: "This chapter consolidates everything skipped or missed so far, " +
			"explained differently than the first pass. The check at the end " +
			"retires the debt it covers.",
	}
	s.rngMu.Lock()
	s.rng.Shuffle(len(checkPool), func(i, j int) {
		checkPool[i], checkPool[j] = checkPool[j], checkPool[i]
	})
	s.rngMu.Unlock()
	n := len(checkPool) / 2
	if n < 8 {
		n = 8
	}
	if n > len(checkPool) {
		n = len(checkPool)
	}
	return render.RenderChapter(catchup, sections, render.Directives{
		NextAction: "Work every section, then take the combined check.",
	}, nil, checkPool[:n])
}

func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request) {
	var ex Exchange
	if err := json.NewDecoder(r.Body).Decode(&ex); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request: %v", err)
		return
	}
	if ex.Subject == "" {
		ex.Subject = "ai"
	}
	if ex.Phase == "" {
		ex.Phase = "boundary"
	}
	sub, ok := s.subject(ex.Subject)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown subject: %s", ex.Subject)
		return
	}
	// Every exchange is the reader working in this book; a client that
	// never fetches rendered pages has no other way to say which is open.
	s.markActive(sub.ID)
	writeJSON(w, http.StatusOK, s.processExchange(sub, ex, ex.Async))
}

// handleChapter is the JSON client's view of the current chapter and of the
// authoring that may be producing the next one: {chapter, authoring,
// authoring_error}. The chapter is null until one exists; ?unit= reads any
// persisted chapter without touching learner state.
func (s *Server) handleChapter(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r.PathValue("subject"))
	if !ok {
		http.Error(w, "unknown subject", http.StatusNotFound)
		return
	}
	building, buildErr := sub.buildStatus()
	out := map[string]any{
		"chapter":         nil,
		"authoring":       building,
		"authoring_error": buildErr,
	}
	if stage, seconds := sub.buildProgress(); stage != "" {
		out["authoring_stage"], out["authoring_seconds"] = stage, seconds
	}
	if ch, err := requestChapter(sub, r); err == nil {
		out["chapter"] = ch
	}
	writeJSON(w, http.StatusOK, out)
}

// processExchange is the whole boundary contract - grade, gate, plan, author,
// persist - shared by the JSON exchange endpoint and the ink check-in.
// asyncAuthor returns the graded exchange immediately and authors the next
// chapter in the background - the learner reads their results while the
// model writes. The chapter then arrives via the pages meta. Only graded
// submissions (a gate exists) go async: everything else is fast already.
func (s *Server) processExchange(sub *Subject, ex Exchange, asyncAuthor bool) map[string]any {
	results := []Result{}
	var gate *Gate
	var chapter *render.Chapter
	var breakSuggestion *BreakSuggestion
	sess := s.cfg.Session

	// Telemetry first: it feeds the fatigue term.
	if ex.ChunkMinutes > 0 {
		m := ex.ChunkMinutes
		sub.Learner.Apply(state.Event{Kind: "chunk", Minutes: &m, Unit: ex.Unit})
	}
	if ex.BreakMinutes > 0 {
		m := ex.BreakMinutes
		sub.Learner.Apply(state.Event{Kind: "break_taken", Minutes: &m})
	}

	s.gradeBeats(sub, ex.BeatResponses, &results)

	var summary strings.Builder
	if len(ex.PretestResponses) > 0 {
		p, t := s.gradeItems(sub, ex.PretestResponses, &results)
		fmt.Fprintf(&summary, "Pretest: %d/%d. ", p, t)
	}

	screenerOnly := len(ex.CheckResponses) > 0
	for _, r := range ex.CheckResponses {
		if q, _ := sub.Corpus.FindQuestion(r.ItemID); q == nil || q.Check != "screener" {
			screenerOnly = false
			break
		}
	}

	switch {
	case screenerOnly:
		// Self-placement recorded; no gate, no check_result. The chapter
		// build below re-delivers the calibration unit, now carrying the
		// series for the chosen level.
		s.gradeItems(sub, ex.CheckResponses, &results)
	case len(ex.CheckResponses) > 0:
		p, t := s.gradeItems(sub, ex.CheckResponses, &results)
		score := 0.0
		if t > 0 {
			score = float64(p) / float64(t)
		}
		unit, haveUnit := sub.Corpus.Units[ex.Unit]
		calibration := haveUnit && unit.IsCalibration()
		passed := score >= sess.MasteryGate || calibration
		var mastered []string
		if haveUnit && passed && !calibration {
			mastered = unit.ConceptIDs()
		}
		sub.Learner.Apply(state.Event{Kind: "check_result", Unit: ex.Unit,
			Score: &score, Passed: &passed, MasteredConcepts: mastered})

		if passed && ex.Unit == "catchup" {
			for _, d := range sub.Learner.OpenDebt() {
				sub.Learner.Apply(state.Event{Kind: "debt_retired", Unit: d.Unit})
			}
		}
		var missed []string
		for _, res := range results {
			if !passedVerdict(res.Verdict) {
				missed = append(missed, res.ItemID)
			}
		}
		sc := score
		gate = &Gate{Score: &sc, Passed: passed, Gate: sess.MasteryGate,
			ExtensionUnlocked: !calibration && score >= sess.ExtensionTrigger,
			Calibration:       calibration}
		if calibration {
			summary.WriteString(calibrationSummary(unit, sub.Learner.Data.Profile.SelfRating, score, results))
		} else {
			verdict := "below gate"
			if passed {
				verdict = "passed"
			}
			fmt.Fprintf(&summary, "Check %s: %.0f%% (%s). ", ex.Unit, score*100, verdict)
		}

		if !passed && ex.Override && haveUnit {
			var unmastered []string
			for _, c := range unit.ConceptIDs() {
				if sub.Learner.ConceptLevel(c) != "mastered" {
					unmastered = append(unmastered, c)
				}
			}
			sub.Learner.Apply(state.Event{Kind: "override", Unit: ex.Unit,
				Concepts: unmastered, ItemsMissed: missed, Reason: "failed_gate"})
		}

	case ex.SkippedCheck && ex.Unit != "":
		if unit, ok := sub.Corpus.Units[ex.Unit]; ok {
			sub.Learner.Apply(state.Event{Kind: "override", Unit: ex.Unit,
				Concepts: unit.ConceptIDs(), Reason: "skipped_check"})
			gate = &Gate{Passed: false, Gate: sess.MasteryGate}
		}
	}

	// Fatigue heuristic: 90+ minutes without a logged break earns a suggestion
	// and a flag. Late-session errors are fatigue, not knowledge gaps, and must
	// not be read as a mastery downgrade.
	if sub.Learner.MinutesSinceBreak() >= sess.LongBreakEveryChunks*sess.ChunkMinutes {
		sub.Learner.Apply(state.Event{Kind: "fatigue"})
		breakSuggestion = &BreakSuggestion{Minutes: 15, Kind: "long",
			Note: "90+ minutes since a break. Late-session errors will be misread " +
				"as knowledge gaps - rest first."}
	} else if ex.ChunkMinutes >= sess.ChunkMinutes && ex.ChunkMinutes > 0 {
		breakSuggestion = &BreakSuggestion{Minutes: int(sess.BreakMinutes), Kind: "short",
			Note: "Chunk done. Five minutes, eyes off screens."}
	}

	// A break report is telemetry, not a request for a chapter: nothing was
	// answered and nothing is owed. Whatever was pending before the break
	// (a chapter being authored after a failed gate, or one already on disk)
	// follows through /chapter; authoring another here would spend minutes
	// of model time and replace the remediation chapter with a plain rewrite.
	breakReport := ex.BreakMinutes > 0 && ex.Phase != "start" &&
		len(ex.CheckResponses) == 0 && !ex.Override && !ex.SkippedCheck && !ex.CatchMeUp
	advanceAllowed := (gate == nil || gate.Passed || ex.Override || ex.SkippedCheck) && !breakReport
	if ex.CatchMeUp {
		if ch, err := s.buildCatchup(sub); err == nil {
			chapter = ch
		} else {
			log.Printf("build catchup: %v", err)
		}
	}
	authoring := ""
	if chapter == nil && (ex.Phase == "boundary" || ex.Phase == "start") && advanceAllowed {
		if next := s.nextUnit(sub, ex.Choice); next != "" {
			if asyncAuthor && gate != nil {
				s.buildAsync(sub, next, summary.String())
				authoring = next
			} else if ch, err := s.buildChapter(sub, next, summary.String()); err == nil {
				chapter = ch
			} else {
				log.Printf("build chapter %s: %v", next, err)
			}
		}
	} else if chapter == nil && gate != nil && !gate.Passed && !ex.Override && ex.Unit != "" {
		// Remediation loop: rebuild the SAME unit. The planner sees the failed
		// check in the summary and must switch representation.
		remSummary := summary.String() +
			"REMEDIATE: switch representation, do not re-explain the same way."
		if asyncAuthor {
			s.buildAsync(sub, ex.Unit, remSummary)
			authoring = ex.Unit
		} else if ch, err := s.buildChapter(sub, ex.Unit, remSummary); err == nil {
			chapter = ch
		} else {
			log.Printf("build remediation %s: %v", ex.Unit, err)
		}
	}

	if chapter != nil {
		if err := persistChapter(sub, chapter); err != nil {
			log.Printf("persist chapter %s: %v", chapter.Unit, err)
		}
		// Render the page images now, while the learner is still reading
		// their results - by the time they ask for the next chapter it is
		// already on disk. The renderer serializes with the on-demand path.
		s.renders.Add(1)
		go func(ch *render.Chapter) {
			defer s.renders.Done()
			if _, err := sub.Pages.Render(ch); err != nil {
				log.Printf("eager page render %s: %v", ch.Unit, err)
			}
		}(chapter)
	}
	out := map[string]any{
		"results":          results,
		"gate":             gate,
		"chapter":          chapter,
		"state":            s.statePayload(sub),
		"break_suggestion": breakSuggestion,
	}
	if authoring != "" {
		out["authoring"] = authoring
	}
	// A graded check gets typeset results pages (SPEC §0.1): render them
	// before responding - the grading wait covers grade + typeset time, so
	// the client lands on finished pages.
	if gate != nil && gate.Score != nil {
		if doc := s.buildResultsDoc(sub, ex, results, gate); len(doc.Entries) > 0 {
			out["results_doc"] = doc
			if err := persistResults(sub, doc); err != nil {
				log.Printf("persist results %s: %v", ex.Unit, err)
			}
			if res, err := sub.Pages.RenderResults(doc); err == nil {
				out["results_pages"] = map[string]any{
					"count": res.Count, "hash": res.Hash, "action": doc.Action,
				}
			} else {
				log.Printf("render results %s: %v", ex.Unit, err)
			}
		}
	}
	return out
}

// buildResultsDoc assembles the typeset results content from the graded
// exchange: printed item numbers from the chapter's own sequence, the
// transcripts, references, and the framing copy.
func (s *Server) buildResultsDoc(sub *Subject, ex Exchange, results []Result, gate *Gate) *pages.ResultsDoc {
	respByID := map[string]ItemResponse{}
	for _, r := range ex.PretestResponses {
		respByID[r.ItemID] = r
	}
	for _, r := range ex.CheckResponses {
		respByID[r.ItemID] = r
	}
	nByID := map[string]int{}
	title := ex.Unit
	if ch, err := loadChapter(sub, ex.Unit); err == nil {
		title = ch.Title
		n := 1
		for _, it := range ch.Pretest {
			nByID[it.ID] = n
			n++
		}
		for _, it := range ch.Check {
			nByID[it.ID] = n
			n++
		}
	}

	doc := &pages.ResultsDoc{
		Unit:              ex.Unit,
		Calibration:       gate.Calibration,
		Score:             *gate.Score,
		GatePct:           gate.Gate,
		Passed:            gate.Passed,
		ExtensionUnlocked: gate.ExtensionUnlocked,
	}
	switch {
	case gate.Calibration:
		doc.HeadLeft = strings.ToUpper(sub.Title)
		doc.Action = "Begin chapter 1"
		doc.Dek = "A measurement, not a verdict - the chapters ahead are shaped by where the sure answers stopped."
	case gate.Passed:
		doc.HeadLeft = pages.HeadLeft(ex.Unit, title)
		doc.Action = "Next chapter"
		if gate.ExtensionUnlocked {
			doc.Dek = "Cleared with room to spare - an extension section is unlocked in the next chapter."
		} else {
			doc.Dek = "The next chapter builds on what held. The misses below are worth a minute before moving on."
		}
	default:
		doc.HeadLeft = pages.HeadLeft(ex.Unit, title)
		doc.Action = "Back to the chapter"
		doc.Dek = "Not yet - the chapter returns from a different angle. The reveals below are the map."
	}

	for _, res := range results {
		q, _ := sub.Corpus.FindQuestion(res.ItemID)
		if q == nil || q.Check == "screener" {
			continue
		}
		r := respByID[res.ItemID]
		e := pages.ResultsEntry{
			N:       nByID[res.ItemID],
			Verdict: res.Verdict,
			IDK:     r.IDK,
			Kind:    q.Kind,
			Prompt:  q.Prompt,
		}
		if !r.IDK {
			e.Confidence = r.Confidence
		}
		switch {
		case q.Kind == "mcq":
			if r.SelectedIndex != nil && *r.SelectedIndex >= 0 && *r.SelectedIndex < len(q.Options) {
				e.Chose = fmt.Sprintf("%c — '%s'", 'A'+*r.SelectedIndex, q.Options[*r.SelectedIndex].Text)
			}
			for i, o := range q.Options {
				if o.Correct {
					e.Answer = fmt.Sprintf("%c — %s", 'A'+i, o.Text)
					break
				}
			}
		default:
			e.ReadAs = r.Response
			e.Answer = q.Answer.String()
		}
		if fb := strings.TrimSpace(res.FeedbackMD); fb != "" &&
			!strings.HasPrefix(fb, "Reference:") && !strings.HasPrefix(fb, "Marked \"I don't know\".") {
			e.Why = fb
		}
		doc.Entries = append(doc.Entries, e)
	}
	// Unmapped items (not in the persisted chapter) sort last, in
	// submission order.
	next := len(nByID) + 1
	for i := range doc.Entries {
		if doc.Entries[i].N == 0 {
			doc.Entries[i].N = next
			next++
		}
	}
	sort.SliceStable(doc.Entries, func(i, j int) bool {
		return doc.Entries[i].N < doc.Entries[j].N
	})
	doc.Summarize()
	return doc
}

// buildAsync authors a chapter in the background and delivers it through
// persistence + eager page render. Failures land in the subject's build
// status for the pages meta to surface.
func (s *Server) buildAsync(sub *Subject, unitID, checkSummary string) {
	if !sub.beginBuild() {
		return
	}
	s.renders.Add(1)
	go func() {
		defer s.renders.Done()
		ch, err := s.buildChapter(sub, unitID, checkSummary)
		if err != nil {
			log.Printf("async build %s: %v", unitID, err)
			sub.endBuild(err.Error())
			return
		}
		if err := persistChapter(sub, ch); err != nil {
			log.Printf("persist chapter %s: %v", ch.Unit, err)
			sub.endBuild(err.Error())
			return
		}
		sub.setStage("pages")
		if _, err := sub.Pages.Render(ch); err != nil {
			log.Printf("eager page render %s: %v", ch.Unit, err)
		}
		sub.endBuild("")
	}()
}

func (s *Server) statePayload(sub *Subject) map[string]any {
	type spineRow struct {
		Unit     string   `json:"unit"`
		Title    string   `json:"title"`
		Status   string   `json:"status"`
		Score    *float64 `json:"score"`
		InFringe bool     `json:"in_fringe"`
	}
	fringe := sub.Learner.Fringe()
	inFringe := map[string]bool{}
	for _, uid := range fringe {
		inFringe[uid] = true
	}
	snapshot := sub.Learner.Snapshot()
	spine := []spineRow{}
	for _, uid := range sub.Corpus.UnitOrder() {
		title := uid
		if u, ok := sub.Corpus.Units[uid]; ok {
			title = u.Title
		}
		var score *float64
		if us, ok := snapshot.Units[uid]; ok {
			score = us.CheckScore
		}
		spine = append(spine, spineRow{
			Unit: uid, Title: title, Status: sub.Learner.UnitStatus(uid),
			Score: score, InFringe: inFringe[uid],
		})
	}
	if fringe == nil {
		fringe = []string{}
	}
	debt := sub.Learner.OpenDebt()
	if debt == nil {
		debt = []*state.Debt{}
	}
	mis := sub.Learner.ActiveMisconceptions()
	if mis == nil {
		mis = []string{}
	}
	return map[string]any{
		"spine":                 spine,
		"fringe":                fringe,
		"debt":                  debt,
		"active_misconceptions": mis,
		"summary":               snapshot.Summary,
		"session_minutes":       sub.Learner.SessionMinutes(),
		"llm":                   s.chain.Status(),
	}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(s.subjectParam(r))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown subject: %s", s.subjectParam(r))
		return
	}
	writeJSON(w, http.StatusOK, s.statePayload(sub))
}

// handleReviewSchedule returns the day-1/3/10 plan built from what actually
// went wrong. It is self-contained - prompts and answers inline - so it is
// useful with no server, no model and no network.
func (s *Server) handleReviewSchedule(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(s.subjectParam(r))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown subject: %s", s.subjectParam(r))
		return
	}
	// Served as text/plain, not HTML: this is a markdown document meant to be
	// printed or synced to a reader, so there is no markup context for the
	// learner's own answers to escape into.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, review.Build(sub.Learner, sub.Corpus, sub.Title, time.Now()))
}
