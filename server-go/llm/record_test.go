package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type namedChain struct {
	model string
	fail  bool
}

func (c namedChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	if c.fail {
		return Errorf("down")
	}
	return json.Unmarshal([]byte(`{"ok":true}`), out)
}
func (c namedChain) Status() Status { return Status{Connected: true, Model: c.model} }

// A recorder wrapped around a chain keeps, for every call that produced
// something, what was asked of which model and when; a failed call is not
// a thing the reader sees and is not kept.
func TestARecorderKeepsEveryCallThatProduced(t *testing.T) {
	rec := Record(namedChain{model: "claude-fable-5-1"})
	var out map[string]any
	before := time.Now().UTC()
	if err := rec.Structured("author", "SYS", "write the chapter", nil, "rewrite", &out); err != nil {
		t.Fatal(err)
	}
	if err := rec.Structured("planner", "SYS2", "plan it", nil, "directives", &out); err != nil {
		t.Fatal(err)
	}
	calls := rec.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	c := calls[0]
	if c.Role != "author" || c.Name != "rewrite" || c.Model != "claude-fable-5-1" || c.System != "SYS" || c.Prompt != "write the chapter" {
		t.Fatalf("call = %+v", c)
	}
	if c.At.Before(before.Truncate(time.Second)) || c.At.Location() != time.UTC {
		t.Fatalf("at = %v, want a UTC time at or after %v", c.At, before)
	}
	if calls[1].Role != "planner" {
		t.Fatalf("second call = %+v", calls[1])
	}
	if rec.Status().Model != "claude-fable-5-1" {
		t.Fatal("status does not pass through")
	}

	down := Record(namedChain{model: "x", fail: true})
	if err := down.Structured("author", "s", "u", nil, "n", &out); err == nil || len(down.Calls()) != 0 {
		t.Fatalf("a failed call was kept: err=%v calls=%d", err, len(down.Calls()))
	}
}

// Models names each model once, in order of first use; a call with no
// model known is left out.
func TestModelsNamesEachOnce(t *testing.T) {
	calls := []Call{{Model: "a"}, {Model: ""}, {Model: "b"}, {Model: "a"}}
	if got := strings.Join(Models(calls), ","); got != "a,b" {
		t.Fatalf("models = %q", got)
	}
}

// Brief is the call without its prompts: what rides along with a mark or
// a shelf row, the prompts fetched when the reader asks for them.
func TestBriefDropsThePrompts(t *testing.T) {
	c := Call{Role: "planner", Name: "answer", Model: "m", System: "s", Prompt: "p", At: time.Now(), Seconds: 1.5}
	b := c.Brief()
	if b.System != "" || b.Prompt != "" || b.Model != "m" || b.Role != "planner" || b.Seconds != 1.5 {
		t.Fatalf("brief = %+v", b)
	}
}
