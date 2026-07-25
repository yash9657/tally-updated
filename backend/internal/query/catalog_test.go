package query

import (
	"sort"
	"testing"
)

// TestCatalogCoversWhitelistExactly is the anti-drift pin: it independently
// re-derives the expected catalog surface straight from the `entities` map (the
// compiler's own authority) and asserts Catalog() matches it EXACTLY — every
// dimension/metric/filter/dateCol key, no more and no fewer — plus the two
// compiler special-cases that live outside the maps (universal count(*), and the
// outstandings SUM-only "pending"). A future whitelist widening that forgets to
// flow through Catalog() fails here.
func TestCatalogCoversWhitelistExactly(t *testing.T) {
	got := Catalog()
	if len(got) != len(entities) {
		t.Fatalf("Catalog has %d entities, whitelist has %d", len(got), len(entities))
	}

	byEntity := map[string]EntityDoc{}
	for _, d := range got {
		if _, dup := byEntity[d.Entity]; dup {
			t.Fatalf("Catalog lists entity %q twice", d.Entity)
		}
		byEntity[d.Entity] = d
	}

	for ent, def := range entities {
		doc, ok := byEntity[string(ent)]
		if !ok {
			t.Fatalf("Catalog is missing entity %q", ent)
		}

		// Dimensions: exact key set.
		assertSameSet(t, string(ent)+" dimensions", keysOf(def.dimensions), doc.Dimensions)

		// DateCols: exact key set.
		assertSameSet(t, string(ent)+" dateCols", keysOf(def.dateCols), doc.DateCols)

		// Metrics: every whitelist metric with its exact agg set, PLUS the
		// synthesized universal "*"/count and (outstandings only) "pending"/sum.
		wantMetrics := map[string][]string{}
		for name, mc := range def.metrics {
			wantMetrics[name] = aggNames(mc.aggs)
		}
		wantMetrics["*"] = []string{string(AggCount)}
		if ent == EntityOutstandings {
			wantMetrics["pending"] = []string{string(AggSum)}
		}
		gotMetrics := map[string][]string{}
		for _, m := range doc.Metrics {
			gotMetrics[m.Name] = m.Aggs
		}
		if len(gotMetrics) != len(wantMetrics) {
			t.Errorf("%s: Catalog has %d metrics, want %d (got %v)", ent, len(gotMetrics), len(wantMetrics), metricNames(doc.Metrics))
		}
		for name, wantAggs := range wantMetrics {
			gotAggs, ok := gotMetrics[name]
			if !ok {
				t.Errorf("%s: Catalog metric %q missing", ent, name)
				continue
			}
			assertSameSet(t, string(ent)+" metric "+name+" aggs", wantAggs, gotAggs)
		}

		// Filters: every whitelist filter with its exact op set.
		wantFilters := map[string][]string{}
		for name, fc := range def.filters {
			wantFilters[name] = opNames(fc.ops)
		}
		gotFilters := map[string][]string{}
		for _, f := range doc.Filters {
			gotFilters[f.Name] = f.Ops
		}
		if len(gotFilters) != len(wantFilters) {
			t.Errorf("%s: Catalog has %d filters, want %d", ent, len(gotFilters), len(wantFilters))
		}
		for name, wantOps := range wantFilters {
			gotOps, ok := gotFilters[name]
			if !ok {
				t.Errorf("%s: Catalog filter %q missing", ent, name)
				continue
			}
			assertSameSet(t, string(ent)+" filter "+name+" ops", wantOps, gotOps)
		}
	}
}

// TestCatalogSynthesizesOutstandingsAndCount pins the two additions explicitly so
// the intent is legible even if the anti-drift assertions above are refactored.
func TestCatalogSynthesizesOutstandingsAndCount(t *testing.T) {
	for _, d := range Catalog() {
		var hasCount bool
		for _, m := range d.Metrics {
			if m.Name == "*" {
				hasCount = true
				if len(m.Aggs) != 1 || m.Aggs[0] != string(AggCount) {
					t.Errorf("%s: '*' metric aggs = %v, want [count]", d.Entity, m.Aggs)
				}
			}
		}
		if !hasCount {
			t.Errorf("%s: missing universal count(*) metric", d.Entity)
		}
		if d.Entity == string(EntityOutstandings) {
			var hasPending bool
			for _, m := range d.Metrics {
				if m.Name == "pending" {
					hasPending = true
					if len(m.Aggs) != 1 || m.Aggs[0] != string(AggSum) {
						t.Errorf("outstandings pending aggs = %v, want [sum]", m.Aggs)
					}
				}
			}
			if !hasPending {
				t.Error("outstandings is missing the synthesized 'pending' metric")
			}
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func metricNames(ms []MetricDoc) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func assertSameSet(t *testing.T, label string, want, got []string) {
	t.Helper()
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	if len(w) != len(g) {
		t.Errorf("%s: set mismatch: want %v, got %v", label, w, g)
		return
	}
	for i := range w {
		if w[i] != g[i] {
			t.Errorf("%s: set mismatch: want %v, got %v", label, w, g)
			return
		}
	}
}
