package corpus

import (
	"encoding/json"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/mjbraun/chiron/server/checkers"
)

// Scalar is a corpus value that is textual to every consumer but which authors
// naturally write as a bare number ("answer: 6").
//
// This exists because of a real outage: one numeric answer in one unit made the
// entire 11-chapter offline bundle fail to decode on the iPad, and the app
// silently reported "No bundled book found". Anything reaching a client as text
// must be text at the payload boundary, whatever the YAML said.
type Scalar string

func (s *Scalar) UnmarshalYAML(node *yaml.Node) error {
	var raw any
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*s = Scalar(stringify(raw))
	return nil
}

func (s *Scalar) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = Scalar(stringify(raw))
	return nil
}

// MarshalJSON always emits a JSON string, never a bare number.
func (s Scalar) MarshalJSON() ([]byte, error) { return json.Marshal(string(s)) }

func (s Scalar) String() string { return string(s) }

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		// Keep integral floats looking like integers: "6", not "6".
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// Reveal is what the app shows after the learner commits an answer.
type Reveal struct {
	Answer  Scalar         `yaml:"answer" json:"answer,omitempty"`
	Rubric  string         `yaml:"rubric" json:"rubric,omitempty"`
	Options []RevealOption `yaml:"options" json:"options,omitempty"`
}

type RevealOption struct {
	Text          string `yaml:"text" json:"text"`
	Correct       bool   `yaml:"correct" json:"correct"`
	Explain       string `yaml:"explain" json:"explain,omitempty"`
	Misconception string `yaml:"misconception" json:"misconception,omitempty"`
}

// Question is a pretest or check item.
type Question struct {
	ID      string `yaml:"id" json:"id"`
	Unit    string `yaml:"unit" json:"unit,omitempty"`
	Concept string `yaml:"concept" json:"concept,omitempty"`
	Kind    string `yaml:"kind" json:"kind"` // constructed | mcq
	// KindImplied says the loader derived Kind from the item's shape (a
	// pretest item has no kind in the schema; its options and `check:
	// choice` make it a choice). A derived kind is not part of the bank
	// as authored, so a chapter's fingerprint leaves it out.
	KindImplied bool   `yaml:"-" json:"-"`
	Prompt      string `yaml:"prompt" json:"prompt"`
	Check       string `yaml:"check" json:"check"`
	Answer      Scalar `yaml:"answer" json:"-"`
	Rubric      string `yaml:"rubric" json:"-"`
	Difficulty  string `yaml:"difficulty" json:"difficulty,omitempty"`
	// Band places a calibration item on the screener's own 1-5 scale, so a
	// level's series can be a window around the level rather than a ladder
	// that only ever trims from the top.
	Band             int               `yaml:"band" json:"-"`
	Congruent        bool              `yaml:"congruent" json:"-"`
	CallbackEligible bool              `yaml:"callback_eligible" json:"-"`
	Options          []checkers.Option `yaml:"options" json:"-"`
	// Source names the open text an imported item came from: the source
	// id, the locator and the exercise number.
	Source string `yaml:"source,omitempty" json:"-"`
}

// Beat is one in-chapter interaction. Step-based interaction is where the
// tutoring effect lives, so these are first-class, not decoration.
type Beat struct {
	ID      string `yaml:"id" json:"id"`
	Type    string `yaml:"type" json:"type"` // predict | completion | self-explain | compute
	Concept string `yaml:"concept" json:"concept,omitempty"`
	Prompt  string `yaml:"prompt" json:"prompt"`
	Answer  Scalar `yaml:"answer" json:"answer,omitempty"`
	Rubric  string `yaml:"rubric" json:"rubric,omitempty"`
	Check   string `yaml:"check" json:"check,omitempty"`
	// Options make a beat a choice, answered by a tap; reveals travel with
	// the options, since a beat's reveal is a learning tool, not a secret.
	Options []checkers.Option `yaml:"options,omitempty" json:"options,omitempty"`
}

// Misconception is a bank entry: a wrong model plus the specific prediction it
// makes that observably fails, which is what a chapter uses to break it.
type Misconception struct {
	ID                string   `yaml:"id" json:"id"`
	Name              string   `yaml:"name" json:"name"`
	WrongModel        string   `yaml:"wrong_model" json:"wrong_model"`
	WhyAppealing      string   `yaml:"why_appealing" json:"why_appealing"`
	FailingPrediction string   `yaml:"failing_prediction" json:"failing_prediction"`
	Correction        string   `yaml:"correction" json:"correction"`
	Units             []string `yaml:"units" json:"units,omitempty"`
}

type ConceptRef struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
}

// UnitSpec is a syllabus entry.
type UnitSpec struct {
	ID       string       `yaml:"id"`
	Slug     string       `yaml:"slug"`
	Title    string       `yaml:"title"`
	Minutes  int          `yaml:"minutes"`
	Prereqs  []string     `yaml:"prereqs"`
	Concepts []ConceptRef `yaml:"concepts"`
	Notes    string       `yaml:"notes"`
}

type Syllabus struct {
	Title   string `yaml:"title"`
	Learner struct {
		Name    string `yaml:"name"`
		Profile string `yaml:"profile"`
		Session string `yaml:"session"`
	} `yaml:"learner"`
	Units      []UnitSpec `yaml:"units"`
	Extensions []struct {
		ID    string `yaml:"id"`
		Title string `yaml:"title"`
	} `yaml:"extensions"`
}

type QuestionFile struct {
	Pretest []Question `yaml:"pretest"`
	Check   []Question `yaml:"check"`
	// Screener is a single self-placement question a calibration unit asks
	// first; the answer picks one of the CalibrationSets.
	Screener *Question `yaml:"screener"`
	// CalibrationSets maps a self-rating (1..5) to the ordered item ids of
	// the series for that level - pre-computed, not derived at runtime.
	CalibrationSets map[int][]string `yaml:"calibration_sets"`
}
