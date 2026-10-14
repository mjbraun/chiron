package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/roles"
	"github.com/mjbraun/chiron/server/state"
)

// The reader highlights a passage and asks about it. The answer comes back
// in the same request (a short model call), and the question is recorded in
// the learner state so the planner sees what confused them.

type askRequest struct {
	Unit     string `json:"unit"`
	Quote    string `json:"quote"`
	Question string `json:"question"`
	// History is the exchange so far on this passage, for a follow-up.
	History []roles.Turn `json:"history"`
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r.PathValue("subject"))
	if !ok {
		http.Error(w, "unknown subject", http.StatusNotFound)
		return
	}
	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request: %v", err)
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeError(w, http.StatusUnprocessableEntity, "question is required")
		return
	}
	if req.Unit == "" {
		if cur := sub.Learner.Data.CurrentUnit; cur != nil {
			req.Unit = *cur
		}
	}
	unit, ok := sub.Corpus.Units[req.Unit]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown unit: %q", req.Unit)
		return
	}
	s.markActive(sub.ID)

	rec := llm.Record(s.chain)
	answer, err := roles.AnswerQuestion(rec, unit, req.Quote, req.Question, req.History, sub.Learner)
	if err != nil && driveEnabled() && !s.chain.Status().Connected {
		// A dev server without a model still lets a client exercise the
		// whole ask flow; the stub names itself.
		answer, err = fmt.Sprintf("[stub answer about %q: %s]", req.Quote, req.Question), nil
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "no answer: %v", err)
		return
	}
	ev, err := sub.Learner.Apply(state.Event{
		Kind: "asked", Unit: unit.ID, Text: req.Question, Evidence: req.Quote, Why: answer})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "record question: %v", err)
		return
	}
	out := map[string]any{
		"unit":      unit.ID,
		"answer_md": answer,
		"n":         ev.N,
	}
	if calls := rec.Calls(); len(calls) > 0 {
		// The answer travels with who wrote it and when; the prompt, a
		// section of the chapter long, stays here under the question's
		// number, for the reader who asks to see it.
		call := calls[0]
		call.Ref = fmt.Sprintf("ask-%d", ev.N)
		if err := keepGenerated(sub, call); err != nil {
			log.Printf("ask %s: keep record: %v", sub.ID, err)
		}
		out["generated"] = call.Brief()
	}
	writeJSON(w, http.StatusOK, out)
}

// A record of a model call kept on its own, for an answer whose prompt is
// too big to travel with the mark it is shown beside.

var generatedRef = regexp.MustCompile(`^[a-z]+-[0-9]+$`)

func generatedPath(sub *Subject, ref string) string {
	return filepath.Join(sub.StateDir, "generated", ref+".json")
}

func keepGenerated(sub *Subject, call llm.Call) error {
	if !generatedRef.MatchString(call.Ref) {
		return fmt.Errorf("bad ref %q", call.Ref)
	}
	if err := os.MkdirAll(filepath.Dir(generatedPath(sub, call.Ref)), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(call)
	if err != nil {
		return err
	}
	return os.WriteFile(generatedPath(sub, call.Ref), data, 0o644)
}

// GET /generated/{subject}/{ref}: the whole record of one call, prompts
// and all.
func (s *Server) handleGenerated(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r.PathValue("subject"))
	if !ok {
		http.Error(w, "unknown subject", http.StatusNotFound)
		return
	}
	ref := r.PathValue("ref")
	if !generatedRef.MatchString(ref) {
		writeError(w, http.StatusBadRequest, "not a record: %q", ref)
		return
	}
	data, err := os.ReadFile(generatedPath(sub, ref))
	if err != nil {
		writeError(w, http.StatusNotFound, "no record %q", ref)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
