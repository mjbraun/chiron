// Package state holds the learner model: a single-writer store with a JSON
// snapshot and an append-only JSONL event log. Every mastery change carries
// provenance - the event that caused it. Only this package mutates the model;
// everything else reads.
//
// Mastery per concept: unseen -> exposed -> shaky -> mastered. The known set
// (for fringe computation) is units whose terminal check passed the gate, or
// which were explicitly credited by a catch-up check.
//
// The snapshot format is byte-compatible with the Python implementation's, so
// an existing learner.json keeps working across the rewrite.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mjbraun/chiron/server/corpus"
)

type Concept struct {
	Level      string            `json:"level"`
	History    []LevelChange     `json:"history"`
	Confidence []ConfidencePoint `json:"confidence"`
}

type LevelChange struct {
	N    int    `json:"n"`
	From string `json:"from"`
	To   string `json:"to"`
	Why  string `json:"why"`
}

type ConfidencePoint struct {
	N          int  `json:"n"`
	Confidence int  `json:"confidence"`
	Correct    bool `json:"correct"`
}

type UnitState struct {
	Status     string   `json:"status,omitempty"`
	CheckScore *float64 `json:"check_score,omitempty"`
	Attempts   int      `json:"attempts"`
	// Missed is what the latest check got wrong, and Items how many it
	// asked: a failed gate is repaired on the misses alone, and the repair
	// round is scored against the whole check.
	Missed []string `json:"missed,omitempty"`
	Items  int      `json:"items,omitempty"`
}

type MisconceptionState struct {
	Evidence []map[string]any `json:"evidence"`
	Active   bool             `json:"active"`
}

type Debt struct {
	Unit        string   `json:"unit"`
	Concepts    []string `json:"concepts"`
	ItemsMissed []string `json:"items_missed"`
	Reason      string   `json:"reason"` // failed_gate | skipped_check
	TS          float64  `json:"ts"`
	Retired     bool     `json:"retired"`
}

type Profile struct {
	ExpertAxes   []string `json:"expert_axes"`
	NoviceAxes   []string `json:"novice_axes"`
	AssumedKnown []string `json:"assumed_known"`
	// SelfRating is the learner's own placement from the calibration
	// screener: 1 (absolute novice) to 5 (expert). 0 = not asked yet.
	SelfRating int `json:"self_rating,omitempty"`
}

type Chunk struct {
	N       int     `json:"n"`
	Minutes float64 `json:"minutes"`
	Unit    string  `json:"unit,omitempty"`
}

type Break struct {
	N       int     `json:"n"`
	Minutes float64 `json:"minutes"`
}

type Pacing struct {
	Chunks         []Chunk  `json:"chunks"`
	Breaks         []Break  `json:"breaks"`
	FatigueFlag    bool     `json:"fatigue_flag"`
	SessionStarted *float64 `json:"session_started"`
}

type Data struct {
	Version        int                            `json:"version"`
	Concepts       map[string]*Concept            `json:"concepts"`
	Units          map[string]*UnitState          `json:"units"`
	Misconceptions map[string]*MisconceptionState `json:"misconceptions"`
	Debt           []*Debt                        `json:"debt"`
	Profile        Profile                        `json:"profile"`
	Pacing         Pacing                         `json:"pacing"`
	Summary        string                         `json:"summary"`
	CurrentUnit    *string                        `json:"current_unit"`
	ChapterCache   map[string]any                 `json:"chapter_cache"`
	Questions      []Question                     `json:"questions,omitempty"`
	Flags          []Flag                         `json:"flags,omitempty"`
}

// Event is one entry in the append-only log. Fields are optional per kind; the
// zero values are never written thanks to omitempty, so the log stays readable.
type Event struct {
	N    int     `json:"n"`
	TS   float64 `json:"ts"`
	Kind string  `json:"kind"`

	Unit             string   `json:"unit,omitempty"`
	Concepts         []string `json:"concepts,omitempty"`
	Item             string   `json:"item,omitempty"`
	Concept          string   `json:"concept,omitempty"`
	Verdict          string   `json:"verdict,omitempty"`
	Confidence       *int     `json:"confidence,omitempty"`
	Misconceptions   []string `json:"misconceptions,omitempty"`
	Evidence         string   `json:"evidence,omitempty"`
	ID               string   `json:"id,omitempty"`
	Why              string   `json:"why,omitempty"`
	Score            *float64 `json:"score,omitempty"`
	Passed           *bool    `json:"passed,omitempty"`
	MasteredConcepts []string `json:"mastered_concepts,omitempty"`
	ItemsMissed      []string `json:"items_missed,omitempty"`
	Items            int      `json:"items,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Minutes          *float64 `json:"minutes,omitempty"`
	// Band is the calibration band (1-5) of a graded item; zero outside
	// calibration units.
	Band int    `json:"band,omitempty"`
	Text string `json:"text,omitempty"`
	Ref  any    `json:"ref,omitempty"`
}

// Question is something the reader asked about a passage, with the answer
// they were given. Kept so the planner can see what confused them.
type Question struct {
	Unit     string  `json:"unit"`
	Quote    string  `json:"quote"`
	Question string  `json:"question"`
	Answer   string  `json:"answer"`
	TS       float64 `json:"ts"`
}

// Flag is an item the reader thinks is wrong: the question, its reference
// answer or the grading. Kept with the answer they gave and the reference
// as it was, so the concern still reads after the bank is rewritten.
type Flag struct {
	Unit      string  `json:"unit"`
	Item      string  `json:"item"`
	Concern   string  `json:"concern"`
	Answer    string  `json:"answer,omitempty"`
	Reference string  `json:"reference,omitempty"`
	TS        float64 `json:"ts"`
}

// maxQuestions bounds the snapshot: the log keeps everything.
const maxQuestions = 200

type Learner struct {
	dir    string
	corpus *corpus.Corpus
	mu     sync.Mutex
	Data   *Data
	nowFn  func() float64
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func Open(dir string, c *corpus.Corpus) (*Learner, error) {
	l := &Learner{dir: dir, corpus: c, nowFn: now}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(l.snapshotPath())
	switch {
	case err == nil:
		var d Data
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("learner.json: %w", err)
		}
		l.Data = &d
		l.normalise()
	case os.IsNotExist(err):
		l.Data = fresh()
		if err := l.save(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return l, nil
}

func (l *Learner) snapshotPath() string { return filepath.Join(l.dir, "learner.json") }
func (l *Learner) logPath() string      { return filepath.Join(l.dir, "events.jsonl") }

// LogPath exposes the append-only log so the review exporter can replay it.
func (l *Learner) LogPath() string { return l.logPath() }

func fresh() *Data {
	return &Data{
		Concepts:       map[string]*Concept{},
		Units:          map[string]*UnitState{},
		Misconceptions: map[string]*MisconceptionState{},
		Debt:           []*Debt{},
		Profile: Profile{
			ExpertAxes:   []string{"code", "systems", "llm-tooling-behavior"},
			NoviceAxes:   []string{"matrix-calculus", "ml-notation"},
			AssumedKnown: []string{},
		},
		Pacing:       Pacing{Chunks: []Chunk{}, Breaks: []Break{}},
		Summary:      "New session. No evidence yet; treat per the static profile.",
		ChapterCache: map[string]any{},
	}
}

// normalise fills in maps a hand-edited or older snapshot might be missing, so
// a nil map never panics on first write.
func (l *Learner) normalise() {
	d := l.Data
	if d.Concepts == nil {
		d.Concepts = map[string]*Concept{}
	}
	if d.Units == nil {
		d.Units = map[string]*UnitState{}
	}
	if d.Misconceptions == nil {
		d.Misconceptions = map[string]*MisconceptionState{}
	}
	if d.ChapterCache == nil {
		d.ChapterCache = map[string]any{}
	}
	if d.Debt == nil {
		d.Debt = []*Debt{}
	}
}

// ---------- event-sourced mutation (the ONLY write path) ----------

func (l *Learner) Apply(ev Event) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ev.N = l.Data.Version + 1
	ev.TS = l.nowFn()

	// The directory can disappear between boot and write (cleanup script,
	// redeploy, wiped volume). Losing a learner's progress mid-flight to a
	// missing mkdir is not an acceptable failure, so re-create on demand.
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return ev, err
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	f, err := os.OpenFile(l.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return ev, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return ev, err
	}
	if err := f.Close(); err != nil {
		return ev, err
	}

	l.Data.Version = ev.N
	l.handle(ev)
	return ev, l.save()
}

func (l *Learner) save() error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(l.Data, "", " ")
	if err != nil {
		return err
	}
	// Write-then-rename: a crash mid-write must not leave a truncated snapshot
	// where a learner's whole history used to be.
	tmp := l.snapshotPath() + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.snapshotPath())
}

// ---------- event handlers ----------

func (l *Learner) handle(ev Event) {
	switch ev.Kind {
	case "exposed":
		for _, cid := range ev.Concepts {
			if l.concept(cid).Level == "unseen" {
				l.setLevel(cid, "exposed", ev.N, "read chapter "+ev.Unit)
			}
		}
	case "item_graded":
		l.onItemGraded(ev)
	case "misconception_cleared":
		if m, ok := l.Data.Misconceptions[ev.ID]; ok {
			m.Active = false
			m.Evidence = append(m.Evidence, map[string]any{"n": ev.N, "cleared_by": ev.Why})
		}
	case "check_result":
		l.onCheckResult(ev)
	case "override":
		u := l.unit(ev.Unit)
		u.Status = "overridden"
		l.Data.Debt = append(l.Data.Debt, &Debt{
			Unit: ev.Unit, Concepts: ev.Concepts, ItemsMissed: ev.ItemsMissed,
			Reason: ev.Reason, TS: l.nowFn(), Retired: false,
		})
	case "debt_retired":
		for _, d := range l.Data.Debt {
			if d.Unit == ev.Unit && !d.Retired {
				d.Retired = true
			}
		}
	case "chunk":
		m := 0.0
		if ev.Minutes != nil {
			m = *ev.Minutes
		}
		l.Data.Pacing.Chunks = append(l.Data.Pacing.Chunks, Chunk{N: ev.N, Minutes: m, Unit: ev.Unit})
	case "break_taken":
		m := 0.0
		if ev.Minutes != nil {
			m = *ev.Minutes
		}
		l.Data.Pacing.Breaks = append(l.Data.Pacing.Breaks, Break{N: ev.N, Minutes: m})
		l.Data.Pacing.FatigueFlag = false
	case "fatigue":
		l.Data.Pacing.FatigueFlag = true
	case "summary":
		l.Data.Summary = ev.Text
	case "unit_started":
		unit := ev.Unit
		l.Data.CurrentUnit = &unit
		l.unit(ev.Unit).Status = "active"
		if l.Data.Pacing.SessionStarted == nil {
			t := l.nowFn()
			l.Data.Pacing.SessionStarted = &t
		}
	case "chapter_cached":
		l.Data.ChapterCache[ev.Unit] = ev.Ref
	case "self_rating":
		if ev.Score != nil {
			l.Data.Profile.SelfRating = int(*ev.Score)
		}
	case "asked":
		l.Data.Questions = append(l.Data.Questions, Question{
			Unit: ev.Unit, Quote: ev.Evidence, Question: ev.Text, Answer: ev.Why, TS: ev.TS})
		if n := len(l.Data.Questions); n > maxQuestions {
			l.Data.Questions = l.Data.Questions[n-maxQuestions:]
		}
	case "flagged":
		l.Data.Flags = append(l.Data.Flags, Flag{
			Unit: ev.Unit, Item: ev.Item, Concern: ev.Text, Answer: ev.Evidence, Reference: ev.Why, TS: ev.TS})
		if n := len(l.Data.Flags); n > maxQuestions {
			l.Data.Flags = l.Data.Flags[n-maxQuestions:]
		}
	case "assumed_known":
		seen := map[string]bool{}
		for _, c := range l.Data.Profile.AssumedKnown {
			seen[c] = true
		}
		for _, c := range ev.Concepts {
			seen[c] = true
		}
		out := make([]string, 0, len(seen))
		for c := range seen {
			out = append(out, c)
		}
		sort.Strings(out)
		l.Data.Profile.AssumedKnown = out
	}
}

func (l *Learner) onItemGraded(ev Event) {
	c := l.concept(ev.Concept)
	ok := ev.Verdict == "pass" || ev.Verdict == "valid_alternative_path"
	if ev.Confidence != nil {
		c.Confidence = append(c.Confidence, ConfidencePoint{
			N: ev.N, Confidence: *ev.Confidence, Correct: ok,
		})
	}
	switch cur := c.Level; {
	case ok && (cur == "unseen" || cur == "exposed"):
		// One correct answer never jumps straight to mastered from cold.
		l.setLevel(ev.Concept, "shaky", ev.N, "correct on "+ev.Item)
	case ok && cur == "shaky" && ev.Band > 0 && ev.Band < 3:
		// A floor probe - arithmetic, recognition, a spelled-out recipe -
		// is evidence the learner is not lost, never that the concept is
		// held. Mastery waits for an item at the level of the concept itself.
	case ok && cur == "shaky":
		l.setLevel(ev.Concept, "mastered", ev.N, "correct on "+ev.Item)
	case !ok && cur == "mastered":
		l.setLevel(ev.Concept, "shaky", ev.N, "missed "+ev.Item)
	case !ok && (cur == "unseen" || cur == "exposed"):
		l.setLevel(ev.Concept, "shaky", ev.N, "missed "+ev.Item+" (needs work)")
	}
	for _, mid := range ev.Misconceptions {
		m, ok := l.Data.Misconceptions[mid]
		if !ok {
			m = &MisconceptionState{Evidence: []map[string]any{}}
			l.Data.Misconceptions[mid] = m
		}
		m.Active = true
		m.Evidence = append(m.Evidence, map[string]any{
			"n": ev.N, "item": ev.Item, "quote": ev.Evidence,
		})
	}
}

func (l *Learner) onCheckResult(ev Event) {
	u := l.unit(ev.Unit)
	u.Attempts++
	if ev.Score != nil {
		s := *ev.Score
		u.CheckScore = &s
	}
	if ev.Items > 0 {
		u.Missed = ev.ItemsMissed
		u.Items = ev.Items
	}
	passed := ev.Passed != nil && *ev.Passed
	if passed {
		u.Status = "passed"
		score := 0.0
		if ev.Score != nil {
			score = *ev.Score
		}
		why := fmt.Sprintf("passed %s check @ %.0f%%", ev.Unit, score*100)
		for _, cid := range ev.MasteredConcepts {
			l.setLevel(cid, "mastered", ev.N, why)
		}
	} else {
		u.Status = "failed"
	}
}

func (l *Learner) concept(cid string) *Concept {
	c, ok := l.Data.Concepts[cid]
	if !ok {
		c = &Concept{Level: "unseen", History: []LevelChange{}, Confidence: []ConfidencePoint{}}
		l.Data.Concepts[cid] = c
	}
	return c
}

func (l *Learner) unit(uid string) *UnitState {
	u, ok := l.Data.Units[uid]
	if !ok {
		u = &UnitState{}
		l.Data.Units[uid] = u
	}
	return u
}

func (l *Learner) setLevel(cid, level string, eventN int, why string) {
	c := l.concept(cid)
	if c.Level == level {
		return
	}
	c.History = append(c.History, LevelChange{N: eventN, From: c.Level, To: level, Why: why})
	c.Level = level
}

// Reset clears the learner model back to a fresh session.
//
// The event log is truncated rather than appended to: it is the provenance
// record for the state that exists, and keeping a previous run's grades under a
// snapshot that no longer reflects them makes the review export lie about what
// was missed. The old log is kept beside it, once, so a reset by mistake is
// recoverable.
func (l *Learner) Reset() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := os.Stat(l.logPath()); err == nil {
		if err := os.Rename(l.logPath(), l.logPath()+".previous"); err != nil {
			return err
		}
	}
	l.Data = fresh()
	return l.save()
}

// ---------- read-side queries ----------

// LastCheck is the unit's latest check: its score, the items it missed
// and how many it asked. Zero values before any check.
func (l *Learner) LastCheck(unitID string) (score float64, missed []string, items int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	u, ok := l.Data.Units[unitID]
	if !ok {
		return 0, nil, 0
	}
	if u.CheckScore != nil {
		score = *u.CheckScore
	}
	return score, append([]string(nil), u.Missed...), u.Items
}

func (l *Learner) UnitStatus(unitID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if u, ok := l.Data.Units[unitID]; ok && u.Status != "" {
		return u.Status
	}
	return "locked"
}

func (l *Learner) ClearedUnits() map[string]bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.clearedLocked()
}

func (l *Learner) clearedLocked() map[string]bool {
	out := map[string]bool{}
	for id, u := range l.Data.Units {
		if u.Status == "passed" || u.Status == "overridden" {
			out[id] = true
		}
	}
	return out
}

// Fringe is the ALEKS-style outer fringe: units whose prerequisites are all
// cleared and which are not cleared themselves. An overridden unit counts as
// cleared for availability - that is the learner's agency, and the debt ledger
// carries the difference.
func (l *Learner) Fringe() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	graph := l.corpus.PrereqGraph()
	cleared := l.clearedLocked()
	var out []string
	for _, uid := range l.corpus.UnitOrder() {
		if cleared[uid] {
			continue
		}
		if _, loaded := l.corpus.Units[uid]; !loaded {
			continue // never offer a unit whose files are missing
		}
		ready := true
		for _, p := range graph[uid] {
			if _, loaded := l.corpus.Units[p]; !loaded {
				continue
			}
			if !cleared[p] {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, uid)
		}
	}
	return out
}

func (l *Learner) ActiveMisconceptions() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for mid, m := range l.Data.Misconceptions {
		if m.Active {
			out = append(out, mid)
		}
	}
	sort.Strings(out) // map order is random in Go; prompts must be stable
	return out
}

func (l *Learner) OpenDebt() []*Debt {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*Debt
	for _, d := range l.Data.Debt {
		if !d.Retired {
			out = append(out, d)
		}
	}
	return out
}

func (l *Learner) ConceptLevel(cid string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.Data.Concepts[cid]; ok {
		return c.Level
	}
	return "unseen"
}

// FragileConcepts is the inner fringe: shaky, or mastered but answered
// correctly with low confidence. This is the review queue that cumulative
// callbacks draw from - a low-confidence-correct answer is the fluency illusion
// showing, and LLM prose amplifies it.
func (l *Learner) FragileConcepts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for cid, c := range l.Data.Concepts {
		switch {
		case c.Level == "shaky":
			out = append(out, cid)
		case c.Level == "mastered" && len(c.Confidence) > 0:
			last := c.Confidence[len(c.Confidence)-1]
			if last.Correct && last.Confidence <= 2 {
				out = append(out, cid)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (l *Learner) SessionMinutes() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Data.Pacing.SessionStarted == nil {
		return 0
	}
	return (l.nowFn() - *l.Data.Pacing.SessionStarted) / 60
}

func (l *Learner) MinutesSinceBreak() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	chunks, breaks := l.Data.Pacing.Chunks, l.Data.Pacing.Breaks
	if len(chunks) == 0 {
		return 0
	}
	total := 0.0
	if len(breaks) == 0 {
		start := max(0, len(chunks)-4)
		for _, c := range chunks[start:] {
			total += c.Minutes
		}
		return total
	}
	lastBreak := breaks[len(breaks)-1].N
	for _, c := range chunks {
		if c.N > lastBreak {
			total += c.Minutes
		}
	}
	return total
}

// Questions returns what the reader asked, newest last; unit "" means all.
func (l *Learner) Questions(unit string) []Question {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Question
	for _, q := range l.Data.Questions {
		if unit == "" || q.Unit == unit {
			out = append(out, q)
		}
	}
	return out
}

// Flags lists what the reader flagged, in one unit or in all of them.
func (l *Learner) Flags(unit string) []Flag {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Flag
	for _, f := range l.Data.Flags {
		if unit == "" || f.Unit == unit {
			out = append(out, f)
		}
	}
	return out
}

func (l *Learner) Snapshot() Data {
	l.mu.Lock()
	defer l.mu.Unlock()
	return *l.Data
}
