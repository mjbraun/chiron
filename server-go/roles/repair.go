package roles

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/render"
)

// Miss is one check item the reader got wrong, with what they answered and
// what the grader said about it: the whole brief for teaching that one
// thing again.
type Miss struct {
	Unit     string
	Question corpus.Question
	// ReadAs is a constructed answer as it was read; Chose the option
	// picked on a choice item. Either may be empty.
	ReadAs string
	Chose  string
	// Why is the grader's or the option's explanation of the miss.
	Why string
	IDK bool
}

const repairSystem = `You write a short repair chapter for one learner who has just failed the check at the end of a textbook chapter. You receive the questions they missed, what they answered, the reference answer, why it was wrong, and the canonical text those questions draw on.

Write one section per missed question, in the order given. Each section teaches the exact thing the miss shows was not understood, and nothing else: no recap of the chapter, no material the learner did not miss. The learner will answer the same questions again right after reading, so the section must make the answer derivable without stating the question or quoting the reference answer.

Hard constraints:
- Switch representation from the canonical text (symbolic <-> numeric <-> code <-> geometric, or a worked example where the text argued in prose). Never re-explain the same way slower.
- Where the learner's answer shows a specific wrong model, name it, show the prediction it makes that fails, then give the right model.
- 150-400 words per section. Real equations with symbols defined inline. Worked numbers use tiny shapes that compute cleanly.
- Do not include any ` + "```beat" + ` fenced block.
- Audience: expert software engineer, novice at ML math. Direct tone, no filler, no exclamation marks. Hyphens only, no em dashes.` + acronymRule + `
Return the sections as JSON: a "sections" array of {heading, markdown}, the markdown starting with its "## " heading.`

var repairSchema = rewriteSchema

var beatBlock = regexp.MustCompile("(?s)```beat\n.*?```")

// coveringSections picks the sections of a unit that carry a beat on one of
// the concepts, in the unit's order. A unit with no beat on any of them
// gives all its sections: better the whole text as context than none.
func coveringSections(unit *corpus.Unit, concepts map[string]bool) []corpus.Section {
	var out []corpus.Section
	for _, sec := range unit.Sections {
		for _, seg := range sec.Segments {
			if seg.Type == "beat" && concepts[seg.Beat.Concept] {
				out = append(out, sec)
				break
			}
		}
	}
	if len(out) == 0 {
		return unit.Sections
	}
	return out
}

// switchedVariant is the depth variant a repair falls back to when no model
// can write one: any representation but the one the reader just failed on.
func switchedVariant(unit *corpus.Unit) string {
	for _, v := range []string{"more-intuition", "se-analogies", "deeper-math"} {
		if _, ok := unit.Depths[v]; ok {
			return v
		}
	}
	return ""
}

// AuthorRepair writes the short chapter that takes the misses alone: one
// section per missed item, teaching what that miss shows, from a different
// angle than the text the reader just failed on. Without a model it hands
// back the sections of the units that cover the missed concepts, in a
// depth variant where one exists, so the reader always gets a chapter. The
// bool says whether the model's sections were used.
func AuthorRepair(chain llm.Chain, c *corpus.Corpus, misses []Miss) ([]render.AssembledSection, bool) {
	// Units in order of first miss, and the concepts missed in each.
	var unitOrder []string
	concepts := map[string]map[string]bool{}
	for _, m := range misses {
		if _, ok := concepts[m.Unit]; !ok {
			unitOrder = append(unitOrder, m.Unit)
			concepts[m.Unit] = map[string]bool{}
		}
		concepts[m.Unit][m.Question.Concept] = true
	}

	var fallback []render.AssembledSection
	var context []string
	for _, uid := range unitOrder {
		unit, ok := c.Units[uid]
		if !ok {
			continue
		}
		variant := switchedVariant(unit)
		for _, sec := range coveringSections(unit, concepts[uid]) {
			md := sec.Markdown()
			context = append(context, beatBlock.ReplaceAllString(md, ""))
			if variant != "" {
				if text, ok := unit.Depths[variant][sec.Heading]; ok && len(text) > minVariantChars {
					md = "## " + sec.Heading + "\n\n" + text
					for _, seg := range sec.Segments {
						if seg.Type == "beat" {
							md += "\n\n" + seg.Markdown()
						}
					}
				}
			}
			fallback = append(fallback, render.AssembledSection{Heading: sec.Heading, Markdown: md})
		}
	}
	if len(fallback) == 0 {
		return nil, false
	}

	var briefs []string
	for i, m := range misses {
		q := m.Question
		var b strings.Builder
		fmt.Fprintf(&b, "MISS %d (%s, concept %s)\nQUESTION: %s\n", i+1, q.ID, q.Concept, strings.TrimSpace(q.Prompt))
		switch {
		case m.IDK:
			b.WriteString("THE LEARNER ANSWERED: marked \"I don't know\"\n")
		case m.Chose != "":
			fmt.Fprintf(&b, "THE LEARNER CHOSE: %s\n", m.Chose)
		case m.ReadAs != "":
			fmt.Fprintf(&b, "THE LEARNER ANSWERED: %s\n", m.ReadAs)
		}
		if q.Kind == "mcq" {
			for _, o := range q.Options {
				if o.Correct {
					fmt.Fprintf(&b, "REFERENCE: %s\n", o.Text)
				}
			}
		} else if a := q.Answer.String(); a != "" {
			fmt.Fprintf(&b, "REFERENCE: %s\n", a)
		}
		if q.Rubric != "" {
			fmt.Fprintf(&b, "RUBRIC: %s\n", strings.TrimSpace(q.Rubric))
		}
		if m.Why != "" {
			fmt.Fprintf(&b, "WHY IT WAS WRONG: %s\n", strings.TrimSpace(m.Why))
		}
		briefs = append(briefs, b.String())
	}
	user := fmt.Sprintf("MISSED QUESTIONS:\n%s\nCANONICAL TEXT THE QUESTIONS DRAW ON (the representation to switch away from):\n%s",
		strings.Join(briefs, "\n"), strings.Join(context, "\n\n"))

	var out struct {
		Sections []struct {
			Heading  string `json:"heading"`
			Markdown string `json:"markdown"`
		} `json:"sections"`
	}
	if err := chain.Structured("author", repairSystem, user, repairSchema, "repair", &out); err != nil {
		return fallback, false
	}
	var sections []render.AssembledSection
	for _, s := range out.Sections {
		md := strings.TrimSpace(s.Markdown)
		heading := strings.TrimSpace(s.Heading)
		if heading == "" || len(md) < 80 || strings.Contains(md, "```beat") {
			// A section that lost its heading, says nothing, or invents an
			// interaction the app cannot grade: the whole answer is
			// discarded, the reader gets the assembled text instead.
			return fallback, false
		}
		if !strings.HasPrefix(md, "## ") {
			md = "## " + heading + "\n\n" + md
		}
		sections = append(sections, render.AssembledSection{Heading: heading, Markdown: md})
	}
	if len(sections) == 0 {
		return fallback, false
	}
	return sections, true
}
