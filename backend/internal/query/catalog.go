package query

// catalog.go exports a NAMES-ONLY description of the query surface (entities,
// dimensions, metrics + their legal aggregates, filters + their legal operators,
// date columns) for the NL->DSL translator's system prompt. It is derived from
// the same `entities` whitelist the compiler enforces, so a future widening of
// the DSL automatically flows into the catalog — catalog_test.go pins that the
// two never drift.
//
// SECURITY NOTE. Catalog() exposes ONLY display names, never the constant SQL
// fragments in whitelist.go (rawExpr/skeleton/expr). The translator is downstream
// of the compiler in the trust model: whatever DSL the LLM emits is re-validated
// by Compile, so the catalog is a HINT, not an authority. It exists so the LLM
// asks for legal columns in the first place; a hallucinated column simply 422s.

import "sort"

// MetricDoc is one aggregate-able column and the CLOSED set of aggregate
// functions legal on it (e.g. {"debit", ["avg","max","min","sum"]}). The special
// universal metric {"*", ["count"]} appears on every entity (count(*) is handled
// generically by the compiler and lives in no metrics map).
type MetricDoc struct {
	Name string   `json:"name"`
	Aggs []string `json:"aggs"`
}

// FilterDoc is one filterable column and the CLOSED set of comparison operators
// legal on it.
type FilterDoc struct {
	Name string   `json:"name"`
	Ops  []string `json:"ops"`
}

// EntityDoc documents one queryable entity: its dimensions (group-by targets),
// metrics, filters and date columns — names only. It is the unit the NL->DSL
// system prompt is built from.
type EntityDoc struct {
	Entity     string      `json:"entity"`
	Dimensions []string    `json:"dimensions"`
	Metrics    []MetricDoc `json:"metrics"`
	Filters    []FilterDoc `json:"filters"`
	DateCols   []string    `json:"date_cols"`
}

// Catalog returns the full, deterministically-ordered query surface synthesized
// from the whitelist, PLUS the two compiler special-cases that live outside the
// maps:
//
//   - the universal count(*) metric — {"*", ["count"]} — legal on EVERY entity
//     (the compiler handles AggCount before consulting the metrics map).
//   - the outstandings "pending" metric — SUM-only — which the compiler
//     special-cases by nature-orientation BEFORE the (deliberately empty)
//     outstandings metrics map is consulted.
//
// Without these synthesized additions a mechanical walk of `entities` would tell
// the LLM that outstandings has ZERO metrics (so it could never ask for
// receivables/payables) and that count(*) is illegal everywhere — both wrong.
func Catalog() []EntityDoc {
	docs := make([]EntityDoc, 0, len(entities))
	for ent, def := range entities {
		doc := EntityDoc{Entity: string(ent)}

		for name := range def.dimensions {
			doc.Dimensions = append(doc.Dimensions, name)
		}
		sort.Strings(doc.Dimensions)

		for name, mc := range def.metrics {
			doc.Metrics = append(doc.Metrics, MetricDoc{Name: name, Aggs: aggNames(mc.aggs)})
		}
		// Synthesized: the outstandings "pending" metric is SUM-only and lives
		// outside the (empty-by-design) metrics map.
		if ent == EntityOutstandings {
			doc.Metrics = append(doc.Metrics, MetricDoc{Name: "pending", Aggs: []string{string(AggSum)}})
		}
		// Synthesized: count(*) is universal.
		doc.Metrics = append(doc.Metrics, MetricDoc{Name: "*", Aggs: []string{string(AggCount)}})
		sort.Slice(doc.Metrics, func(i, j int) bool { return doc.Metrics[i].Name < doc.Metrics[j].Name })

		for name, fc := range def.filters {
			doc.Filters = append(doc.Filters, FilterDoc{Name: name, Ops: opNames(fc.ops)})
		}
		sort.Slice(doc.Filters, func(i, j int) bool { return doc.Filters[i].Name < doc.Filters[j].Name })

		for name := range def.dateCols {
			doc.DateCols = append(doc.DateCols, name)
		}
		sort.Strings(doc.DateCols)

		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Entity < docs[j].Entity })
	return docs
}

// aggNames returns the sorted string names of an agg set.
func aggNames(aggs map[AggFunc]bool) []string {
	out := make([]string, 0, len(aggs))
	for a := range aggs {
		out = append(out, string(a))
	}
	sort.Strings(out)
	return out
}

// opNames returns the sorted string names of an operator set.
func opNames(ops map[Op]bool) []string {
	out := make([]string, 0, len(ops))
	for o := range ops {
		out = append(out, string(o))
	}
	sort.Strings(out)
	return out
}
