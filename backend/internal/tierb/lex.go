// Package tierb is PLAN-AGENTIC's TIER B: the model may write a real SQL
// SELECT, but only over PUBLISHED VIEWS, only through a hand-written
// allow-list validator, only as the agent_ro role, under READ ONLY, a 5 s
// statement timeout, an EXPLAIN cost gate and a forced LIMIT (§2.3: "No raw
// SQL from the model against base tables").
//
// ⛔ THE SECURITY MODEL IS DENY-BY-DEFAULT AND RE-EMISSION, NOT FILTERING.
// The validator does not look for bad things in the model's text; it PARSES
// the text into a deliberately small grammar (lex.go, parse.go), refuses
// anything outside it, resolves every identifier against the published-view
// catalogue (views.go), and then PRINTS a new SQL string from the syntax tree
// (print.go). The text the model wrote is never sent to Postgres. A comment,
// a second statement, a dollar-quoted string or a quoted identifier cannot
// survive because the printer has no way to emit one.
//
// ⛔ AND THE VALIDATOR IS NOT THE ONLY WALL — BUT IT IS THE ONLY WALL
// AGAINST ONE THING. SQL that bypassed it entirely would run as agent_ro,
// which holds SELECT on the agent.* views and on nothing else, inside a READ
// ONLY transaction, with the tenant GUC set so FORCE RLS on every base table
// scopes the views' owner to one company (migration 0097). tierb_db_test.go
// runs raw, unvalidated SQL as agent_ro to prove that wall stops base-table
// reads, writes and default cross-tenant reads on its own.
//
// ⚠️ IT DOES NOT STOP GUC TAMPERING. The tenant GUC is a session setting any
// role may change, so raw SQL that knows another company's id can call
// set_config (or SET) and re-scope RLS to that tenant; the same DB test
// measures it and pins it. Only this validator excludes that — set_config and
// current_setting are not allow-listed and SET is not a SELECT. Any path that
// ran SQL as agent_ro WITHOUT this validator (a raw-SQL or Tier C door) would
// be cross-tenant; d122 §5 records the residual and the owed hardening.
//
// ⛔ NO NEW DEPENDENCY (IMPLEMENTATION_PLAN §1.1). There is no SQL parser in
// the module graph and none may be added, which is WHY the accepted grammar
// is small: a hand-written parser for full PostgreSQL would be a large attack
// surface of its own. Everything the grammar omits (CTEs, subqueries, set
// operations, window functions, casts, arrays, LATERAL, quoted identifiers,
// comments, dollar quotes, escape strings, parameters) is REFUSED, and the
// DSL (Tier A) remains the default path.
package tierb

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRejected wraps every validation refusal; the message names the reason
// in terms a model can act on ("functions allowed: ...") without echoing the
// input back.
var ErrRejected = errors.New("tierb: rejected")

func reject(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRejected, fmt.Sprintf(format, a...))
}

// Limits on the INPUT, before any parsing — a 10 MB query or a 10,000-deep
// parenthesis nest is refused as a size, not discovered as a stack overflow.
const (
	MaxQueryBytes = 8000
	maxDepth      = 40
	maxTokens     = 2000
)

type tokKind int

const (
	tIdent  tokKind = iota // unquoted identifier or keyword, lower-cased
	tNumber                // 123, 1.5
	tString                // '...' (value unescaped)
	tOp                    // = <> != < > <= >= + - * / % || ( ) , .
	tEOF
)

type token struct {
	kind tokKind
	text string
	pos  int
}

// lex tokenises the ONE statement. Everything not explicitly recognised is a
// refusal, and several things Postgres would happily accept are refused on
// purpose: comments (-- and /* */), a semicolon ANYWHERE (one statement, no
// trailing ';' either — the model is told so), quoted identifiers ("x"),
// dollar quotes ($$ / $tag$), escape and Unicode strings (E'..', U&'..'), bit
// strings (B'..', X'..'), parameters ($1, ?), casts (::), and every byte
// outside printable ASCII outside a string literal.
func lex(src string) ([]token, error) {
	if len(src) > MaxQueryBytes {
		return nil, reject("query longer than %d bytes", MaxQueryBytes)
	}
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			return nil, reject("comments are not allowed")
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			return nil, reject("comments are not allowed")
		case c == ';':
			return nil, reject("one statement only, with no ';'")
		case c == '"':
			return nil, reject("quoted identifiers are not allowed")
		case c == '$' || c == '?':
			return nil, reject("parameters and dollar quoting are not allowed")
		case c == '\'':
			// A plain SQL string: '' is the only escape. A backslash is a
			// literal backslash under standard_conforming_strings (the PG
			// default since 9.1 and what the executor pins), so it is kept
			// as a byte — and the printer re-escapes only quotes.
			var b strings.Builder
			j := i + 1
			closed := false
			for j < len(src) {
				if src[j] == '\'' {
					if j+1 < len(src) && src[j+1] == '\'' {
						b.WriteByte('\'')
						j += 2
						continue
					}
					closed = true
					j++
					break
				}
				if src[j] == 0 {
					return nil, reject("NUL byte in a string")
				}
				b.WriteByte(src[j])
				j++
			}
			if !closed {
				return nil, reject("unterminated string")
			}
			out = append(out, token{tString, b.String(), i})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			w := strings.ToLower(src[i:j])
			// E'..', U&'..', B'..', X'..', N'..': a prefix glued to a quote.
			if j < len(src) && (src[j] == '\'' || src[j] == '&') {
				return nil, reject("prefixed string literals (E'', U&'', B'', X'', N'') are not allowed")
			}
			out = append(out, token{tIdent, w, i})
			i = j
		case c >= '0' && c <= '9':
			j := i
			dot := false
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || (src[j] == '.' && !dot)) {
				if src[j] == '.' {
					dot = true
				}
				j++
			}
			if j < len(src) && (isIdentStart(src[j]) || src[j] == '.') {
				// 1e10, 0x1F, 1..2, 12abc
				return nil, reject("malformed number")
			}
			out = append(out, token{tNumber, src[i:j], i})
			i = j
		default:
			op, n := lexOp(src[i:])
			if n == 0 {
				return nil, reject("character %q is not allowed", c)
			}
			out = append(out, token{tOp, op, i})
			i += n
		}
		if len(out) > maxTokens {
			return nil, reject("query has more than %d tokens", maxTokens)
		}
	}
	out = append(out, token{tEOF, "", len(src)})
	return out, nil
}

func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || (c >= '0' && c <= '9') }

// lexOp recognises the CLOSED operator set. Notably absent: :: (casts), ~ and
// SIMILAR (regex, a ReDoS surface), @ # ^ & | (bitwise / geometric / jsonb),
// [ ] (arrays), { } and ! on its own.
func lexOp(s string) (string, int) {
	for _, op := range []string{"<>", "!=", "<=", ">=", "||"} {
		if strings.HasPrefix(s, op) {
			return op, 2
		}
	}
	if strings.HasPrefix(s, "::") {
		return "", 0
	}
	switch s[0] {
	case '=', '<', '>', '+', '-', '*', '/', '%', '(', ')', ',', '.':
		return s[:1], 1
	}
	return "", 0
}
