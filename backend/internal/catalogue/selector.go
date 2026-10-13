package catalogue

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/khata/cored/internal/query"
)

// selector.go is the FIRST HOP of the CHESS-style selector PLAN-AGENTIC W3
// asks for (question -> top-k reports + entities -> DSL), built LEXICALLY.
//
// ⛔ IT IS BM25 OVER THE CATALOGUE, NOT AN EMBEDDING, AND THAT IS A STAGE, NOT
// A CHOICE. The plan's selector embeds the question; this repo has no
// embedding model (no provider keys, and the compose image has no pgvector),
// and W1a's Hinglish embedding eval is what DECIDES the model. A lexical
// selector is the baseline that eval has to beat, and it is useful today: the
// catalogue's Hinglish phrasings are exactly the vocabulary a transliterating
// embedding would otherwise have to learn. When embeddings land, Select keeps
// its signature and fuses the two rankings (reciprocal-rank fusion), so every
// caller and the golden test below survive the swap.
//
// ⚠️ DETERMINISM IS THE CONTRACT. Ties break on the report name, the corpus is
// built once from constant data, and there is no randomness — so the W1/W8
// evals can pin a ranking and a regression is distinguishable from noise (the
// same reason actions.Search is unranked and exact).

// Match is one ranked report.
type Match struct {
	Report string  `json:"report"`
	Score  float64 `json:"score"`
	// Entities are the DSL entities this report's examples use, in first-use
	// order — the second hop. Empty when the report has no DSL.
	Entities []query.Entity `json:"entities,omitempty"`
}

// stopwords are dropped before scoring: English and Hinglish function words
// that appear in every question and so carry no signal. "kitna" is here
// because it means "how much" and is in half the questions; "baki" is NOT,
// because it means "outstanding" and is the whole question.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "of": true, "for": true,
	"in": true, "to": true, "and": true, "or": true, "on": true, "by": true, "we": true, "our": true,
	"us": true, "my": true, "me": true, "i": true, "do": true, "does": true, "did": true, "what": true,
	"which": true, "how": true, "much": true, "many": true, "show": true, "list": true, "give": true,
	"this": true, "that": true, "last": true, "month": true, "year": true, "week": true, "today": true,
	"have": true, "has": true, "all": true, "any": true, "there": true, "with": true, "from": true, "at": true,
	"ka": true, "ki": true, "ke": true, "ko": true, "hai": true, "hain": true, "kya": true, "kitna": true,
	"kitni": true, "kitne": true, "mein": true, "se": true, "aur": true, "batao": true, "dikhao": true,
	"mahine": true, "saal": true, "hua": true, "hui": true, "ho": true, "tha": true, "nikalo": true,
	"banao": true, "karo": true, "please": true, "pls": true,
}

func tokens(s string) []string {
	s = strings.ToLower(s)
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) < 2 || stopwords[w] {
			// "me" is both "me" and "in" (mein): dropped either way.
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stem is a deliberately tiny suffix folder: plurals and the -ing/-ed forms
// that separate "deposits" from "deposit" and "owed" from "owe". Anything
// cleverer belongs to the embedding.
func stem(w string) string {
	for _, suf := range []string{"ing", "ies", "es", "ed", "s"} {
		if len(w) > len(suf)+2 && strings.HasSuffix(w, suf) {
			if suf == "ies" {
				return w[:len(w)-3] + "y"
			}
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// terms adds bigrams to the unigrams, so "bank statement" outranks "bank" +
// "statement" scattered across two unrelated entries.
func terms(s string) []string {
	u := tokens(s)
	out := append([]string(nil), u...)
	for i := 0; i+1 < len(u); i++ {
		out = append(out, u[i]+"_"+u[i+1])
	}
	return out
}

type doc struct {
	report string
	tf     map[string]float64
	length float64
}

type index struct {
	docs  []doc
	df    map[string]int
	avgdl float64
}

// The title, synonyms and Hinglish phrasings are the report's NAMES and weigh
// double; the questions and description are context.
const nameWeight = 2.0

var idx = buildIndex()

func buildIndex() *index {
	ix := &index{df: map[string]int{}}
	total := 0.0
	for _, e := range Reports() {
		d := doc{report: e.Report, tf: map[string]float64{}}
		add := func(s string, w float64) {
			for _, t := range terms(s) {
				d.tf[t] += w
				d.length += w
			}
		}
		add(e.Title, nameWeight)
		add(strings.ReplaceAll(e.Report, "-", " "), nameWeight)
		for _, s := range e.Synonyms {
			add(s, nameWeight)
		}
		for _, s := range e.Hinglish {
			add(s, nameWeight)
		}
		for _, s := range e.Questions {
			add(s, 1)
		}
		add(e.Description, 1)
		for t := range d.tf {
			ix.df[t]++
		}
		total += d.length
		ix.docs = append(ix.docs, d)
	}
	if n := len(ix.docs); n > 0 {
		ix.avgdl = total / float64(n)
	}
	return ix
}

// Select ranks the catalogue's reports against a question and returns the
// top k with a positive score. It never returns a report the question shares
// no term with: an empty result is the honest "I do not know which report",
// and the caller (W2's planner) must treat it as a refusal to guess.
func Select(question string, k int) []Match {
	const k1, b = 1.2, 0.75
	q := terms(question)
	n := float64(len(idx.docs))
	var out []Match
	for _, d := range idx.docs {
		score := 0.0
		for _, t := range q {
			tf := d.tf[t]
			if tf == 0 {
				continue
			}
			df := float64(idx.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*d.length/idx.avgdl))
		}
		if score > 0 {
			out = append(out, Match{Report: d.report, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Report < out[j].Report
	})
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	for i := range out {
		out[i].Entities = entitiesOf(out[i].Report)
	}
	return out
}

func entitiesOf(report string) []query.Entity {
	e, ok := byReport[report]
	if !ok {
		return nil
	}
	var out []query.Entity
	seen := map[query.Entity]bool{}
	for _, q := range e.DSL {
		if !seen[q.Entity] {
			seen[q.Entity] = true
			out = append(out, q.Entity)
		}
	}
	return out
}
