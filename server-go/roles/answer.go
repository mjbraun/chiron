package roles

import (
	"fmt"
	"strings"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/state"
)

// The reader highlighted a passage and asked about it. The answer is grounded
// in the section the passage came from, so the tutor and the book never
// disagree, and shaped by what the learner state says they already hold.

var answerSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"answer_md": map[string]any{"type": "string",
			"description": "the answer, in markdown with $...$ math, addressed to the reader"},
	},
	"required": []string{"answer_md"},
}

const answerSystem = `You are the tutor behind a textbook the reader has open right now. They highlighted a passage and asked a question about it. Answer the question they asked, and only that.

Rules:
- The chapter section you are given is the ground truth. Answer from it; extend it only when the question needs more, and never contradict it.
- Answer directly, in the first sentence. No preamble, no restating the passage, no praise for the question.
- Prose must chain: each sentence opens on something the previous one put on the table. Refer to nothing by a name the reader has not met.
- Under 200 words unless a derivation is genuinely needed. Real equations in $...$ with every symbol defined at first use. Hyphens only, no em dashes.
- If the question shows a wrong model, state the wrong model in one clause and then the right one - do not lecture.
- Never reveal answers to the chapter's check questions. The reader is mid-chapter and the check is closed-book.
- A follow-up continues the exchange you are shown: build on what you said, do not repeat it.
- Audience: expert software engineer, novice at ML math.` + acronymRule + unitNameRule

// Turn is one earlier question and answer on the same passage: a reader
// who keeps chatting gets a tutor who remembers what it just said.
type Turn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// AnswerQuestion answers a reader's question about a quoted passage of unit.
// history holds the exchange so far on that passage, oldest first.
func AnswerQuestion(chain llm.Chain, unit *corpus.Unit, quote, question string, history []Turn, l *state.Learner) (string, error) {
	section := sectionContaining(unit, quote)
	snapshot := l.Snapshot()
	active := l.ActiveMisconceptions()

	var thread strings.Builder
	if len(history) > 0 {
		thread.WriteString("EARLIER IN THIS EXCHANGE, ON THE SAME PASSAGE (oldest first):\n")
		for _, t := range history {
			fmt.Fprintf(&thread, "Reader: %s\nTutor: %s\n\n", strings.TrimSpace(t.Question), clip(strings.TrimSpace(t.Answer), 4000))
		}
	}

	user := fmt.Sprintf(`UNIT: %s %q

SECTION THE PASSAGE IS FROM:
%s

PASSAGE THE READER HIGHLIGHTED:
%q

%sREADER'S QUESTION:
%s

READER: self-rated start %s; active misconceptions: %s; state summary: %s`,
		unit.ID, unit.Title,
		clip(section, 60000),
		quote,
		thread.String(),
		question,
		selfRatingLine(snapshot.Profile.SelfRating),
		orNone(strings.Join(active, ", ")),
		orNone(snapshot.Summary))

	var out struct {
		AnswerMD string `json:"answer_md"`
	}
	if err := chain.Structured("planner", answerSystem, user, answerSchema, "answer", &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.AnswerMD) == "" {
		return "", llm.Errorf("empty answer")
	}
	return out.AnswerMD, nil
}

// sectionContaining finds the section whose prose holds the quote, comparing
// with whitespace collapsed since the rendered page rewraps lines. Falls
// back to the whole unit when nothing matches (a quote spanning sections, or
// text the author rewrote).
func sectionContaining(unit *corpus.Unit, quote string) string {
	needle := collapse(quote)
	if len(needle) >= 12 {
		for _, s := range unit.Sections {
			md := s.Markdown()
			if strings.Contains(collapse(md), needle) {
				return md
			}
		}
	}
	var b strings.Builder
	for _, s := range unit.Sections {
		b.WriteString(s.Markdown())
		b.WriteString("\n\n")
	}
	return b.String()
}

func collapse(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[...]"
}
