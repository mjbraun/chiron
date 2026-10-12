package roles

import (
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/render"
	"github.com/mjbraun/chiron/server/state"
)

type renderSection = render.AssembledSection

func fixtures(t *testing.T) (*corpus.Corpus, *state.Learner) {
	t.Helper()
	c, err := corpus.Load(filepath.Join("..", "..", "corpus"))
	if err != nil {
		t.Fatalf("corpus: %v", err)
	}
	l, err := state.Open(t.TempDir(), c)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	return c, l
}

// deadChain stands in for an unreachable model.
type deadChain struct{}

func (deadChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	return llm.Errorf("no upstream reachable")
}
func (deadChain) Status() llm.Status { return llm.Status{} }

// A check below eight items is statistical noise for an 80% gate, so the
// composer must always fill the quota even when the callback pool is empty.
func TestComposeCheckAlwaysFillsTheQuota(t *testing.T) {
	c, l := fixtures(t)
	rng := rand.New(rand.NewSource(7))
	items := ComposeCheck(c.Units["u0"], l, c, 9, 0.4, rng)
	if len(items) != 9 {
		t.Errorf("got %d items on a first unit with no callback pool, want 9", len(items))
	}
	seen := map[string]bool{}
	for _, q := range items {
		if seen[q.ID] {
			t.Errorf("item %s appears twice in one check", q.ID)
		}
		seen[q.ID] = true
		if q.Unit == "" {
			t.Errorf("item %s has no unit attribution", q.ID)
		}
	}
}

// Response congruency: the goal is to derive and explain, so the check
// takes the unit's constructed items before its recognition items. The
// bank decides how many there are (the spec asks for about half MCQ);
// the check's job is to leave none of them out while the quota allows.
func TestComposeCheckPrefersConstructedItems(t *testing.T) {
	c, l := fixtures(t)
	rng := rand.New(rand.NewSource(3))
	unit := c.Units["u2"]
	inBank := 0
	for _, q := range unit.Questions.Check {
		if q.Kind == "constructed" {
			inBank++
		}
	}
	if inBank == 0 {
		t.Fatal("u2's bank has no constructed item to prefer")
	}
	n, callback := 9, 0.4
	items := ComposeCheck(unit, l, c, n, callback, rng)
	constructed := 0
	for _, q := range items {
		if q.Kind == "constructed" && q.Unit == "u2" {
			constructed++
		}
	}
	if want := min(inBank, n-int(float64(n)*callback)); constructed < want {
		t.Errorf("%d of u2's %d constructed items in the check, want %d", constructed, inBank, want)
	}
}

// Once prior units are cleared, the check must actually reach back into them -
// that is what makes it cumulative rather than a unit quiz.
func TestComposeCheckDrawsCallbacksFromClearedUnits(t *testing.T) {
	c, l := fixtures(t)
	score, passed := 1.0, true
	for _, uid := range []string{"u0", "u1"} {
		if _, err := l.Apply(state.Event{Kind: "check_result", Unit: uid,
			Score: &score, Passed: &passed}); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewSource(11))
	items := ComposeCheck(c.Units["u2"], l, c, 9, 0.4, rng)
	fromPrior := 0
	for _, q := range items {
		if q.Unit != "u2" {
			fromPrior++
		}
	}
	if fromPrior == 0 {
		t.Error("no callbacks drawn from cleared units - the check is not cumulative")
	}
}

// With no measured evidence there is nothing to adapt to, so the first chapter
// must not wait on a model call.
func TestPlannerColdStartsWithoutCallingTheModel(t *testing.T) {
	c, l := fixtures(t)
	d := PlanDirectives(deadChain{}, l, c.Units["u0"], "")
	if d.Depth != "default" {
		t.Errorf("cold start depth %q", d.Depth)
	}
	if d.NextAction == "" {
		t.Error("every output must carry a concrete next action")
	}
	if len(d.SectionsToRewrite) != 0 {
		t.Error("nothing to rewrite before any evidence exists")
	}
}

// An unreachable model must degrade to canon, never to a broken chapter.
func TestAuthorFallsBackToCanonWhenTheModelIsDown(t *testing.T) {
	c, _ := fixtures(t)
	unit := c.Units["u1"]
	d := Directives{
		Depth: "default",
		SectionsToRewrite: []SectionRewrite{
			{Heading: unit.Sections[0].Heading, Instruction: "make it simpler"},
		},
	}
	sections, used := AuthorChapter(deadChain{}, unit, d, c)
	if used {
		t.Error("reported an LLM rewrite when the model was unreachable")
	}
	if len(sections) != len(unit.Sections) {
		t.Errorf("got %d sections, want %d", len(sections), len(unit.Sections))
	}
	for i, s := range sections {
		if !strings.HasPrefix(s.Markdown, "## ") {
			t.Errorf("section %d lost its heading", i)
		}
	}
}

// The beats are the teaching. A depth swap replaces prose, so it must re-append
// the canonical beats rather than dropping them.
func TestDepthSwapKeepsBeats(t *testing.T) {
	c, _ := fixtures(t)
	for _, id := range c.UnitOrder() {
		unit := c.Units[id]
		for depth := range unit.Depths {
			sections, _ := AuthorChapter(deadChain{}, unit, Directives{Depth: depth}, c)
			joined := strings.Join(markdowns(sections), "\n")
			for _, b := range unit.Beats() {
				if !strings.Contains(joined, "id: "+b.ID) {
					t.Errorf("%s at depth %q lost beat %s", id, depth, b.ID)
				}
			}
		}
	}
}

func markdowns(ss []renderSection) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Markdown)
	}
	return out
}

// Grading must never invent a verdict when it could not reach a model - an
// unassessed item goes to the debt ledger instead.
func TestGradeDegradesToUngraded(t *testing.T) {
	c, _ := fixtures(t)
	q, _ := c.FindQuestion("u1-q1")
	if q == nil {
		t.Fatal("fixture question missing")
	}
	g := GradeFreeText(deadChain{}, q, "some answer", c.Misconceptions)
	if g.Verdict != "ungraded" {
		t.Errorf("verdict %q - a guess is worse than admitting the gap", g.Verdict)
	}
	if g.NextAction == "" {
		t.Error("even a degraded grade must tell the learner what to do")
	}
}

// A turn that neither asks a question nor finishes leaves the learner with
// nothing to reply to and the conversation stalled.
type stallingChain struct{}

func (stallingChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	e := out.(*Elicitation)
	e.ReplyMD = "I am planning a book on jet engines for you."
	e.Done = false
	return nil
}
func (stallingChain) Status() llm.Status { return llm.Status{} }

func TestElicitTurnNeverEndsWithNothingToAnswer(t *testing.T) {
	got := ElicitTurn(stallingChain{}, []Message{{Role: "learner", Text: "teach me jet engines"}})
	if got.Done {
		t.Fatal("fixture should not be done")
	}
	if !strings.Contains(got.ReplyMD, "?") {
		t.Errorf("an unfinished turn must end in a question, got %q", got.ReplyMD)
	}
}

// capturingChain records the planner prompt and then fails like a dead
// model, so the fallback path stays exercised.
type capturingChain struct{ user string }

func (c *capturingChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	c.user = user
	return llm.Errorf("captured")
}
func (capturingChain) Status() llm.Status { return llm.Status{} }

// The self-rating was recorded in the state and never shown to the planner:
// after calibration every later unit was planned as if the learner had
// never been asked where they stood.
func TestPlannerSeesTheSelfRating(t *testing.T) {
	c, l := fixtures(t)
	sc := 1.0
	if _, err := l.Apply(state.Event{Kind: "self_rating", Score: &sc}); err != nil {
		t.Fatal(err)
	}
	chain := &capturingChain{}
	PlanDirectives(chain, l, c.Units["u1"], "Calibration u0: self-rated level 1 of 5; 40% overall. ")
	if !strings.Contains(chain.user, "SELF-RATED START: level 1 of 5") {
		t.Errorf("planner prompt lacks the self-rating:\n%s", chain.user)
	}
	chain = &capturingChain{}
	_, l2 := fixtures(t)
	PlanDirectives(chain, l2, c.Units["u1"], "Check u1: 50% (below gate). ")
	if !strings.Contains(chain.user, "SELF-RATED START: (not asked)") {
		t.Errorf("unrated learner should be marked as not asked:\n%s", chain.user)
	}
}

// The answer is grounded in the section the quote came from, not the whole
// unit, and the reader's question travels verbatim.
func TestAnswerIsGroundedInTheQuotedSection(t *testing.T) {
	c, l := fixtures(t)
	unit := c.Units["u1"]
	var quoteSection string
	for _, s := range unit.Sections {
		if strings.Contains(s.Heading, "Tokens") {
			quoteSection = s.Markdown()
		}
	}
	if quoteSection == "" {
		t.Fatal("no tokens section in u1")
	}
	// A quote as the rendered page would give it: rewrapped whitespace.
	quote := "Frequent words become single symbols, rare words decompose into pieces"
	chain := &capturingChain{}
	if _, err := AnswerQuestion(chain, unit, quote, "why not just use words?", nil, l); err == nil {
		t.Fatal("captured chain should fail")
	}
	if !strings.Contains(chain.user, "why not just use words?") {
		t.Errorf("question missing from prompt:\n%s", chain.user)
	}
	if !strings.Contains(chain.user, "Tokens: BPE from scratch") {
		t.Errorf("the quoted section is not the context:\n%.400s", chain.user)
	}
	if strings.Contains(chain.user, "Embeddings are coordinates") {
		t.Errorf("unrelated sections leaked into the context")
	}

	// A quote nothing matches falls back to the whole unit.
	chain = &capturingChain{}
	AnswerQuestion(chain, unit, "text the author rewrote entirely", "what?", nil, l)
	if !strings.Contains(chain.user, "Embeddings are coordinates") || !strings.Contains(chain.user, "Tokens: BPE") {
		t.Errorf("fallback should carry the whole unit")
	}
}

// A question asked while reading is a confusion the check may never surface;
// the planner sees the recent ones.
func TestPlannerSeesRecentQuestions(t *testing.T) {
	c, l := fixtures(t)
	if _, err := l.Apply(state.Event{Kind: "asked", Unit: "u1", Text: "why is the loss in nats?",
		Evidence: "the loss is this same quantity in natural-log units", Why: "..."}); err != nil {
		t.Fatal(err)
	}
	chain := &capturingChain{}
	PlanDirectives(chain, l, c.Units["u2"], "Check u1: 50% (below gate). ")
	if !strings.Contains(chain.user, "why is the loss in nats?") || !strings.Contains(chain.user, "[u1]") {
		t.Errorf("planner prompt lacks the reader's question:\n%s", chain.user)
	}
	chain = &capturingChain{}
	_, l2 := fixtures(t)
	PlanDirectives(chain, l2, c.Units["u2"], "Check u1: 50% (below gate). ")
	if !strings.Contains(chain.user, "QUESTIONS THE READER ASKED WHILE READING (newest last):\n(none)") {
		t.Errorf("no questions should read as none:\n%s", chain.user)
	}
}

// A follow-up carries the exchange so far, so the tutor can build on it.
func TestAFollowUpCarriesTheThread(t *testing.T) {
	c, err := corpus.Load(filepath.Join("..", "..", "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	unit := c.Units["u1"]
	l, _ := state.Open(t.TempDir(), c)
	chain := &primerChain{payload: map[string]any{"answer_md": "Building on that: subwords."}}
	history := []Turn{{Question: "why tokens?", Answer: "Because words are open-ended."}}
	got, err := AnswerQuestion(chain, unit, "predict the next token", "and why subwords?", history, l)
	if err != nil || got != "Building on that: subwords." {
		t.Fatalf("%q %v", got, err)
	}
	for _, want := range []string{"EARLIER IN THIS EXCHANGE", "Reader: why tokens?", "Tutor: Because words are open-ended.", "READER'S QUESTION:\nand why subwords?"} {
		if !strings.Contains(chain.user, want) {
			t.Errorf("prompt lacks %q:\n%s", want, chain.user)
		}
	}
}

// Every prompt that writes for the reader carries the acronym rule.
func TestPromptsThatWriteForTheReaderExpandAcronyms(t *testing.T) {
	for name, system := range map[string]string{
		"author": authorSystem, "answer": answerSystem, "primer": primerSystem,
		"extend": extendSystem, "capture": captureAnswerSystem,
	} {
		if !strings.Contains(system, "acronym") {
			t.Errorf("%s prompt says nothing about acronyms", name)
		}
	}
}

// A reader does not see unit ids: "u1" in a planner note or an answer means
// nothing to them (Matt, 2026-10-12, on the Agent Auth book). Every role
// whose words reach the reader is told to name a unit by its title.
func TestRolesNameUnitsByTitleNotID(t *testing.T) {
	for name, system := range map[string]string{
		"planner": plannerSystem,
		"author":  authorSystem,
		"answer":  answerSystem,
		"repair":  repairSystem,
	} {
		if !strings.Contains(system, unitNameRule) || !strings.Contains(unitNameRule, "never by its id") {
			t.Errorf("%s prompt does not tell the model to name units by title", name)
		}
	}
}
