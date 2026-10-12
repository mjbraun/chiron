package corpus

import (
	"hash/fnv"
	"math/rand"

	"github.com/mjbraun/chiron/server/checkers"
)

// Shuffled puts an item's options in an order fixed by the item's id, so
// the correct option is not the first one every author wrote first, and
// so every parse of the item, for delivery or for grading, agrees on the
// order without the file changing. A screener keeps its authored order,
// which runs novice to expert.
func Shuffled(id string, opts []checkers.Option) []checkers.Option {
	if len(opts) < 2 {
		return opts
	}
	h := fnv.New64a()
	h.Write([]byte(id))
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	out := make([]checkers.Option, len(opts))
	copy(out, opts)
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func (q *Question) shuffleOptions() {
	if q.Check == "screener" {
		return
	}
	q.Options = Shuffled(q.ID, q.Options)
}

// deriveKind fills in the kind of an item written without one: options
// under `check: choice` make it a multiple choice question, whatever the
// section. Every consumer tests Kind, so the derivation happens once,
// here, rather than at each of them.
func (q *Question) deriveKind() {
	if q.Kind == "" && q.Check == "choice" && len(q.Options) > 0 {
		q.Kind = "mcq"
		q.KindImplied = true
	}
}

// Authored returns the bank as it was written: a kind the loader derived
// is cleared again, so what depends on the authored text (a stored
// chapter's fingerprint) sees no change when the derivation does.
func (f QuestionFile) Authored() QuestionFile {
	strip := func(qs []Question) []Question {
		if qs == nil {
			return nil
		}
		out := make([]Question, len(qs))
		copy(out, qs)
		for i := range out {
			if out[i].KindImplied {
				out[i].Kind = ""
				out[i].KindImplied = false
			}
		}
		return out
	}
	f.Pretest = strip(f.Pretest)
	f.Check = strip(f.Check)
	return f
}
