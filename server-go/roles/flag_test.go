package roles

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
)

type rulingChain struct {
	reply        map[string]any
	fail         bool
	system, user string
}

func (c *rulingChain) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	c.system, c.user = system, user
	if c.fail {
		return errors.New("no model")
	}
	data, _ := json.Marshal(c.reply)
	return json.Unmarshal(data, out)
}
func (rulingChain) Status() llm.Status { return llm.Status{Connected: true} }

// The grader rules on a reader's flag from everything the reader saw and
// said: the question, the reference, their answer, how it was graded, and
// their concern.
func TestRuleOnFlagSeesEverythingAndRules(t *testing.T) {
	c := &rulingChain{reply: map[string]any{"upheld": true, "reply": "You are right: the key was rotated at 16:00."}}
	q := &corpus.Question{Prompt: "When does the leaked key stop working?", Answer: corpus.Scalar("17:15")}
	r, err := RuleOnFlag(c, q, "16:00", "fail", "the key would have been rotated at 16:00")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Upheld || r.Reply != "You are right: the key was rotated at 16:00." {
		t.Errorf("ruling = %+v", r)
	}
	for _, want := range []string{"When does the leaked key stop working?", "17:15", "16:00", "fail", "rotated at 16:00"} {
		if !strings.Contains(c.user, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, c.user)
		}
	}
	if !strings.Contains(c.system, "confiden") {
		t.Error("the ruling must say a confident reader is not thereby right")
	}
}

func TestRuleOnFlagWithoutAModelSaysSo(t *testing.T) {
	_, err := RuleOnFlag(&rulingChain{fail: true}, &corpus.Question{Prompt: "p"}, "a", "fail", "c")
	if err == nil {
		t.Error("no ruling is an error, not a verdict")
	}
}
