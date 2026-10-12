package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

// A pretest item is written without a kind (the spec's schema has none);
// its options and `check: choice` make it a choice. Loaded as a prose item
// it reached the iPad as a text box with no answer behind it (2026-10-12,
// the Agent Auth u3 pretest).
func TestOptionsWithoutAKindLoadAsAChoice(t *testing.T) {
	dir := t.TempDir()
	udir := filepath.Join(dir, "units", "u1-tokens")
	if err := os.MkdirAll(udir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(dir, "syllabus.yaml"):           "title: T\nunits:\n  - id: u1\n    slug: tokens\n    title: Tokens\n",
		filepath.Join(dir, "misconception-bank.yaml"): "misconceptions:\n  - id: M6\n    name: Signed means secret\n",
		filepath.Join(udir, "canon.md"):               "Intro.\n\n## One\n\nText.\n",
		filepath.Join(udir, "questions.yaml"): `pretest:
  - id: u1-p1
    concept: c-jwt
    prompt: What does the signature protect?
    check: choice
    options:
      - text: Integrity only.
        correct: true
        explain: The claims are readable by anyone.
      - text: Confidentiality.
        misconception: M6
        explain: Base64url is not encryption.
  - id: u1-p2
    concept: c-jwt
    prompt: How many segments?
    answer: 3
    check: numeric(0)
check:
  - id: u1-q1
    concept: c-jwt
    kind: mcq
    prompt: Pick one.
    check: choice
    options:
      - text: A
        correct: true
        explain: a
      - text: B
        misconception: M6
        explain: b
`,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	u := c.Units["u1"]
	if u == nil {
		t.Fatal("unit not loaded")
	}
	p1 := u.Questions.Pretest[0]
	if p1.Kind != "mcq" || !p1.KindImplied {
		t.Errorf("u1-p1: kind %q implied %v, want mcq, implied", p1.Kind, p1.KindImplied)
	}
	if p2 := u.Questions.Pretest[1]; p2.Kind != "" || p2.KindImplied {
		t.Errorf("u1-p2: kind %q implied %v, want none", p2.Kind, p2.KindImplied)
	}
	if q1 := u.Questions.Check[0]; q1.Kind != "mcq" || q1.KindImplied {
		t.Errorf("u1-q1: kind %q implied %v, want mcq, authored", q1.Kind, q1.KindImplied)
	}
}
