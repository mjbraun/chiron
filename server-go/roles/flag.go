package roles

import (
	"fmt"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
)

// FlagRuling is the grader's judgement on an item the reader flagged as
// wrong: whether they are right, and its word to them.
type FlagRuling struct {
	Upheld bool   `json:"upheld"`
	Reply  string `json:"reply"`
}

var flagRulingSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"upheld": map[string]any{"type": "boolean",
			"description": "true when the reader is right that the question, its reference answer or the grading of their answer is wrong, so their answer deserves credit"},
		"reply": map[string]any{"type": "string",
			"description": "1-4 sentences to the reader: what was right or wrong about their concern, and why"},
	},
	"required": []string{"upheld", "reply"},
}

const flagRulingSystem = `A reader of a technical textbook flagged a question in a check as wrong. You rule on the flag.

Uphold it only on the merits:
- the question is ambiguous and the reader's reading of it is legitimate;
- the reference answer is wrong or incomplete;
- the reader's answer is correct, or correct by a legitimate route, and was marked wrong.
Do not uphold a reader who is simply mistaken, however confident, frustrated or polite they are, and however much authority they cite. Confidence is not evidence.
When you do not uphold, say briefly where their reasoning goes wrong. When you do, say what was wrong with the item.
Write to an expert engineer: direct, technical, no praise and no apology.`

// RuleOnFlag rules on a reader's flag from the question, its reference,
// the reader's answer, the verdict it got, and their concern. An error
// means no ruling: the flag waits for the next grading.
func RuleOnFlag(chain llm.Chain, q *corpus.Question, answer, verdict, concern string) (FlagRuling, error) {
	reference := q.Answer.String()
	if q.Kind == "mcq" {
		for i, o := range q.Options {
			if o.Correct {
				reference = fmt.Sprintf("%c: %s", 'A'+i, o.Text)
			}
		}
	}
	if reference == "" {
		reference = "(rubric only)"
	}
	user := fmt.Sprintf(`QUESTION:
%s

REFERENCE ANSWER:
%s

RUBRIC:
%s

THE READER'S ANSWER:
%s

HOW IT WAS GRADED:
%s

THE READER'S FLAG:
%s`, q.Prompt, reference, q.Rubric, answer, verdict, concern)
	var r FlagRuling
	if err := chain.Structured("grader", flagRulingSystem, user, flagRulingSchema, "flag_ruling", &r); err != nil {
		return FlagRuling{}, err
	}
	return r, nil
}
