package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// trigramEmbedder is a TEST DOUBLE: character trigrams hashed into Dim
// buckets, L2-normalised. Texts that share trigrams land near each other,
// which is enough to prove the store ranks by vector and that RLS scopes the
// vectors. ⛔ It is not a model and nothing outside _test.go may use it: a
// "semantic" search backed by this would be a lexical search wearing a lab
// coat, which is the silent degradation this package refuses.
type trigramEmbedder struct{ calls int }

func (e *trigramEmbedder) Version() string { return "test-trigram-hash@1024/v1" }
func (e *trigramEmbedder) ModelID() string { return "test-trigram-hash" }
func (e *trigramEmbedder) Embed(_ context.Context, _ uuid.UUID, texts []string) ([][]float32, error) {
	e.calls++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = trigramVector(t)
	}
	return out, nil
}

func trigramVector(t string) []float32 {
	v := make([]float32, Dim)
	s := " " + strings.ToLower(t) + " "
	r := []rune(s)
	for i := 0; i+3 <= len(r); i++ {
		h := fnv.New32a()
		h.Write([]byte(string(r[i : i+3])))
		v[h.Sum32()%Dim]++
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}

func TestFuseIsReciprocalRankAndDeterministic(t *testing.T) {
	a, b, c := uuid.MustParse("00000000-0000-0000-0000-00000000000a"), uuid.MustParse("00000000-0000-0000-0000-00000000000b"), uuid.MustParse("00000000-0000-0000-0000-00000000000c")
	lex := []Hit{{ID: a}, {ID: b}}
	vec := []Hit{{ID: b}, {ID: c}}
	got := Fuse(lex, vec)
	if len(got) != 3 || got[0].ID != b {
		t.Fatalf("b is in both lists and must rank first: %+v", got)
	}
	want := 1.0/61 + 1.0/62
	if math.Abs(got[0].Score-want) > 1e-12 {
		t.Errorf("fused score %v, want 1/61+1/62 = %v", got[0].Score, want)
	}
	// a and c tie (rank 1 in one list each): id order decides.
	if got[1].ID != a || got[2].ID != c {
		t.Errorf("a tie must break on id: %v %v", got[1].ID, got[2].ID)
	}
	if len(Fuse(lex, nil)) != 2 {
		t.Error("fusing with an empty list must keep the other")
	}
}

func TestFormatVectorIsPgvectorText(t *testing.T) {
	if got := FormatVector([]float32{0.5, -1, 3.25e-5}); got != "[0.5,-1,3.25e-05]" {
		t.Errorf("FormatVector = %s", got)
	}
}

func TestUntrustedKindsAreTheThirdPartyOnes(t *testing.T) {
	want := map[Kind]bool{KindNarration: true, KindBankLine: true, KindDocChunk: true}
	for _, k := range Kinds {
		if k.Untrusted() != want[k] {
			t.Errorf("%s.Untrusted() = %v", k, k.Untrusted())
		}
	}
	if len(Kinds) != 7 {
		t.Errorf("%d kinds; 0098's CHECK lists 7", len(Kinds))
	}
}
