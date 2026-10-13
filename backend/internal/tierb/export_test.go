package tierb

import "strings"

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

// OrderUnstable reports whether SQL leaves the ROWS of q's answer unspecified:
// a LIMIT or OFFSET with no ORDER BY may return a different subset of the same
// visible rows whenever the plan or the heap changes. The leak replay needs to
// know, because for such a query "B's answer changed" is not evidence on its
// own (TestAcceptedAdversarialQueriesLeakNothing).
func (q *Query) OrderUnstable() bool {
	return (q.limit >= 0 || q.offset >= 0) && len(q.orderBy) == 0
}

// Aggregates reports whether q computes over rows (GROUP BY or any aggregate
// call). An aggregate folds rows it does not return, so a changed aggregate is
// always treated as a leak, never as an unstable row choice.
func (q *Query) Aggregates() bool {
	if len(q.groupBy) > 0 || q.having != nil {
		return true
	}
	s := strings.ToLower(q.SQL(1 << 20))
	for _, f := range []string{"count(", "sum(", "avg(", "min(", "max(", "_agg(", "bool_and(", "bool_or(", "every("} {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}
