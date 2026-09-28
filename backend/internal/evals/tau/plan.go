package tau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// A plan is a fixture's hand-written API script. It is how a task SEEDS its
// starting book and how the ReferenceAgent solves it, and it is written against
// names, not ids, because a fixture cannot know the uuids a fresh company mints.
//
// PLACEHOLDERS. Any string in a step's path or body may carry {…} placeholders:
//
//	{@ledger:HDFC Bank}   the id of the ledger with that exact name
//	{@item:Steel Rod 12mm} the id of the stock item with that exact name
//	{@voucher:S-12}       the id of the live voucher numbered S-12
//	{name}                a value an earlier step captured
//
// A lookup that finds nothing, or finds two, is an ERROR rather than an empty
// string: a seed that silently posted to ledger "" would 400, and a seed that
// silently picked one of two same-named vouchers would seed the wrong book.

var placeholderRe = regexp.MustCompile(`\{([^{}]+)\}`)

// Executor runs plan steps against one company's Books.
type Executor struct {
	Books    Books
	Captures map[string]string
	// cache holds name->id maps, dropped after every write so a step always
	// resolves against the book as it is now.
	cache map[string]map[string][]string
}

// NewExecutor returns an Executor with no captures.
func NewExecutor(b Books) *Executor {
	return &Executor{Books: b, Captures: map[string]string{}}
}

// Run executes steps in order and returns the answer step's rendered text ("" if
// the plan has none). The first failing step stops the run.
func (e *Executor) Run(ctx context.Context, steps []Step) (string, error) {
	answer := ""
	for i, s := range steps {
		if s.IsAnswer() {
			a, err := e.expand(ctx, s.Answer)
			if err != nil {
				return "", fmt.Errorf("step %d (answer): %w", i, err)
			}
			answer = a
			continue
		}
		if err := e.Step(ctx, s); err != nil {
			return "", fmt.Errorf("step %d (%s %s): %w", i, s.Method, s.Path, err)
		}
	}
	return answer, nil
}

// Step executes one call. Any non-2xx is an error.
func (e *Executor) Step(ctx context.Context, s Step) error {
	path, err := e.expandPath(ctx, s.Path)
	if err != nil {
		return err
	}
	var body []byte
	if len(s.Body) > 0 {
		var v any
		dec := json.NewDecoder(bytes.NewReader(s.Body))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("body: %w", err)
		}
		v, err = e.expandValue(ctx, v)
		if err != nil {
			return err
		}
		if body, err = json.Marshal(v); err != nil {
			return err
		}
	}
	code, resp, err := e.Books.Do(ctx, s.Method, path, body)
	if err != nil {
		return err
	}
	if s.Method != http.MethodGet {
		e.cache = nil
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("status %d: %s", code, truncate(resp, 400))
	}
	for name, sel := range s.Capture {
		val, err := Select(resp, sel)
		if err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
		e.Captures[name] = val
	}
	return nil
}

// expandPath expands placeholders and path-escapes each substituted value, so
// a ledger id is a segment and a date is a query value, never a new segment.
func (e *Executor) expandPath(ctx context.Context, p string) (string, error) {
	return e.expandWith(ctx, p, url.PathEscape)
}

func (e *Executor) expand(ctx context.Context, s string) (string, error) {
	return e.expandWith(ctx, s, func(v string) string { return v })
}

func (e *Executor) expandWith(ctx context.Context, s string, esc func(string) string) (string, error) {
	var firstErr error
	out := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		if firstErr != nil {
			return m
		}
		v, err := e.resolve(ctx, m[1:len(m)-1])
		if err != nil {
			firstErr = err
			return m
		}
		return esc(v)
	})
	return out, firstErr
}

func (e *Executor) expandValue(ctx context.Context, v any) (any, error) {
	switch x := v.(type) {
	case string:
		return e.expand(ctx, x)
	case []any:
		for i := range x {
			y, err := e.expandValue(ctx, x[i])
			if err != nil {
				return nil, err
			}
			x[i] = y
		}
		return x, nil
	case map[string]any:
		for k := range x {
			y, err := e.expandValue(ctx, x[k])
			if err != nil {
				return nil, err
			}
			x[k] = y
		}
		return x, nil
	default:
		return v, nil
	}
}

func (e *Executor) resolve(ctx context.Context, ref string) (string, error) {
	if !strings.HasPrefix(ref, "@") {
		v, ok := e.Captures[ref]
		if !ok {
			return "", fmt.Errorf("placeholder {%s}: nothing captured under that name", ref)
		}
		return v, nil
	}
	kind, name, ok := strings.Cut(ref[1:], ":")
	if !ok {
		return "", fmt.Errorf("placeholder {%s}: want {@kind:name}", ref)
	}
	idx, err := e.index(ctx, kind)
	if err != nil {
		return "", err
	}
	ids := idx[name]
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("placeholder {%s}: no %s named %q", ref, kind, name)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("placeholder {%s}: %d %ss named %q", ref, len(ids), kind, name)
	}
}

func (e *Executor) index(ctx context.Context, kind string) (map[string][]string, error) {
	if m, ok := e.cache[kind]; ok {
		return m, nil
	}
	m := map[string][]string{}
	switch kind {
	case "ledger":
		var r struct {
			Ledgers []struct{ ID, Name string } `json:"ledgers"`
		}
		if err := getJSON(ctx, e.Books, "/ledgers", &r); err != nil {
			return nil, err
		}
		for _, l := range r.Ledgers {
			m[l.Name] = append(m[l.Name], l.ID)
		}
	case "item":
		var r struct {
			StockItems []struct{ ID, Name string } `json:"stock_items"`
		}
		if err := getJSON(ctx, e.Books, "/stock-items", &r); err != nil {
			return nil, err
		}
		for _, it := range r.StockItems {
			m[it.Name] = append(m[it.Name], it.ID)
		}
	case "voucher":
		var r struct {
			Entries []struct {
				VoucherID string `json:"voucher_id"`
				VoucherNo string `json:"voucher_no"`
				Op        string `json:"op"`
			} `json:"entries"`
		}
		q := url.Values{"from": {snapshotFrom}, "to": {snapshotTo}}
		if err := getJSON(ctx, e.Books, "/day-book?"+q.Encode(), &r); err != nil {
			return nil, err
		}
		for _, v := range r.Entries {
			if strings.EqualFold(v.Op, "DELETE") {
				continue
			}
			m[v.VoucherNo] = append(m[v.VoucherNo], v.VoucherID)
		}
	default:
		return nil, fmt.Errorf("unknown reference kind %q", kind)
	}
	if e.cache == nil {
		e.cache = map[string]map[string][]string{}
	}
	e.cache[kind] = m
	return m, nil
}

// Select walks a JSON document with a dotted selector:
//
//	sup_details.osup_det.txval
//	rows[ledger=Acme Traders].debit
//	uncleared[voucher_no=P-1].leg_no
//	b2cs[0].camt
//
// A filter that matches zero or several elements is an error, for the same
// reason an ambiguous name is.
func Select(doc []byte, selector string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var cur any
	if err := dec.Decode(&cur); err != nil {
		return "", fmt.Errorf("select %q: %w", selector, err)
	}
	for _, seg := range splitSelector(selector) {
		name, filter, hasFilter := seg, "", false
		if i := strings.IndexByte(seg, '['); i >= 0 && strings.HasSuffix(seg, "]") {
			name, filter, hasFilter = seg[:i], seg[i+1:len(seg)-1], true
		}
		if name != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				return "", fmt.Errorf("select %q: %q is not an object", selector, name)
			}
			if cur, ok = obj[name]; !ok {
				return "", fmt.Errorf("select %q: no key %q", selector, name)
			}
		}
		if !hasFilter {
			continue
		}
		arr, ok := cur.([]any)
		if !ok {
			return "", fmt.Errorf("select %q: %q is not an array", selector, name)
		}
		if n, err := strconv.Atoi(filter); err == nil {
			if n < 0 || n >= len(arr) {
				return "", fmt.Errorf("select %q: index %d out of range (%d)", selector, n, len(arr))
			}
			cur = arr[n]
			continue
		}
		fk, fv, ok := strings.Cut(filter, "=")
		if !ok {
			return "", fmt.Errorf("select %q: bad filter %q", selector, filter)
		}
		var hits []any
		for _, el := range arr {
			if obj, ok := el.(map[string]any); ok && scalar(obj[fk]) == fv {
				hits = append(hits, el)
			}
		}
		if len(hits) != 1 {
			return "", fmt.Errorf("select %q: filter [%s] matched %d elements", selector, filter, len(hits))
		}
		cur = hits[0]
	}
	switch cur.(type) {
	case map[string]any, []any, nil:
		return "", fmt.Errorf("select %q: resolves to a non-scalar", selector)
	}
	return scalar(cur), nil
}

// splitSelector splits on dots outside brackets, so a filter value may itself
// contain a dot ("rows[ledger=Acme Pvt. Ltd.].debit").
func splitSelector(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
