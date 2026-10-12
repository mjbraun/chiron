package roles

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/render"
)

var rewriteSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"sections": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"heading":  map[string]any{"type": "string"},
					"markdown": map[string]any{"type": "string"},
				},
				"required": []string{"heading", "markdown"},
			},
		},
	},
	"required": []string{"sections"},
}

const authorSystem = `You rewrite specific sections of a textbook chapter for one specific learner. You receive the canonical section text, optional depth-variant text for the same section, and a one-sentence instruction from the planner.

Hard constraints:
- Preserve full technical coverage of the canonical section. You may change representation, depth, examples, and framing - never drop a concept.
- Keep every ` + "```beat" + ` fenced block byte-for-byte intact and in a sensible position.` + acronymRule + unitNameRule + `
- Refutation style when attacking a misconception: state the wrong model, give the specific prediction it makes that fails, say why it is appealing, then the correct model.
- Real equations with symbols defined inline at first use. Restate rather than cross-reference. Worked numbers use tiny shapes that compute cleanly.
- Audience: expert software engineer, novice at ML math. Math explained fully, code in one line. Direct tone, no filler, no exclamation marks. Hyphens only, no em dashes.
- Prose must chain. Each sentence opens on something the previous sentence put on the table and ends on the new thing; the next sentence picks that up. Never refer to a thing ("this game", "the argument") before the text has introduced it by that name. No sentence that only announces what the next sentences will do.
Return ONLY the rewritten sections you were asked to rewrite.`

// minVariantChars is the size below which a depth file's section is treated as
// a stub. Depth variants legitimately contain "nothing to add here" placeholders
// for sections that need no change at that depth, and swapping one in would
// replace real teaching with a note.
const minVariantChars = 400

type assembled struct {
	heading  string
	markdown string
	beatIDs  []string
}

// AuthorChapter assembles the chapter's sections.
//
// Code does the deterministic assembly - the depth-variant swap - and the model
// rewrites only the sections the planner flagged. If a rewrite mangles beats or
// headings, that section falls back to the code-assembled version: the reader
// always gets a chapter.
func AuthorChapter(chain llm.Chain, unit *corpus.Unit, d Directives,
	c *corpus.Corpus) ([]render.AssembledSection, bool) {
	depth := d.Depth
	if depth == "" {
		depth = "default"
	}

	work := make([]assembled, 0, len(unit.Sections))
	for _, s := range unit.Sections {
		md := s.Markdown()
		var beatIDs []string
		var beatBlocks []string
		for _, seg := range s.Segments {
			if seg.Type == "beat" {
				beatIDs = append(beatIDs, seg.Beat.ID)
				beatBlocks = append(beatBlocks, seg.Markdown())
			}
		}
		if variants, ok := unit.Depths[depth]; ok {
			if text, ok := variants[s.Heading]; ok && len(text) > minVariantChars {
				// Swap the prose, then re-append the canonical beats: the
				// interaction is the teaching, and a depth variant must not
				// silently drop it.
				md = "## " + s.Heading + "\n\n" + text
				for _, b := range beatBlocks {
					md += "\n\n" + b
				}
			}
		}
		work = append(work, assembled{heading: s.Heading, markdown: md, beatIDs: beatIDs})
	}

	if len(d.SectionsToRewrite) == 0 {
		return toSections(work), false
	}

	var targetLines []string
	ids := append([]string{}, d.RefutationTargets...)
	sort.Strings(ids)
	for _, mid := range ids {
		if m, ok := c.Misconceptions[mid]; ok {
			targetLines = append(targetLines, fmt.Sprintf("- %s: %s -> %s", mid, m.WrongModel, m.Correction))
		}
	}

	var blocks []string
	for _, r := range d.SectionsToRewrite {
		idx := -1
		for i := range work {
			if work[i].heading == r.Heading {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		var variants []string
		var names []string
		for name := range unit.Depths {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if text, ok := unit.Depths[name][r.Heading]; ok {
				variants = append(variants, fmt.Sprintf("[variant: %s]\n%s", name, text))
			}
		}
		block := fmt.Sprintf("SECTION: %s\nINSTRUCTION: %s\nCANONICAL TEXT:\n%s",
			work[idx].heading, r.Instruction, work[idx].markdown)
		if len(variants) > 0 {
			block += "\nDEPTH VARIANTS AVAILABLE:\n" + strings.Join(variants, "\n\n")
		}
		blocks = append(blocks, block)
	}

	user := fmt.Sprintf("LEARNER NOTE FROM PLANNER: %s\nMISCONCEPTIONS TO REFUTE WHERE RELEVANT:\n%s\n\n%s",
		d.OpeningNoteMD, orNone(strings.Join(targetLines, "\n")), strings.Join(blocks, "\n"))

	var out struct {
		Sections []struct {
			Heading  string `json:"heading"`
			Markdown string `json:"markdown"`
		} `json:"sections"`
	}
	if err := chain.Structured("author", authorSystem, user, rewriteSchema, "rewrite", &out); err != nil {
		return toSections(work), false
	}

	used := false
	for _, rewritten := range out.Sections {
		idx := -1
		for i := range work {
			if work[i].heading == rewritten.Heading {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		md := rewritten.Markdown
		if !strings.HasPrefix(strings.TrimLeft(md, " \t\n"), "## ") {
			md = "## " + work[idx].heading + "\n\n" + md
		}
		// Every beat id from the original section must survive intact, or the
		// rewrite is discarded. A chapter that lost its interaction is worse
		// than one that was not personalised.
		intact := true
		for _, bid := range work[idx].beatIDs {
			if !strings.Contains(md, "id: "+bid) {
				intact = false
				break
			}
		}
		if intact {
			work[idx].markdown = md
			used = true
		}
	}
	return toSections(work), used
}

func toSections(work []assembled) []render.AssembledSection {
	out := make([]render.AssembledSection, 0, len(work))
	for _, a := range work {
		out = append(out, render.AssembledSection{Heading: a.heading, Markdown: a.markdown})
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }
