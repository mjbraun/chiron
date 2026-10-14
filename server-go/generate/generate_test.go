package generate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/sources"
)

// A chain that answers each role with a canned payload and keeps what it
// was asked, so the prompts can be checked.
type stubChain struct {
	payloads map[string]map[string]any
	asked    map[string][]string
	systems  map[string][]string
}

func (c *stubChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	if c.asked == nil {
		c.asked = map[string][]string{}
		c.systems = map[string][]string{}
	}
	c.asked[role] = append(c.asked[role], user)
	c.systems[role] = append(c.systems[role], system)
	payload := c.payloads[role]
	if role == "author" {
		// The author is asked for one file at a time; answer with the key it wants.
		for key := range schema["properties"].(map[string]any) {
			payload = map[string]any{key: c.payloads["author"]["body"]}
		}
	}
	// Roles that share a chain role are told apart by their schema name.
	if p, ok := c.payloads[name]; ok {
		payload = p
	}
	data, _ := json.Marshal(payload)
	return json.Unmarshal(data, out)
}

func (stubChain) Status() llm.Status { return llm.Status{Connected: true, Model: "stub-model"} }

func TestNamesInABriefAreFound(t *testing.T) {
	got := NamedInBrief(`A short book on Bayesian statistics for an engineer, starting from "Think Bayes" by Allen Downey and interleaving content from MIT 18.05 Introduction to Probability and Statistics, with worked examples.`)
	want := []string{"Think Bayes", "MIT 18.05 Introduction to Probability and Statistics"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", got)
	}
	if got := NamedInBrief("Teach me hashing from first principles."); len(got) != 0 {
		t.Fatalf("got %v from a brief that names nothing", got)
	}
}

func TestFrontMatterGainsTheSources(t *testing.T) {
	provs := []sources.Provenance{{Source: "think-bayes", Title: "Think Bayes 2e", Authors: "Allen B. Downey", Licence: "CC BY-NC-SA 4.0", LicenceURL: "u", URL: "x", Fetched: "2026-09-05"}}
	canon := "---\nunit: u1\ntitle: Priors\n---\n\nProse.\n"
	got := withSources(canon, provs)
	if !strings.HasPrefix(got, "---\nunit: u1\ntitle: Priors\nsources:\n") || !strings.Contains(got, "  title: Think Bayes 2e\n") || !strings.HasSuffix(got, "---\n\nProse.\n") {
		t.Fatalf("got:\n%s", got)
	}
	// The author's own sources list gives way to the pipeline's.
	doubled := withSources("---\nunit: u1\nsources:\n  - title: The author's guess\n    licence: none\ntitle: Priors\n---\n\nProse.\n", provs)
	if strings.Count(doubled, "\nsources:\n") != 1 || strings.Contains(doubled, "author's guess") || !strings.Contains(doubled, "title: Priors\n") {
		t.Fatalf("doubled:\n%s", doubled)
	}
	bare := withSources("Prose only.\n", provs)
	if !strings.HasPrefix(bare, "---\nsources:\n") || !strings.HasSuffix(bare, "---\nProse only.\n") {
		t.Fatalf("bare:\n%s", bare)
	}
}

// The whole named-sources path: the brief names a book the index has;
// the planner sees its chapters and assigns them; the author gets the
// fetched text as material and the unit records where it came from.
func TestABookIsPlannedAndAuthoredFromANamedSource(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/repos/AllenDowney/ThinkBayes2/contents/notebooks":
			w.Write([]byte(`[{"name":"chap01.ipynb","type":"file"},{"name":"chap02.ipynb","type":"file"}]`))
		case "/AllenDowney/ThinkBayes2/master/notebooks/chap02.ipynb":
			w.Write([]byte(`{"cells":[{"cell_type":"markdown","source":"# Bayes's Theorem\n\nThe cookie problem, in Downey's words.\n\n## Exercises\n\n**Exercise:** Two coins, one fair. P(heads)?\n"},{"cell_type":"code","source":"# Solution\n\np = 0.75\n"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()

	dir := t.TempDir()
	spec := filepath.Join(dir, "authoring-spec.md")
	os.WriteFile(spec, []byte("# Contract\n\nWrite canon.md with front matter.\n"), 0o644)
	idx := &sources.Index{Sources: []sources.Source{{
		ID: "think-bayes", Title: "Think Bayes 2e", Authors: []string{"Allen B. Downey"}, Subjects: []string{"bayesian"},
		Verdict: sources.Adaptable, Licence: sources.Licence{Name: "CC BY-NC-SA 4.0", URL: "lic"}, Voice: "Conversational.",
		Fetch: sources.Recipe{Kind: "github", Base: host.URL, Repo: "AllenDowney/ThinkBayes2", Ref: "master", Path: "notebooks/{locator}", Dir: "notebooks"},
	}}}
	client := sources.NewClient(filepath.Join(dir, "cache"))
	client.Pause = 0
	chain := &stubChain{payloads: map[string]map[string]any{
		"planner": {
			"title": "Bayes for Engineers", "learner_profile": "an engineer",
			"units": []map[string]any{
				{"id": "u0", "slug": "bayes-theorem", "title": "Bayes's Theorem", "minutes": 20, "prereqs": []string{}, "concepts": []map[string]any{{"id": "c-bayes", "name": "Bayes's theorem"}}, "notes": "",
					"sources": []map[string]any{{"source": "think-bayes", "locator": "chap02.ipynb", "role": "spine"}, {"source": "think-bayes", "locator": "nope.ipynb", "role": "spine"}}},
				{"id": "u1", "slug": "from-scratch", "title": "Something Else", "minutes": 20, "prereqs": []string{"u0"}, "concepts": []map[string]any{{"id": "c-x", "name": "x"}}, "notes": "", "sources": []map[string]any{}},
			},
			"misconceptions": []map[string]any{},
		},
		"author":         {"body": "---\nunit: u0\ntitle: Bayes's Theorem\n---\n\n## The cookie problem\n\nIn Downey's words, adapted.\n"},
		"imported_items": {"items": []map[string]any{{"source": "think-bayes chap02.ipynb exercise 1", "yaml": "- id: u0-e1\n  concept: c-bayes\n  kind: constructed\n  prompt: Two coins, one fair. P(heads)?\n  answer: '0.75'\n  check: numeric(0.01)\n  difficulty: core\n"}}},
	}}
	g := &Generator{Chain: chain, SpecPath: spec, OutDir: filepath.Join(dir, "corpus-bayes"), Index: idx, Fetch: client, Workers: 1}

	total, err := g.Plan("A short book on Bayesian statistics, starting from Think Bayes.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d", total)
	}
	planPrompt := chain.asked["planner"][0]
	if !strings.Contains(planPrompt, "[spine] think-bayes") || !strings.Contains(planPrompt, "chap02.ipynb | Bayes's Theorem") {
		t.Fatalf("the planner did not see the spine's sections:\n%s", planPrompt)
	}
	if !strings.Contains(chain.systems["planner"][0], "follow the spine's order") {
		t.Fatal("the planner was not told to follow the spine")
	}
	var syllabus struct {
		Units []unitPlan `yaml:"units"`
	}
	raw, _ := os.ReadFile(filepath.Join(g.OutDir, "syllabus.yaml"))
	if err := yaml.Unmarshal(raw, &syllabus); err != nil {
		t.Fatal(err)
	}
	if len(syllabus.Units[0].Sources) != 1 || syllabus.Units[0].Sources[0].Locator != "chap02.ipynb" {
		t.Fatalf("a made-up locator should have been dropped: %+v", syllabus.Units[0].Sources)
	}
	if _, err := os.Stat(filepath.Join(g.OutDir, "sources.yaml")); err != nil {
		t.Fatal("sources.yaml was not written")
	}

	if failures, err := g.Units([]string{"u0"}, nil); err != nil || failures != 0 {
		t.Fatalf("units: failures=%d err=%v", failures, err)
	}
	authorPrompt := chain.asked["author"][0]
	if !strings.Contains(authorPrompt, "MATERIAL TO ADAPT") || !strings.Contains(authorPrompt, "The cookie problem, in Downey's words.") || !strings.Contains(authorPrompt, "=== spine: Think Bayes 2e") {
		t.Fatalf("the author did not get the material:\n%s", authorPrompt)
	}
	for _, later := range chain.asked["author"][1:] {
		if strings.Contains(later, "MATERIAL TO ADAPT") {
			t.Fatal("only the canon call carries the material")
		}
	}
	// The source's exercises were imported before the bank was written,
	// and the author wrote the bank around them.
	importPrompt := chain.asked["author"][1]
	if !strings.Contains(importPrompt, "think-bayes chap02.ipynb exercise 1") || !strings.Contains(importPrompt, "SOLUTION:\n```python\n# Solution\n\np = 0.75") {
		t.Fatalf("the importer did not see the exercise and its solution:\n%s", importPrompt)
	}
	questionsPrompt := chain.asked["author"][2]
	if !strings.Contains(questionsPrompt, "ITEMS IMPORTED") || !strings.Contains(questionsPrompt, "- id: u0-e1\n") || !strings.Contains(questionsPrompt, "source: think-bayes chap02.ipynb exercise 1") {
		t.Fatalf("the bank was not written around the imported items:\n%s", questionsPrompt)
	}
	unitDir := filepath.Join(g.OutDir, "units", "u0-bayes-theorem")
	canon, _ := os.ReadFile(filepath.Join(unitDir, "canon.md"))
	if !strings.Contains(string(canon), "sources:\n") || !strings.Contains(string(canon), "title: Think Bayes 2e") || !strings.Contains(string(canon), "licence: CC BY-NC-SA 4.0") {
		t.Fatalf("canon front matter lacks the sources:\n%s", canon)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "sources", "01-think-bayes-chap02-ipynb.provenance.yaml")); err != nil {
		t.Fatal("the chunk's provenance was not written beside the unit")
	}
	if imported, err := os.ReadFile(filepath.Join(unitDir, "imported.yaml")); err != nil || !strings.Contains(string(imported), "u0-e1") {
		t.Fatalf("imported.yaml: %v %q", err, imported)
	}
	// The calls that wrote the unit are on record beside its files: the
	// canon, the imported items, the bank, the misconceptions and the
	// depths, each naming the model and carrying its prompt.
	var record []llm.Call
	if raw, err := os.ReadFile(filepath.Join(unitDir, "provenance.json")); err != nil {
		t.Fatal("provenance.json was not written")
	} else if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range record {
		names = append(names, c.Name)
		if c.Model != "stub-model" || c.Role != "author" || c.At.IsZero() {
			t.Fatalf("call = %+v", c)
		}
	}
	if got := strings.Join(names, ","); !strings.HasPrefix(got, "canon_md,imported_items,questions_yaml,misconceptions_yaml,content") {
		t.Fatalf("recorded calls = %s", got)
	}
	if !strings.Contains(record[0].Prompt, "MATERIAL TO ADAPT") {
		t.Fatal("the canon call's prompt is not the one the author saw")
	}

	// A unit with no sources is authored from the brief, with no material block.
	chain.asked["author"] = nil
	if failures, err := g.Units([]string{"u1"}, nil); err != nil || failures != 0 {
		t.Fatalf("units: failures=%d err=%v", failures, err)
	}
	if strings.Contains(chain.asked["author"][0], "MATERIAL TO ADAPT") {
		t.Fatal("a sourceless unit got material")
	}
}

// A rerun keeps the syllabus it has and only writes what is missing.
func TestARerunKeepsTheSyllabus(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	os.WriteFile(spec, []byte("contract"), 0o644)
	out := filepath.Join(dir, "out")
	os.MkdirAll(out, 0o755)
	os.WriteFile(filepath.Join(out, "syllabus.yaml"), []byte("units:\n  - id: u0\n    slug: a\n    title: A\n  - id: u1\n    slug: b\n    title: B\n"), 0o644)
	chain := &stubChain{payloads: map[string]map[string]any{}}
	g := &Generator{Chain: chain, SpecPath: spec, OutDir: out}
	total, err := g.Plan("anything", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(chain.asked["planner"]) != 0 {
		t.Fatalf("total=%d planner calls=%d", total, len(chain.asked["planner"]))
	}
}

func TestAnUnknownNameIsRecordedNotFatal(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	os.WriteFile(spec, []byte("contract"), 0o644)
	chain := &stubChain{payloads: map[string]map[string]any{"planner": {
		"title": "T", "learner_profile": "p", "units": []map[string]any{{"id": "u0", "slug": "a", "title": "A", "minutes": 10, "prereqs": []string{}, "concepts": []map[string]any{}, "notes": "", "sources": []map[string]any{}}}, "misconceptions": []map[string]any{}}}}
	g := &Generator{Chain: chain, SpecPath: spec, OutDir: filepath.Join(dir, "out"), Index: &sources.Index{}, Fetch: sources.NewClient("")}
	if _, err := g.Plan("Starting from The Book Nobody Indexed.", "", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(g.OutDir, "sources.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "unresolved:") || !strings.Contains(string(raw), "- Book Nobody Indexed") {
		t.Fatalf("sources.yaml:\n%s", raw)
	}
}

func TestBareMCQsGetTheirCheck(t *testing.T) {
	in := "items:\n  - id: q1\n    kind: mcq\n    prompt: a\n    options:\n      - text: x\n        correct: true\n\n  - id: q2\n    kind: mcq\n    check: choice\n    prompt: b\n\n  - id: q3\n    kind: constructed\n    check: llm\n"
	got := withChoiceChecks(in)
	if strings.Count(got, "check: choice") != 2 || !strings.Contains(got, "kind: mcq\n    check: choice\n    prompt: a") {
		t.Fatalf("got:\n%s", got)
	}
	if withChoiceChecks(got) != got {
		t.Fatal("not idempotent")
	}
}

// A prompt written as a block scalar can hold a blank line between
// paragraphs. The scan for an existing check line must not mistake that
// blank line for the end of the item, or the author's own `check: choice`
// after the prompt gets a duplicate above it and the file no longer parses
// (u14-q8 of the first agent-auth build).
func TestAnMCQWithAParagraphBreakKeepsItsOneCheck(t *testing.T) {
	in := "items:\n  - id: q1\n    kind: mcq\n    prompt: |\n      First paragraph.\n\n      Second paragraph?\n    check: choice\n    options:\n      - text: x\n        correct: true\n"
	got := withChoiceChecks(in)
	if got != in {
		t.Fatalf("an MCQ that already has its check must be left alone, got:\n%s", got)
	}
}

// A brief that names nothing: the planner says what to search for, the
// index and the catalogues offer candidates with their contents, the
// planner picks, and the book is planned from the picks as if they had
// been named. A found source is fetchable on a rerun from sources.yaml.
func TestSourcesAreFoundWhenTheBriefNamesNone(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/repos/AllenDowney/ThinkBayes2/contents/notebooks":
			w.Write([]byte(`[{"name":"chap02.ipynb","type":"file"}]`))
		case "/AllenDowney/ThinkBayes2/master/notebooks/chap02.ipynb":
			w.Write([]byte(`{"cells":[{"cell_type":"markdown","source":"# Bayes's Theorem\n\nCookies.\n"}]}`))
		case "/otl/textbooks.json":
			w.Write([]byte(`{"data":[]}`))
		case "/learn/":
			w.Write([]byte(`{"results":[{"readable_id":"18.05+spring_2022","title":"Introduction to Probability and Statistics","url":"https://ocw.mit.edu/courses/18-05-introduction-to-probability-and-statistics-spring-2022/","description":"Probability.","course_feature":["Problem Sets","Problem Set Solutions"]}]}`))
		case "/lt/catalog":
			w.Write([]byte(`{"books":[]}`))
		case "/courses/18-05-introduction-to-probability-and-statistics-spring-2022/":
			w.Write([]byte(`<html><body><nav><a href="/courses/18-05-introduction-to-probability-and-statistics-spring-2022/pages/syllabus/">Syllabus</a><a href="/courses/18-05-introduction-to-probability-and-statistics-spring-2022/pages/readings/">Readings</a></nav></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()
	dir := t.TempDir()
	spec := filepath.Join(dir, "authoring-spec.md")
	os.WriteFile(spec, []byte("# Contract\n"), 0o644)
	idx := &sources.Index{Sources: []sources.Source{{
		ID: "think-bayes", Title: "Think Bayes 2e", Authors: []string{"Allen B. Downey"}, Subjects: []string{"bayesian", "probability"},
		Verdict: sources.Adaptable, Licence: sources.Licence{Name: "CC BY-NC-SA 4.0", URL: "lic"}, Voice: "Computation first.",
		Fetch: sources.Recipe{Kind: "github", Base: host.URL, Repo: "AllenDowney/ThinkBayes2", Ref: "master", Path: "notebooks/{locator}", Dir: "notebooks"},
	}, {
		ID: "organic-chem", Title: "Organic Chemistry", Subjects: []string{"chemistry"}, Verdict: sources.Adaptable,
	}}}
	client := sources.NewClient(filepath.Join(dir, "cache"))
	client.Pause = 0
	client.Catalogues = sources.Catalogues{OpenTextbookLibrary: host.URL + "/otl/textbooks.json", MITLearn: host.URL + "/learn/", LibreTexts: host.URL + "/lt/catalog"}
	chain := &stubChain{payloads: map[string]map[string]any{
		"search_terms": {"terms": []string{"bayesian statistics"}, "tags": []string{"bayesian", "not-a-tag"}},
		"source_picks": {"picks": []map[string]any{
			{"id": "ocw-18-05", "role": "interleave", "reason": "Problem sets with solutions."},
			{"id": "think-bayes", "role": "spine", "reason": "Computation first, fits an engineer."},
			{"id": "nope", "role": "interleave", "reason": "made up"},
		}, "references": []string{"Bayesian Data Analysis, 3rd edition"}},
		"planner": {"title": "Bayes", "learner_profile": "an engineer", "units": []map[string]any{
			{"id": "u0", "slug": "bayes", "title": "Bayes", "minutes": 20, "prereqs": []string{}, "concepts": []map[string]any{{"id": "c-b", "name": "b"}}, "notes": "",
				"sources": []map[string]any{{"source": "think-bayes", "locator": "chap02.ipynb", "role": "spine"}, {"source": "ocw-18-05", "locator": "readings", "role": "interleave"}}}},
			"misconceptions": []map[string]any{}},
	}}
	g := &Generator{Chain: chain, SpecPath: spec, OutDir: filepath.Join(dir, "out"), Index: idx, Fetch: client, Workers: 1}
	if _, err := g.Plan("A short book on Bayesian statistics for an engineer.", "", nil); err != nil {
		t.Fatal(err)
	}
	pickPrompt := chain.asked["planner"][1]
	if !strings.Contains(pickPrompt, "[index] think-bayes") || !strings.Contains(pickPrompt, "[MIT Learn] ocw-18-05") || strings.Contains(pickPrompt, "organic-chem") {
		t.Fatalf("the picker saw the wrong candidates:\n%s", pickPrompt)
	}
	if !strings.Contains(pickPrompt, "syllabus | Syllabus") || !strings.Contains(pickPrompt, "chap02.ipynb | Bayes's Theorem") {
		t.Fatalf("the picker did not see the tables of contents:\n%s", pickPrompt)
	}
	planPrompt := chain.asked["planner"][2]
	if !strings.Contains(planPrompt, "[spine] ocw-18-05") || !strings.Contains(planPrompt, "[interleave] think-bayes") {
		t.Fatalf("the planner did not get the picks in the picked order:\n%s", planPrompt)
	}
	raw, _ := os.ReadFile(filepath.Join(g.OutDir, "sources.yaml"))
	doc := string(raw)
	if !strings.Contains(doc, "id: ocw-18-05") || !strings.Contains(doc, "course: 18-05-introduction-to-probability-and-statistics-spring-2022") || !strings.Contains(doc, "Bayesian Data Analysis") || strings.Contains(doc, "nope") {
		t.Fatalf("sources.yaml:\n%s", doc)
	}
	// A fresh generator on the same directory can fetch the found source.
	again := &Generator{Chain: chain, SpecPath: spec, OutDir: g.OutDir, Index: idx, Fetch: client}
	again.loadFound()
	if s := again.source("ocw-18-05"); s == nil || s.Fetch.Course == "" {
		t.Fatalf("the found source was not read back: %+v", s)
	}
}

// A book written before placement units existed gets one: authored from
// the syllabus and the bank, put first, with the old first unit following.
func TestCalibrateGivesABookAPlacementUnit(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	os.WriteFile(spec, []byte("contract"), 0o644)
	out := filepath.Join(dir, "out")
	os.MkdirAll(out, 0o755)
	os.WriteFile(filepath.Join(out, "syllabus.yaml"), []byte("title: Bayes\nlearner:\n  profile: an engineer\nunits:\n  - id: u0\n    slug: cookies\n    title: Cookies\n    minutes: 30\n    prereqs: []\n    concepts:\n      - id: c-prior\n        name: Prior probability\n  - id: u1\n    slug: bayes\n    title: Bayes\n    minutes: 30\n    prereqs: [u0]\n    concepts: []\n"), 0o644)
	os.WriteFile(filepath.Join(out, "misconception-bank.yaml"), []byte("misconceptions: []\n"), 0o644)
	chain := &stubChain{payloads: map[string]map[string]any{
		"author": {"body": "---\nunit: cal\ntitle: Placement\ncalibration: true\n---\n\nA short placement.\n"},
	}}
	g := &Generator{Chain: chain, SpecPath: spec, OutDir: out, Workers: 1}
	id, err := g.Calibrate()
	if err != nil {
		t.Fatal(err)
	}
	if id != "cal" {
		t.Fatalf("id = %q", id)
	}
	prompt := chain.asked["author"][0]
	if !strings.Contains(prompt, "calibration: true") || !strings.Contains(prompt, "Prior probability") || !strings.Contains(prompt, "calibration_sets") {
		t.Fatalf("the author was not told what a placement unit is:\n%s", prompt)
	}
	if _, err := os.Stat(filepath.Join(out, "units", "cal-placement", "questions.yaml")); err != nil {
		t.Fatal("the unit was not written")
	}
	if entries, _ := os.ReadDir(filepath.Join(out, "units", "cal-placement", "depths")); len(entries) != 0 {
		t.Fatal("a placement unit has no depth variants")
	}
	raw, _ := os.ReadFile(filepath.Join(out, "syllabus.yaml"))
	var syl struct {
		Units []unitPlan `yaml:"units"`
	}
	if err := yaml.Unmarshal(raw, &syl); err != nil {
		t.Fatal(err)
	}
	if len(syl.Units) != 3 || syl.Units[0].ID != "cal" || !syl.Units[0].Calibration || syl.Units[1].ID != "u0" || strings.Join(syl.Units[1].Prereqs, ",") != "cal" || strings.Join(syl.Units[2].Prereqs, ",") != "u0" {
		t.Fatalf("syllabus after: %+v", syl.Units)
	}
	if _, err := g.Calibrate(); err == nil {
		t.Fatal("a second placement unit was allowed")
	}
}

// The author sometimes ends its front matter with a sources list of its
// own. Dropping it must not take the closing newline with it: eleven
// units of the first PDF-built book had "  - u2sources:" in their front
// matter, and lost their prerequisites to it.
func TestPipelineSourcesStartOnTheirOwnLine(t *testing.T) {
	canon := "---\nunit: u3\nprereqs:\n  - u2\nsources:\n  - private-debt\n---\n\nProse.\n"
	got := withSources(canon, []sources.Provenance{{Source: "private-debt", Title: "Private Debt"}})
	if !strings.Contains(got, "prereqs:\n  - u2\nsources:\n") || strings.Contains(got, "u2sources") {
		t.Errorf("front matter:\n%s", got)
	}
	if got := dropTopLevelKey("a: 1\nsources:\n  - x\n", "sources"); got != "a: 1\n" {
		t.Errorf("dropTopLevelKey = %q", got)
	}
}
