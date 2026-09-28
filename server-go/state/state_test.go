package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mjbraun/chiron/server/corpus"
)

func newLearner(t *testing.T) *Learner {
	t.Helper()
	c, err := corpus.Load(filepath.Join("..", "..", "corpus"))
	if err != nil {
		t.Fatalf("corpus: %v", err)
	}
	l, err := Open(t.TempDir(), c)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return l
}

func f64(v float64) *float64 { return &v }
func b(v bool) *bool         { return &v }
func i(v int) *int           { return &v }

// A single correct answer must not promote a cold concept straight to mastered.
// The whole gate rests on that being two steps, not one.
func TestMasteryLadder(t *testing.T) {
	l := newLearner(t)
	apply := func(verdict string) {
		if _, err := l.Apply(Event{Kind: "item_graded", Concept: "c-x",
			Item: "q1", Verdict: verdict, Confidence: i(4)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.ConceptLevel("c-x"); got != "unseen" {
		t.Errorf("fresh concept is %q", got)
	}
	apply("pass")
	if got := l.ConceptLevel("c-x"); got != "shaky" {
		t.Errorf("one correct answer from cold -> %q, want shaky", got)
	}
	apply("pass")
	if got := l.ConceptLevel("c-x"); got != "mastered" {
		t.Errorf("second correct answer -> %q, want mastered", got)
	}
	apply("fail")
	if got := l.ConceptLevel("c-x"); got != "shaky" {
		t.Errorf("a miss after mastery -> %q, want shaky", got)
	}
}

func TestValidAlternativePathCountsAsCorrect(t *testing.T) {
	l := newLearner(t)
	for range 2 {
		if _, err := l.Apply(Event{Kind: "item_graded", Concept: "c-y", Item: "q",
			Verdict: "valid_alternative_path"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.ConceptLevel("c-y"); got != "mastered" {
		t.Errorf("valid alternative path must count as correct, got %q", got)
	}
}

// Overriding a failed gate advances the learner but must leave a debt entry -
// that is the whole bargain.
func TestOverrideAdvancesButAccruesDebt(t *testing.T) {
	l := newLearner(t)
	if _, err := l.Apply(Event{Kind: "override", Unit: "u0",
		Concepts: []string{"c-notation"}, ItemsMissed: []string{"u0-q1"},
		Reason: "failed_gate"}); err != nil {
		t.Fatal(err)
	}
	if got := l.UnitStatus("u0"); got != "overridden" {
		t.Errorf("unit status %q", got)
	}
	debt := l.OpenDebt()
	if len(debt) != 1 || debt[0].Reason != "failed_gate" {
		t.Fatalf("debt ledger: %+v", debt)
	}
	if !l.ClearedUnits()["u0"] {
		t.Error("an overridden unit must count as cleared for availability")
	}
	if _, err := l.Apply(Event{Kind: "debt_retired", Unit: "u0"}); err != nil {
		t.Fatal(err)
	}
	if len(l.OpenDebt()) != 0 {
		t.Error("retiring the debt should close it")
	}
}

// The fringe is what the learner may read next. It must never offer a unit
// whose files are missing, or one whose prerequisites are unmet.
func TestFringeRespectsPrereqsAndMissingUnits(t *testing.T) {
	l := newLearner(t)
	fringe := l.Fringe()
	if len(fringe) != 1 || fringe[0] != "u0" {
		t.Fatalf("a fresh learner's fringe should be exactly [u0], got %v", fringe)
	}
	if _, err := l.Apply(Event{Kind: "check_result", Unit: "u0",
		Score: f64(1), Passed: b(true)}); err != nil {
		t.Fatal(err)
	}
	fringe = l.Fringe()
	if len(fringe) == 0 || fringe[0] != "u1" {
		t.Errorf("after clearing u0 the fringe should open u1, got %v", fringe)
	}
	for _, uid := range fringe {
		if _, ok := l.corpus.Units[uid]; !ok {
			t.Errorf("fringe offers %s, which is not authored", uid)
		}
	}
}

// A low-confidence correct answer is the fluency illusion showing; it belongs
// in the review queue even though the concept reads as mastered.
func TestFragileConceptsCatchLowConfidenceCorrect(t *testing.T) {
	l := newLearner(t)
	if _, err := l.Apply(Event{Kind: "item_graded", Concept: "c-z", Item: "q1",
		Verdict: "pass", Confidence: i(4)}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Apply(Event{Kind: "item_graded", Concept: "c-z", Item: "q2",
		Verdict: "pass", Confidence: i(2)}); err != nil {
		t.Fatal(err)
	}
	if l.ConceptLevel("c-z") != "mastered" {
		t.Fatalf("expected mastered, got %q", l.ConceptLevel("c-z"))
	}
	found := false
	for _, c := range l.FragileConcepts() {
		if c == "c-z" {
			found = true
		}
	}
	if !found {
		t.Error("mastered-but-unconfident must land in the review queue")
	}
}

func TestMisconceptionsActivateAndClear(t *testing.T) {
	l := newLearner(t)
	if _, err := l.Apply(Event{Kind: "item_graded", Concept: "c-a", Item: "q1",
		Verdict: "fail", Misconceptions: []string{"M2"}, Evidence: "softmax gives truth"}); err != nil {
		t.Fatal(err)
	}
	if got := l.ActiveMisconceptions(); len(got) != 1 || got[0] != "M2" {
		t.Fatalf("active misconceptions %v", got)
	}
	if _, err := l.Apply(Event{Kind: "misconception_cleared", ID: "M2", Why: "u3 check"}); err != nil {
		t.Fatal(err)
	}
	if got := l.ActiveMisconceptions(); len(got) != 0 {
		t.Errorf("cleared misconception still active: %v", got)
	}
}

// Progress must survive the state directory disappearing under a running
// server - a wiped volume mid-flight is not an acceptable way to lose a session.
func TestSurvivesStateDirDisappearing(t *testing.T) {
	c, err := corpus.Load(filepath.Join("..", "..", "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	l, err := Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Apply(Event{Kind: "summary", Text: "still here"}); err != nil {
		t.Fatalf("write after the directory vanished: %v", err)
	}
	if l.Data.Summary != "still here" {
		t.Error("event was not applied")
	}
}

// The snapshot has to stay readable by, and from, the Python implementation.
func TestSnapshotShapeMatchesPython(t *testing.T) {
	l := newLearner(t)
	raw, err := os.ReadFile(l.snapshotPath())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "concepts", "units", "misconceptions",
		"debt", "profile", "pacing", "summary", "current_unit", "chapter_cache"} {
		if _, ok := m[key]; !ok {
			t.Errorf("snapshot is missing %q", key)
		}
	}
	pacing, _ := m["pacing"].(map[string]any)
	for _, key := range []string{"chunks", "breaks", "fatigue_flag", "session_started"} {
		if _, ok := pacing[key]; !ok {
			t.Errorf("pacing is missing %q", key)
		}
	}
}

// Every applied event must be replayable: the log is the provenance record.
func TestEventLogIsAppendOnlyAndComplete(t *testing.T) {
	l := newLearner(t)
	for _, kind := range []string{"unit_started", "summary", "fatigue"} {
		if _, err := l.Apply(Event{Kind: kind, Unit: "u0", Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(l.logPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, line := range splitLines(string(raw)) {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("log line is not valid json: %v", err)
		}
		lines++
		if ev.N != lines {
			t.Errorf("event %d has n=%d - the sequence must be gapless", lines, ev.N)
		}
	}
	if lines != 3 {
		t.Errorf("%d events logged, want 3", lines)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			if line := s[start:i]; line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	return out
}

// Calibration series now open with floor probes - band 1 and 2 items that
// test arithmetic and recognition, not the concept itself. Two correct floor
// answers must not certify a concept as mastered, or a novice who can
// multiply and add walks into u1 with c-dotprod "mastered" and the planner
// compresses the one section they most needed. Floor items still count as
// evidence: a miss on one is a miss.
func TestFloorProbesNeverCertifyMastery(t *testing.T) {
	l := newLearner(t)
	apply := func(verdict string, band int) {
		if _, err := l.Apply(Event{Kind: "item_graded", Concept: "c-x",
			Item: "q1", Verdict: verdict, Band: band}); err != nil {
			t.Fatal(err)
		}
	}
	apply("pass", 1)
	apply("pass", 2)
	apply("pass", 2)
	if got := l.ConceptLevel("c-x"); got != "shaky" {
		t.Errorf("three correct floor answers -> %q, want shaky (floor probes are not the concept)", got)
	}
	apply("pass", 3)
	if got := l.ConceptLevel("c-x"); got != "mastered" {
		t.Errorf("a correct band-3 answer on top of shaky -> %q, want mastered", got)
	}
	apply("fail", 1)
	if got := l.ConceptLevel("c-x"); got != "shaky" {
		t.Errorf("a floor miss after mastery -> %q, want shaky (a miss is a miss)", got)
	}
	// Teaching units carry no band; the ladder is unchanged there.
	l2 := newLearner(t)
	for range 2 {
		if _, err := l2.Apply(Event{Kind: "item_graded", Concept: "c-y", Item: "q2", Verdict: "pass"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := l2.ConceptLevel("c-y"); got != "mastered" {
		t.Errorf("unbanded ladder -> %q, want mastered", got)
	}
}

// What the reader asked about a passage is part of the state: the planner
// reads it, and it survives a restart like everything else in the log.
func TestAskedQuestionsAreKeptPerUnit(t *testing.T) {
	l := newLearner(t)
	for _, q := range []struct{ unit, quote, question, answer string }{
		{"u1", "the loss is the compressed size", "why bits and not nats?", "Because..."},
		{"u1", "4096! relabelings", "is that a factorial?", "Yes..."},
		{"u2", "softmax", "why exponentiate?", "Because..."},
	} {
		if _, err := l.Apply(Event{Kind: "asked", Unit: q.unit, Text: q.question, Evidence: q.quote, Why: q.answer}); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.Questions("u1"); len(got) != 2 || got[1].Question != "is that a factorial?" || got[1].Answer != "Yes..." {
		t.Fatalf("u1 questions = %+v", got)
	}
	if got := l.Questions(""); len(got) != 3 || got[2].Unit != "u2" {
		t.Fatalf("all questions = %+v", got)
	}

	// Survives a reload from disk.
	l2, err := Open(l.dir, l.corpus)
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.Questions(""); len(got) != 3 || got[0].Quote != "the loss is the compressed size" {
		t.Fatalf("after reload, questions = %+v", got)
	}
}

// A question or its reference answer the reader thinks is wrong is part of
// the state: the flag names the item, keeps the reader's answer and the
// reference as they were, and survives a restart like everything else.
func TestFlaggedItemsAreKeptPerUnit(t *testing.T) {
	l := newLearner(t)
	for _, f := range []struct{ unit, item, concern, answer, reference string }{
		{"u1", "u1-q1", "the key would have been rotated at 16:00", "0", "6.75"},
		{"u1", "u1-q2", "two options are both right", "B", "C"},
		{"u2", "u2-q1", "the prompt contradicts the chapter", "", "x"},
	} {
		if _, err := l.Apply(Event{Kind: "flagged", Unit: f.unit, Item: f.item, Text: f.concern, Evidence: f.answer, Why: f.reference}); err != nil {
			t.Fatal(err)
		}
	}
	got := l.Flags("u1")
	if len(got) != 2 || got[0].Item != "u1-q1" || got[0].Concern != "the key would have been rotated at 16:00" ||
		got[0].Answer != "0" || got[0].Reference != "6.75" || got[0].TS == 0 {
		t.Fatalf("u1 flags = %+v", got)
	}
	if got := l.Flags(""); len(got) != 3 || got[2].Unit != "u2" {
		t.Fatalf("all flags = %+v", got)
	}

	l2, err := Open(l.dir, l.corpus)
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.Flags(""); len(got) != 3 || got[1].Item != "u1-q2" {
		t.Fatalf("after reload, flags = %+v", got)
	}
}

// A check result keeps which items were missed and how many there were, so
// a failed gate can be repaired on the misses alone: a short chapter on
// them, then those items again, scored against the whole check.
func TestCheckResultKeepsTheMisses(t *testing.T) {
	l := newLearner(t)
	if _, err := l.Apply(Event{Kind: "check_result", Unit: "u1", Score: f64(0.4), Passed: b(false),
		ItemsMissed: []string{"u1-q2", "u1-q3", "u1-q5"}, Items: 5}); err != nil {
		t.Fatal(err)
	}
	u := l.Snapshot().Units["u1"]
	if got := u.Missed; len(got) != 3 || got[0] != "u1-q2" || got[2] != "u1-q5" {
		t.Errorf("missed after the check: %v", got)
	}
	if u.Items != 5 {
		t.Errorf("items after the check: %d, want 5", u.Items)
	}
	// The repair round closes two of the three; the record follows.
	if _, err := l.Apply(Event{Kind: "check_result", Unit: "u1", Score: f64(0.8), Passed: b(true),
		ItemsMissed: []string{"u1-q3"}, Items: 5}); err != nil {
		t.Fatal(err)
	}
	u = l.Snapshot().Units["u1"]
	if got := u.Missed; len(got) != 1 || got[0] != "u1-q3" {
		t.Errorf("missed after the repair: %v", got)
	}
	if l.UnitStatus("u1") != "passed" {
		t.Errorf("status after the repair: %s", l.UnitStatus("u1"))
	}
}

// A flag is ruled once: the ruling lands on the newest unruled flag of
// the item, and a ruled flag is not offered for ruling again.
func TestAFlagIsRuledOnce(t *testing.T) {
	l := newLearner(t)
	l.Apply(Event{Kind: "flagged", Unit: "u1", Item: "u1-q1", Text: "two answers are right", Evidence: "B", Why: "A"})
	f := l.UnruledFlag("u1-q1")
	if f == nil || f.Concern != "two answers are right" {
		t.Fatalf("unruled = %+v", f)
	}
	if l.UnruledFlag("u1-q2") != nil {
		t.Error("an item nobody flagged has no flag")
	}
	l.Apply(Event{Kind: "flag_ruled", Unit: "u1", Item: "u1-q1", Verdict: "upheld", Text: "B is right too."})
	if l.UnruledFlag("u1-q1") != nil {
		t.Error("a ruled flag is not ruled again")
	}
	flags := l.Flags("u1")
	if len(flags) != 1 || !flags[0].Upheld || flags[0].Ruling != "B is right too." {
		t.Fatalf("flags = %+v", flags)
	}
	// A new flag on the same item is its own, and waits for its own ruling.
	l.Apply(Event{Kind: "flagged", Unit: "u1", Item: "u1-q1", Text: "and the wording", Evidence: "B", Why: "A"})
	if f := l.UnruledFlag("u1-q1"); f == nil || f.Concern != "and the wording" {
		t.Fatalf("second flag = %+v", f)
	}
	l.Apply(Event{Kind: "flag_ruled", Unit: "u1", Item: "u1-q1", Verdict: "not_upheld", Text: "The wording is fine."})
	flags = l.Flags("u1")
	if flags[1].Upheld || flags[1].Ruling != "The wording is fine." || !flags[0].Upheld {
		t.Fatalf("flags = %+v", flags)
	}
}
