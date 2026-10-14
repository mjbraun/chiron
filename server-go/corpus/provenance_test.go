package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

// A unit a model wrote keeps the record of the calls that wrote it beside
// its files, and the loader carries it on the unit: the chapter built
// from the unit can then say who wrote it and show the prompts.
func TestAUnitLoadsItsProvenance(t *testing.T) {
	dir := t.TempDir()
	udir := filepath.Join(dir, "units", "u1-tokens")
	if err := os.MkdirAll(udir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(dir, "syllabus.yaml"):           "title: T\nunits:\n  - id: u1\n    slug: tokens\n    title: Tokens\n",
		filepath.Join(dir, "misconception-bank.yaml"): "misconceptions: []\n",
		filepath.Join(udir, "canon.md"):               "Intro.\n\n## One\n\nText.\n",
		filepath.Join(udir, "provenance.json"): `[{"role":"author","name":"canon_md","model":"claude-fable-5-1","system":"S","prompt":"P","at":"2026-10-14T13:00:00Z","seconds":90.5},
{"role":"author","name":"questions_yaml","model":"claude-fable-5-1","at":"2026-10-14T13:02:00Z"}]`,
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
	if len(u.Provenance) != 2 || u.Provenance[0].Name != "canon_md" || u.Provenance[0].Prompt != "P" || u.Provenance[1].At.Hour() != 13 {
		t.Fatalf("provenance = %+v", u.Provenance)
	}

	// A unit a person wrote has none; a record that does not parse is a
	// load error, since a half-read record would misname the writer.
	os.Remove(filepath.Join(udir, "provenance.json"))
	c, err = Load(dir)
	if err != nil || c.Units["u1"].Provenance != nil {
		t.Fatalf("without the file: err=%v provenance=%v", err, c.Units["u1"].Provenance)
	}
	os.WriteFile(filepath.Join(udir, "provenance.json"), []byte("{not json"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("a broken record loaded silently")
	}
}
