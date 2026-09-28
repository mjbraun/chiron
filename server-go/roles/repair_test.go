package roles

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/llm"
)

// repairChain answers the author with fixed sections, or with junk.
type repairChain struct {
	sections []map[string]string
	user     string
}

func (c *repairChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	c.user = user
	data, _ := json.Marshal(map[string]any{"sections": c.sections})
	return json.Unmarshal(data, out)
}
func (*repairChain) Status() llm.Status { return llm.Status{Connected: true} }

func u1Misses(t *testing.T) []Miss {
	t.Helper()
	c, _ := fixtures(t)
	var misses []Miss
	for _, id := range []string{"u1-q3", "u1-q1"} {
		q, unit := c.FindQuestion(id)
		if q == nil {
			t.Fatalf("%s not in the bank", id)
		}
		misses = append(misses, Miss{Unit: unit, Question: *q, ReadAs: "seven", Why: "off by the vocabulary size"})
	}
	return misses
}

// The repair author gets the misses as a brief - the question, what the
// reader answered, the reference, why it was wrong - and the canonical
// sections on those concepts with their beats stripped; its sections come
// back as the chapter, one per miss, in the order of the misses.
func TestRepairAuthorSeesTheMissesAndWritesOneSectionEach(t *testing.T) {
	c, _ := fixtures(t)
	chain := &repairChain{sections: []map[string]string{
		{"heading": "Tokens, counted", "markdown": strings.Repeat("A token is a unit of the vocabulary, not a word. ", 5)},
		{"heading": "The objective, in numbers", "markdown": "## The objective, in numbers\n\n" + strings.Repeat("Take a vocabulary of three tokens. ", 5)},
	}}
	sections, used := AuthorRepair(chain, c, u1Misses(t))
	if !used {
		t.Fatal("the model's sections were not used")
	}
	if len(sections) != 2 || sections[0].Heading != "Tokens, counted" || sections[1].Heading != "The objective, in numbers" {
		t.Fatalf("sections: %+v", sections)
	}
	if !strings.HasPrefix(sections[0].Markdown, "## Tokens, counted\n") {
		t.Errorf("a section without its heading line must get one: %.60q", sections[0].Markdown)
	}
	for _, want := range []string{"MISS 1 (u1-q3, concept c-tokens)", "MISS 2 (u1-q1", "THE LEARNER ANSWERED: seven",
		"WHY IT WAS WRONG: off by the vocabulary size", "REFERENCE:", "CANONICAL TEXT"} {
		if !strings.Contains(chain.user, want) {
			t.Errorf("the brief lacks %q", want)
		}
	}
	if strings.Contains(chain.user, "```beat") {
		t.Error("the brief carries beat blocks; the author must not see them")
	}
	if !strings.Contains(chain.user, "## Tokens: BPE from scratch") {
		t.Error("the brief lacks the section that carries the missed concept")
	}
	if strings.Contains(chain.user, "## Notation in this unit") {
		t.Error("the brief carries a section on a concept the reader did not miss")
	}
}

// Without a model, or with an answer the app could not show, the reader
// still gets a chapter: the covering sections as they are.
func TestRepairFallsBackToTheCoveringSections(t *testing.T) {
	c, _ := fixtures(t)
	for name, chain := range map[string]llm.Chain{
		"dead":      deadChain{},
		"empty":     &repairChain{},
		"with beat": &repairChain{sections: []map[string]string{{"heading": "x", "markdown": strings.Repeat("y ", 60) + "```beat\nid: u1-b9\n```"}}},
		"headless":  &repairChain{sections: []map[string]string{{"heading": "", "markdown": strings.Repeat("y ", 60)}}},
	} {
		sections, used := AuthorRepair(chain, c, u1Misses(t))
		if used {
			t.Errorf("%s: the model's answer was used", name)
		}
		if len(sections) == 0 {
			t.Fatalf("%s: no sections at all", name)
		}
		var headings []string
		for _, s := range sections {
			headings = append(headings, s.Heading)
		}
		if !strings.Contains(strings.Join(headings, "|"), "Tokens: BPE from scratch") {
			t.Errorf("%s: fallback sections %v lack the one on tokens", name, headings)
		}
		if len(headings) >= len(c.Units["u1"].Sections) {
			t.Errorf("%s: the fallback is the whole chapter (%d sections)", name, len(headings))
		}
	}
}
