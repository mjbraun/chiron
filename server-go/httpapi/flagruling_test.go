package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/llm"
)

// rulingChain answers the flag ruling as scripted and hands every other
// call to the server's own chain.
type rulingChain struct {
	llm.Chain
	ruling map[string]any // nil: the model is unreachable for rulings
	asked  []string
}

func (c *rulingChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	if name != "flag_ruling" {
		return c.Chain.Structured(role, system, user, schema, name, out)
	}
	c.asked = append(c.asked, user)
	if c.ruling == nil {
		return errors.New("no model")
	}
	data, _ := json.Marshal(c.ruling)
	return json.Unmarshal(data, out)
}

type gradedCheck struct {
	Results []struct {
		ItemID     string `json:"item_id"`
		Verdict    string `json:"verdict"`
		FeedbackMD string `json:"feedback_md"`
	} `json:"results"`
	Gate struct {
		Score *float64 `json:"score"`
	} `json:"gate"`
	ResultsDoc struct {
		Entries []struct {
			Verdict    string `json:"verdict"`
			Flag       string `json:"flag"`
			FlagRuling string `json:"flag_ruling"`
			FlagUpheld bool   `json:"flag_upheld"`
		} `json:"entries"`
	} `json:"results_doc"`
}

// Flag the first item, pass on every item, and hand the check in.
func flagAndSubmit(t *testing.T, ruling map[string]any) (*Server, *rulingChain, []string, gradedCheck) {
	t.Helper()
	s := newServer(t, "")
	unit, items := walkToSeries(t, s, "ai")
	chain := &rulingChain{Chain: s.chain, ruling: ruling}
	s.chain = chain
	concern := "the key would have been rotated at 16:00 and so useless at 17:15"
	if w := do(t, s, "POST", "/flag/ai", `{"unit":"`+unit+`","item":"`+items[0]+`","text":"`+concern+`","idk":true}`, ""); w.Code != http.StatusOK {
		t.Fatalf("flag -> %d %s", w.Code, w.Body)
	}
	w := do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","unit":"`+unit+`","check_responses":[`+idkAnswers(items)+`]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("exchange -> %d %s", w.Code, w.Body)
	}
	var g gradedCheck
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	return s, chain, items, g
}

func verdictOf(g gradedCheck, item string) string {
	for _, r := range g.Results {
		if r.ItemID == item {
			return r.Verdict
		}
	}
	return ""
}

func flaggedEntry(t *testing.T, g gradedCheck) (ruling string, upheld bool) {
	t.Helper()
	for _, e := range g.ResultsDoc.Entries {
		if e.Flag != "" {
			return e.FlagRuling, e.FlagUpheld
		}
	}
	t.Fatal("no flagged entry on the results")
	return "", false
}

// The grader rules on the flag as it grades the check. Upheld, the answer
// counts: the item passes and the score moves with it; the reader reads
// the grader's word under their flag.
func TestAnUpheldFlagCountsTheAnswer(t *testing.T) {
	s, chain, items, g := flagAndSubmit(t, map[string]any{"upheld": true, "reply": "Right: the key was rotated at 16:00."})
	if len(chain.asked) != 1 || !strings.Contains(chain.asked[0], "rotated at 16:00") || !strings.Contains(chain.asked[0], "I don't know") {
		t.Fatalf("the grader was asked %d times: %q", len(chain.asked), chain.asked)
	}
	if v := verdictOf(g, items[0]); v != "pass" {
		t.Errorf("the flagged item is %q, want pass", v)
	}
	if v := verdictOf(g, items[1]); v != "fail" {
		t.Errorf("an unflagged item is %q, want fail", v)
	}
	if g.Gate.Score == nil || *g.Gate.Score <= 0 {
		t.Errorf("the score did not move: %v", g.Gate.Score)
	}
	ruling, upheld := flaggedEntry(t, g)
	if !upheld || ruling != "Right: the key was rotated at 16:00." {
		t.Errorf("entry ruling %q upheld %v", ruling, upheld)
	}
	sub, _ := s.subject("ai")
	if sub.Learner.UnruledFlag(items[0]) != nil {
		t.Error("the flag is ruled, and is not ruled again")
	}
}

// Not upheld, the grade stands; the reader still hears why.
func TestARejectedFlagLeavesTheGrade(t *testing.T) {
	_, _, items, g := flagAndSubmit(t, map[string]any{"upheld": false, "reply": "The key is rotated at 18:00, not 16:00."})
	if v := verdictOf(g, items[0]); v != "fail" {
		t.Errorf("the flagged item is %q, want fail", v)
	}
	if g.Gate.Score == nil || *g.Gate.Score != 0 {
		t.Errorf("score %v, want 0", g.Gate.Score)
	}
	ruling, upheld := flaggedEntry(t, g)
	if upheld || ruling != "The key is rotated at 18:00, not 16:00." {
		t.Errorf("entry ruling %q upheld %v", ruling, upheld)
	}
}

// With no model to rule, the grade stands and the flag waits for one.
func TestAFlagWithNoRulingWaits(t *testing.T) {
	s, _, items, g := flagAndSubmit(t, nil)
	if v := verdictOf(g, items[0]); v != "fail" {
		t.Errorf("the flagged item is %q, want fail", v)
	}
	if ruling, _ := flaggedEntry(t, g); ruling != "" {
		t.Errorf("a ruling nobody made: %q", ruling)
	}
	sub, _ := s.subject("ai")
	if sub.Learner.UnruledFlag(items[0]) == nil {
		t.Error("the flag still waits for a ruling")
	}
}
