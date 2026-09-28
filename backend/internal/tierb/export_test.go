package tierb

// export_test.go hands the external DB test (package tierb_test) the corpus
// of ADVERSARIAL queries the validator ACCEPTED, so every one of them is also
// executed against a real second tenant. Compiled into the test binary only.

// AcceptedCorpus returns every injected and grammar-generated query that
// parsed, plus the valid set.
func AcceptedCorpus() []*Query {
	var out []*Query
	srcs := append(append(append([]string{}, validQueries...), injectedSources()...), grammarSources(7, 4000)...)
	for _, s := range srcs {
		if q, err := Parse(s); err == nil {
			out = append(out, q)
		}
	}
	return out
}
