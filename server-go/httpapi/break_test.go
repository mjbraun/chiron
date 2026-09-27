package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unitStarts counts the chapter builds in the learner log.
func unitStarts(t *testing.T, sub *Subject) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sub.StateDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"kind":"unit_started"`)
}

// A break report is telemetry, not a request for a chapter. The chapter
// pending before the break - one being authored after a failed gate, or one
// already on disk - follows through /chapter. Authoring another here spent
// minutes of model time, replaced the remediation chapter with a plain
// rewrite of the unit, and handed the app a chapter it had not asked for
// (2026-09-27).
func TestABreakReportDoesNotAuthorAChapter(t *testing.T) {
	s := newServer(t, "")
	do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start"}`, "")
	sub, _ := s.subject("ai")
	before := unitStarts(t, sub)
	if before == 0 {
		t.Fatal("start delivered no chapter")
	}

	w := do(t, s, "POST", "/exchange", `{"subject":"ai","unit":"u0","break_minutes":5}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("break exchange -> %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Chapter   json.RawMessage `json:"chapter"`
		Authoring string          `json:"authoring"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Chapter) != "null" {
		t.Errorf("a break report delivered a chapter: %.80s", out.Chapter)
	}
	if out.Authoring != "" {
		t.Errorf("a break report started authoring %q", out.Authoring)
	}
	if after := unitStarts(t, sub); after != before {
		t.Errorf("unit_started events %d -> %d: the break report built a chapter", before, after)
	}
	if got := sub.Learner.MinutesSinceBreak(); got > 1 {
		t.Errorf("break not recorded: %.1f minutes since break", got)
	}
}
