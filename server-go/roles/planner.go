package roles

import (
	"fmt"
	"strings"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/state"
)

// SectionRewrite is one flagged section plus why it needs changing.
type SectionRewrite struct {
	Heading     string `json:"heading"`
	Instruction string `json:"instruction"`
}

// Directives are what the planner hands the author.
type Directives struct {
	Depth             string           `json:"depth"` // compressed | default | deeper-math | more-intuition
	SectionsToRewrite []SectionRewrite `json:"sections_to_rewrite"`
	RefutationTargets []string         `json:"refutation_targets"`
	OpeningNoteMD     string           `json:"opening_note_md"`
	Summary           string           `json:"summary"`
	NextAction        string           `json:"next_action"`
}

var directiveSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"depth": map[string]any{
			"type": "string",
			"enum": []string{"compressed", "default", "deeper-math", "more-intuition"},
		},
		"sections_to_rewrite": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"heading": map[string]any{"type": "string"},
					"instruction": map[string]any{"type": "string",
						"description": "one sentence: what to change and why, tied to learner evidence"},
				},
				"required": []string{"heading", "instruction"},
			},
		},
		"refutation_targets": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "misconception IDs to attack explicitly in this chapter"},
		"opening_note_md": map[string]any{"type": "string",
			"description": "2-4 sentences addressed to the learner: what the last check showed and what this chapter will do about it. Specific, not cheerleading."},
		"summary": map[string]any{"type": "string",
			"description": "3-6 sentence prose learner-state summary, sufficient for a fresh model to predict how this learner does on the next check"},
		"next_action": map[string]any{"type": "string"},
	},
	"required": []string{"depth", "sections_to_rewrite", "refutation_targets",
		"opening_note_md", "summary", "next_action"},
}

const plannerSystem = `You are the curriculum planner for an adaptive textbook teaching an expert software engineer how LLMs work, down to the math. You receive the learner's measured state and the next unit's outline; you emit directives for the chapter author.

Principles:
- Depth and directness are parameters, not personality. High mastery -> compress and raise difficulty. Struggle -> switch representation (symbolic <-> numeric <-> code <-> geometric), never re-explain the same way slower.
- The learner is expert in code/systems, novice in matrix calculus. Math gets full treatment; code gets one line. Expertise reversal is real: redundant explanation of known material actively hurts.
- Active misconceptions get named and refuted in the chapter body, not politely re-taught around.
- Fatigue evidence means lighter representation and a break suggestion, NOT a mastery downgrade.
- sections_to_rewrite: flag ONLY sections needing change from the default text (target 0-3). An empty list is a good answer when the default fits.` + unitNameRule

const defaultNextAction = "Read the chapter and work every beat before its reveal."

// PlanDirectives chooses depth, refutation targets and any rewrites.
// selfRatingLine carries the learner's own placement into every planning
// call, not just the one after calibration. The claim and the calibration's
// per-band record together are what tell a planner how much first-principles
// re-derivation this learner needs.
func selfRatingLine(rating int) string {
	if rating <= 0 {
		return "(not asked)"
	}
	return fmt.Sprintf("level %d of 5 (1 = absolute novice, 3 = can work through it slowly, "+
		"5 = expert); the calibration's per-band record says whether the claim held", rating)
}

func PlanDirectives(chain llm.Chain, l *state.Learner, unit *corpus.Unit, checkSummary string) Directives {
	active := l.ActiveMisconceptions()
	snapshot := l.Snapshot()

	targets := active
	if len(targets) > 3 {
		targets = targets[:3]
	}
	fallback := Directives{
		Depth:             "default",
		SectionsToRewrite: []SectionRewrite{},
		RefutationTargets: targets,
		Summary:           snapshot.Summary,
		NextAction:        defaultNextAction,
	}

	// Cold start: no measured evidence yet, so there is nothing for a planner
	// to adapt to. Default directives, no model call, instant first chapter.
	if checkSummary == "" && len(active) == 0 && len(l.OpenDebt()) == 0 {
		return fallback
	}

	var headings []string
	for _, s := range unit.Sections {
		headings = append(headings, "- "+s.Heading)
	}
	var depthNames []string
	for name := range unit.Depths {
		depthNames = append(depthNames, name)
	}
	sortStrings(depthNames)
	depths := strings.Join(depthNames, ", ")
	if depths == "" {
		depths = "(none)"
	}
	var debtUnits []string
	for _, d := range l.OpenDebt() {
		debtUnits = append(debtUnits, d.Unit)
	}
	var conceptLevels []string
	for _, c := range unit.Concepts {
		conceptLevels = append(conceptLevels, fmt.Sprintf("%s (%s)", c.ID, l.ConceptLevel(c.ID)))
	}
	lastCheck := checkSummary
	if lastCheck == "" {
		lastCheck = "(first unit - pretest evidence only)"
	}

	user := fmt.Sprintf(`LEARNER STATE SUMMARY:
%s

ACTIVE MISCONCEPTIONS: %s
FRAGILE CONCEPTS (recently shaky/low-confidence): %s
OPEN DEBT: %s
SESSION: %.0f min elapsed, fatigue_flag=%t
SELF-RATED START: %s

QUESTIONS THE READER ASKED WHILE READING (newest last):
%s

LAST CHECK:
%s

NEXT UNIT: %s %q (%d min budget)
Unit notes: %s
Sections:
%s
Available depth variants: %s
Concepts: %s`,
		snapshot.Summary,
		orNone(strings.Join(active, ", ")),
		orNone(strings.Join(l.FragileConcepts(), ", ")),
		orNone(strings.Join(debtUnits, ", ")),
		l.SessionMinutes(), snapshot.Pacing.FatigueFlag,
		selfRatingLine(snapshot.Profile.SelfRating),
		recentQuestions(l, 6),
		lastCheck,
		unit.ID, unit.Title, unit.Minutes,
		orNone(unit.Notes),
		strings.Join(headings, "\n"),
		depths,
		strings.Join(conceptLevels, ", "))

	var out Directives
	if err := chain.Structured("planner", plannerSystem, user, directiveSchema, "directives", &out); err != nil {
		return fallback
	}

	// Sanitise: only real headings survive, and degenerate text fields fall
	// back rather than propagating junk into the chapter.
	known := map[string]bool{}
	for _, s := range unit.Sections {
		known[s.Heading] = true
	}
	kept := make([]SectionRewrite, 0, len(out.SectionsToRewrite))
	for _, s := range out.SectionsToRewrite {
		if known[s.Heading] && len(kept) < 3 {
			kept = append(kept, s)
		}
	}
	out.SectionsToRewrite = kept
	if len(strings.TrimSpace(out.Summary)) < 20 {
		out.Summary = snapshot.Summary
	}
	if len(strings.TrimSpace(out.NextAction)) < 10 {
		out.NextAction = defaultNextAction
	}
	if out.Depth == "" {
		out.Depth = "default"
	}
	return out
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

// recentQuestions lists the last n things the reader asked about passages,
// with what they highlighted: a question is a confusion the check may never
// surface, and the next chapter should answer it before it is asked again.
func recentQuestions(l *state.Learner, n int) string {
	qs := l.Questions("")
	if len(qs) == 0 {
		return "(none)"
	}
	if len(qs) > n {
		qs = qs[len(qs)-n:]
	}
	var lines []string
	for _, q := range qs {
		quote := strings.Join(strings.Fields(q.Quote), " ")
		if len(quote) > 120 {
			quote = quote[:120] + "..."
		}
		lines = append(lines, fmt.Sprintf("- [%s] on %q: %s", q.Unit, quote, q.Question))
	}
	return strings.Join(lines, "\n")
}
