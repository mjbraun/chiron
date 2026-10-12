package httpapi

import (
	"testing"

	"github.com/mjbraun/chiron/server/checkers"
	"github.com/mjbraun/chiron/server/corpus"
)

// A kind the loader derived from an item's options is not a change to the
// bank: the stored chapter built before the loader derived it must keep
// its fingerprint, or every such chapter is dropped and authored again.
func TestBankHashIgnoresADerivedKind(t *testing.T) {
	unit := func(kind string, implied bool) *corpus.Unit {
		return &corpus.Unit{ID: "u1", Questions: corpus.QuestionFile{Pretest: []corpus.Question{{
			ID: "u1-p1", Prompt: "p", Check: "choice", Kind: kind, KindImplied: implied,
			Options: []checkers.Option{{Text: "a", Correct: true}, {Text: "b"}},
		}}}}
	}
	derived, bare, authored := bankHash(unit("mcq", true)), bankHash(unit("", false)), bankHash(unit("mcq", false))
	if derived != bare {
		t.Errorf("a derived kind changed the hash: %s vs %s", derived, bare)
	}
	if authored == bare {
		t.Errorf("an authored kind is part of the bank and must change the hash")
	}
}
