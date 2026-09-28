package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/state"
)

// The reader thinks a question, its reference answer or its grading is
// wrong, and says so from the check. Nothing is graded differently: the
// concern is recorded against the item in the learner state, with the
// answer they gave and the reference as it stands, so the results show it
// beside the item and a person can find it later (GET /flags/{subject},
// or `flagged` in events.jsonl).

type flagRequest struct {
	Unit string `json:"unit"`
	Item string `json:"item"`
	// Text is the concern, in the reader's words.
	Text string `json:"text"`
	// What they answered, whichever way: typed, chosen, or passed on.
	Answer        string `json:"answer"`
	SelectedIndex *int   `json:"selected_index"`
	IDK           bool   `json:"idk"`
}

// POST /flag/{subject} {unit, item, text, answer | selected_index | idk}
func (s *Server) handleFlag(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r.PathValue("subject"))
	if !ok {
		http.Error(w, "unknown subject", http.StatusNotFound)
		return
	}
	var req flagRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request: %v", err)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "a flag says what is wrong")
		return
	}
	q, unit := sub.Corpus.FindQuestion(req.Item)
	if q == nil {
		writeError(w, http.StatusNotFound, "unknown item: %q", req.Item)
		return
	}
	if req.Unit != "" {
		unit = req.Unit
	}
	s.markActive(sub.ID)

	answer := strings.TrimSpace(req.Answer)
	switch {
	case req.IDK:
		answer = "I don't know."
	case q.Kind == "mcq" && req.SelectedIndex != nil:
		answer = chosenOption(q, req.SelectedIndex)
	}
	ev, err := sub.Learner.Apply(state.Event{
		Kind: "flagged", Unit: unit, Item: q.ID, Text: req.Text, Evidence: answer, Why: referenceAnswer(q)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "record flag: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"unit": unit,
		"item": q.ID,
		"n":    ev.N,
	})
}

// GET /flags/{subject}[?unit=]: what the reader flagged, oldest first.
func (s *Server) handleFlagList(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r.PathValue("subject"))
	if !ok {
		http.Error(w, "unknown subject", http.StatusNotFound)
		return
	}
	flags := sub.Learner.Flags(r.URL.Query().Get("unit"))
	if flags == nil {
		flags = []state.Flag{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": flags})
}

// chosenOption prints a choice the way the results page does: the letter
// and the option's text, or nothing for an index outside the options.
func chosenOption(q *corpus.Question, idx *int) string {
	if idx == nil || *idx < 0 || *idx >= len(q.Options) {
		return ""
	}
	return fmt.Sprintf("%c — '%s'", 'A'+*idx, q.Options[*idx].Text)
}

// referenceAnswer is the item's answer as the results page prints it: the
// correct option with its letter, or the reference text.
func referenceAnswer(q *corpus.Question) string {
	if q.Kind == "mcq" {
		for i, o := range q.Options {
			if o.Correct {
				return fmt.Sprintf("%c — %s", 'A'+i, o.Text)
			}
		}
		return ""
	}
	return q.Answer.String()
}
