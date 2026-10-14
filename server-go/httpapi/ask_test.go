package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/llm"
)

// cannedChain answers every structured call with the same payload and keeps
// the last prompt.
type cannedChain struct {
	payload map[string]any
	user    string
}

func (c *cannedChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	c.user = user
	data, _ := json.Marshal(c.payload)
	return json.Unmarshal(data, out)
}
func (cannedChain) Status() llm.Status { return llm.Status{Connected: true, Model: "canned-model"} }

// The reader highlights a passage, asks, and gets an answer in the same
// request; the question lands in the learner state and marks the book open.
func TestAskAnswersFromTheChapterAndRecordsTheQuestion(t *testing.T) {
	s := newTwoSubjectServer(t, t.TempDir())
	chain := &cannedChain{payload: map[string]any{"answer_md": "Because $\\ln$ is what the code computes; divide by $\\ln 2$ for bits."}}
	s.chain = chain
	do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start"}`, "")

	body := `{"unit":"u1","quote":"the loss is this same quantity in natural-log units","question":"why nats and not bits?"}`
	w := do(t, s, "POST", "/ask/ai", body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("/ask/ai -> %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Unit     string `json:"unit"`
		AnswerMD string `json:"answer_md"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Unit != "u1" || !strings.Contains(resp.AnswerMD, "divide by") {
		t.Fatalf("answer = %+v", resp)
	}
	if !strings.Contains(chain.user, "why nats and not bits?") || !strings.Contains(chain.user, "natural-log units") {
		t.Errorf("model prompt lacks the question or the quote:\n%s", chain.user)
	}
	sub, _ := s.subject("ai")
	qs := sub.Learner.Questions("u1")
	if len(qs) != 1 || qs[0].Question != "why nats and not bits?" || !strings.Contains(qs[0].Answer, "divide by") {
		t.Fatalf("recorded questions = %+v", qs)
	}
	if got := activeSubjectOf(t, s); got != "ai" {
		t.Fatalf("active = %q, want ai", got)
	}
}

func TestAskRejectsWhatItCannotAnswer(t *testing.T) {
	s := newServer(t, "")
	s.chain = &cannedChain{payload: map[string]any{"answer_md": "x"}}
	if w := do(t, s, "POST", "/ask/ai", `{"unit":"u1","quote":"q","question":"   "}`, ""); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty question -> %d, want 422", w.Code)
	}
	if w := do(t, s, "POST", "/ask/ai", `{"unit":"u99","quote":"q","question":"why?"}`, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown unit -> %d, want 404", w.Code)
	}
	if w := do(t, s, "POST", "/ask/nope", `{"unit":"u1","quote":"q","question":"why?"}`, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown subject -> %d, want 404", w.Code)
	}
	// No model: a 502, and nothing recorded.
	s.chain = &capturingFail{}
	if w := do(t, s, "POST", "/ask/ai", `{"unit":"u1","quote":"q","question":"why?"}`, ""); w.Code != http.StatusBadGateway {
		t.Errorf("dead model -> %d, want 502", w.Code)
	}
	sub, _ := s.subject("ai")
	if len(sub.Learner.Questions("")) != 0 {
		t.Error("an unanswered question was recorded")
	}
}

type capturingFail struct{}

func (capturingFail) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	return llm.Errorf("dead")
}
func (capturingFail) Status() llm.Status { return llm.Status{} }

// A dev server with no model answers with a stub, so the client's ask flow
// can be walked end to end; a real server never does.
func TestAskStubsWithoutAModelInDriveMode(t *testing.T) {
	t.Setenv("CHIRON_DRIVE", "1")
	s := newServer(t, "")
	s.chain = &capturingFail{}
	w := do(t, s, "POST", "/ask/ai", `{"unit":"u1","quote":"the loss","question":"why?"}`, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "stub answer") {
		t.Fatalf("drive-mode ask -> %d: %s", w.Code, w.Body.String())
	}
	t.Setenv("CHIRON_DRIVE", "")
	if w := do(t, s, "POST", "/ask/ai", `{"unit":"u1","quote":"the loss","question":"why?"}`, ""); w.Code != http.StatusBadGateway {
		t.Fatalf("outside drive mode -> %d, want 502", w.Code)
	}
}
