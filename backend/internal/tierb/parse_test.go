package tierb

import (
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// parse_test.go is the HERMETIC half of the Tier-B gate: the ≥500-case
// adversarial / fuzz suite PLAN-AGENTIC W3 demands ("zero Tier-B queries
// escape the AST allow-list in a 500-case fuzz"). Every case is either
// REFUSED, or ACCEPTED and then held to invariants on the RE-EMITTED SQL that
// no escape could satisfy. The DB half (tierb_db_test.go) runs every accepted
// case as agent_ro against a second tenant.

// validQueries are things a model should be able to ask.
var validQueries = []string{
	`SELECT voucher_type, count(*) AS n FROM vouchers GROUP BY voucher_type ORDER BY n DESC`,
	`SELECT sum(natural_net) FROM postings WHERE group_name = 'Sales Accounts' AND voucher_date BETWEEN DATE '2025-04-01' AND DATE '2026-03-31'`,
	`select ledger_name, closing_net from ledgers where nature in ('ASSET','LIABILITY') order by closing_net desc limit 10`,
	`SELECT p.ledger_name, sum(p.amount) total FROM postings p WHERE p.dr_cr = 'D' GROUP BY p.ledger_name HAVING sum(p.amount) > 1000`,
	`SELECT item_name, sum(CASE WHEN direction = 'OUT' THEN qty ELSE 0 END) AS sold FROM stock_movements GROUP BY item_name`,
	`SELECT date_trunc('month', voucher_date) AS m, sum(amount) FROM stock_movements WHERE direction = 'OUT' GROUP BY m ORDER BY m`,
	`SELECT v.party_name, count(DISTINCT v.voucher_id) FROM vouchers v LEFT JOIN postings p ON p.voucher_id = v.voucher_id WHERE v.voucher_class = 'Sales' GROUP BY v.party_name`,
	`SELECT bank_ledger_name, match_status, sum(amount) FROM bank_lines GROUP BY bank_ledger_name, match_status`,
	`SELECT ledger_name, ref_name, sum(CASE WHEN dr_cr = 'D' THEN amount ELSE -amount END) AS pending FROM bills WHERE nature = 'ASSET' GROUP BY ledger_name, ref_name HAVING sum(CASE WHEN dr_cr = 'D' THEN amount ELSE -amount END) <> 0`,
	`SELECT entity_name, fact_key, value_num FROM facts WHERE entity_kind = 'party' AND fact_key LIKE 'receipt_lag%'`,
	`SELECT item_name, hsn FROM items WHERE lower(item_name) ILIKE '%widget%' OR hsn IS NULL`,
	`SELECT * FROM items`,
	`SELECT DISTINCT voucher_type FROM vouchers`,
	`SELECT round(avg(rate), 2), min(rate), max(rate) FROM stock_movements WHERE item_name = 'O''Brien Bolts' AND NOT direction = 'IN'`,
	`SELECT coalesce(party_name, '(none)') AS party, nullif(sum(taxable_value), 0) FROM vouchers GROUP BY party ORDER BY party NULLS LAST LIMIT 5 OFFSET 5`,
	`SELECT voucher_no FROM vouchers WHERE voucher_date >= DATE '2025-04-01' AND voucher_no NOT LIKE 'X%' AND voucher_type NOT IN ('Journal')`,
	`SELECT abs(-closing_net) * 100 / 3 % 7 FROM ledgers WHERE closing_net <> 0 AND closing_net != 1`,
	`SELECT ledger_name || ' / ' || group_name FROM ledgers`,
	`SELECT greatest(debit, credit), least(debit, credit), floor(debit), ceil(credit), length(ledger_name) FROM ledgers`,
	`SELECT date_part('year', value_date) y, count(*) FROM bank_lines GROUP BY y`,
	`SELECT s.item_name, i.hsn FROM stock_movements s JOIN items i ON i.item_name = s.item_name WHERE s.qty > 1.5`,
	`SELECT upper(entity_kind), value_text FROM facts WHERE value_text IS NOT NULL AND unit = 'class'`,
	`SELECT count(*) FROM postings WHERE amount NOT BETWEEN 0 AND 10 AND TRUE AND NOT FALSE`,
}

// invariantsOf checks the RE-EMITTED SQL of an accepted query. Each check is
// a property an escape would have to break:
func invariantsOf(t *testing.T, src string, q *Query, maxRows int) {
	t.Helper()
	sql := q.SQL(maxRows)
	lower := strings.ToLower(sql)
	fail := func(why string) { t.Errorf("ESCAPE? %s\n  input: %s\n  sql:   %s", why, src, sql) }
	// 1. One statement, no comments, no dollar quotes, no casts, no params.
	for _, bad := range []string{";", "--", "/*", "$", "::", "?"} {
		if strings.Contains(stripStrings(sql), bad) {
			fail("contains " + bad)
		}
	}
	// 2. Every relation after FROM/JOIN is agent."<view>" for a published view.
	rel := regexp.MustCompile(`(?:FROM|JOIN) ([^ ]+)`)
	for _, m := range rel.FindAllStringSubmatch(stripStrings(sql), -1) {
		name := m[1]
		ok := false
		for _, v := range Views {
			if name == ViewSchema+`."`+v.Name+`"` {
				ok = true
			}
		}
		if !ok {
			fail("relation " + name + " is not a published view")
		}
	}
	// 3. Every function call is allow-listed (pg_catalog-qualified or syntax).
	fn := regexp.MustCompile(`([a-z_][a-z_0-9.]*)\(`)
	for _, m := range fn.FindAllStringSubmatch(stripStrings(lower), -1) {
		name := strings.TrimPrefix(m[1], "pg_catalog.")
		if name == "in" || name == "" {
			continue
		}
		if _, ok := Functions[name]; !ok {
			fail("function " + m[1])
		}
	}
	// 4. A LIMIT at most maxRows is always present, and is the LAST clause
	//    (bar OFFSET).
	lim := regexp.MustCompile(` LIMIT (\d+)( OFFSET \d+)?$`).FindStringSubmatch(sql)
	if lim == nil {
		fail("no trailing LIMIT")
	} else {
		var n int
		fmt.Sscan(lim[1], &n)
		if n > maxRows {
			fail("LIMIT above maxRows")
		}
	}
	// 5. No catalog / role / setting words survive outside string literals.
	for _, w := range []string{"pg_catalog.pg_", "information_schema", "current_setting", "set_config", "company_id", "pg_sleep", "role", "select *  from"} {
		if strings.Contains(stripStrings(lower), w) {
			fail("contains " + w)
		}
	}
	// 6. Idempotent: the printed SQL, with agent. and pg_catalog. and the
	//    quotes stripped, re-parses to the same printed SQL (the tree fully
	//    determines the text; nothing in the text depends on the input).
	again, err := Parse(unprint(sql))
	if err != nil {
		fail("re-emitted SQL does not re-parse: " + err.Error())
	} else if again.SQL(maxRows) != sql {
		fail("re-emission is not a fixed point:\n  again: " + again.SQL(maxRows))
	}
}

// stripStrings blanks out string literal CONTENTS (a payload inside a string
// is data, and the executor binds it as such).
func stripStrings(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			if in && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			in = !in
			b.WriteByte(c)
			continue
		}
		if in {
			b.WriteByte('x')
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// unprint turns printed SQL back into input-grammar SQL.
func unprint(sql string) string {
	s := strings.ReplaceAll(sql, ViewSchema+".", "")
	s = strings.ReplaceAll(s, "pg_catalog.", "")
	// Remove identifier quotes outside strings.
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			in = !in
		}
		if c == '"' && !in {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func TestValidQueriesAreAcceptedAndReemittedSafely(t *testing.T) {
	for _, src := range validQueries {
		q, err := Parse(src)
		if err != nil {
			t.Errorf("valid query refused: %v\n  %s", err, src)
			continue
		}
		invariantsOf(t, src, q, 200)
	}
}

func TestReemissionGolden(t *testing.T) {
	cases := map[string]string{
		`select voucher_type, count(*) n from vouchers group by voucher_type order by n desc limit 5000`: `SELECT "vouchers"."voucher_type", pg_catalog.count(*) AS "n" FROM agent."vouchers" AS "vouchers" GROUP BY "vouchers"."voucher_type" ORDER BY "n" DESC LIMIT 200`,
		`SELECT item_name FROM items WHERE item_name = 'a''b\c'`:                                         `SELECT "items"."item_name" FROM agent."items" AS "items" WHERE ("items"."item_name" = 'a''b\c') LIMIT 200`,
		`SELECT coalesce(hsn, 'none') FROM items i LIMIT 3`:                                              `SELECT COALESCE("i"."hsn", 'none') FROM agent."items" AS "i" LIMIT 3`,
		`SELECT -qty FROM stock_movements WHERE qty > 1 + 2 * 3`:                                         `SELECT (- "stock_movements"."qty") FROM agent."stock_movements" AS "stock_movements" WHERE ("stock_movements"."qty" > (1 + (2 * 3))) LIMIT 200`,
	}
	for src, want := range cases {
		q, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got := q.SQL(200); got != want {
			t.Errorf("\n  src:  %s\n  got:  %s\n  want: %s", src, got, want)
		}
	}
}

// adversarial is the hand-written half: every class the plan names —
// cross-tenant, writes, functions, CTE tricks, comments, set-returning
// functions, pg_catalog — plus the lexical tricks Postgres itself accepts.
var adversarial = []string{
	// Writes and DDL.
	`INSERT INTO ledgers VALUES (1)`, `UPDATE ledgers SET debit = 0`, `DELETE FROM vouchers`,
	`TRUNCATE postings`, `DROP TABLE ledger`, `ALTER TABLE ledger ADD x int`, `CREATE TABLE x (a int)`,
	`GRANT ALL ON ledger TO agent_ro`, `REVOKE ALL ON ledger FROM tally_app`, `COPY ledger TO '/tmp/x'`,
	`VACUUM`, `CALL p()`, `DO $$BEGIN END$$`, `SET ROLE postgres`, `RESET ROLE`, `SET app.current_company_id = 'x'`,
	`LISTEN x`, `NOTIFY x`, `PREPARE p AS SELECT 1`, `EXECUTE p`, `DECLARE c CURSOR FOR SELECT 1`, `LOCK ledger`,
	`EXPLAIN ANALYZE SELECT * FROM vouchers`, `SHOW ALL`, `TABLE ledger`, `VALUES (1)`,
	`SELECT * INTO newtab FROM vouchers`, `SELECT * FROM vouchers FOR UPDATE`, `SELECT * FROM vouchers FOR SHARE`,
	// Multi-statement and comments.
	`SELECT * FROM vouchers; DELETE FROM ledger`, `SELECT * FROM vouchers;`, `SELECT * FROM vouchers -- x`,
	`SELECT * FROM vouchers /* x */`, `SELECT /* */ * FROM vouchers`, `SELECT * FROM vouchers/**/WHERE 1=1`,
	"SELECT * FROM vouchers\n--\n", `SELECT * FROM vouchers /* nested /* */ */`,
	// CTEs and set operations.
	`WITH x AS (SELECT * FROM ledger) SELECT * FROM x`, `WITH RECURSIVE r AS (SELECT 1) SELECT * FROM r`,
	`WITH d AS (DELETE FROM ledger RETURNING *) SELECT * FROM d`,
	`SELECT ledger_name FROM ledgers UNION SELECT name FROM ledger`, `SELECT ledger_name FROM ledgers UNION ALL SELECT usename FROM pg_user`,
	`SELECT ledger_name FROM ledgers INTERSECT SELECT ledger_name FROM ledgers`, `SELECT ledger_name FROM ledgers EXCEPT SELECT 1`,
	// Subqueries everywhere.
	`SELECT * FROM (SELECT * FROM ledger) x`, `SELECT * FROM vouchers WHERE voucher_id IN (SELECT id FROM voucher_version)`,
	`SELECT (SELECT count(*) FROM ledger) FROM vouchers`, `SELECT * FROM vouchers WHERE EXISTS (SELECT 1 FROM ledger)`,
	`SELECT * FROM vouchers WHERE voucher_no = ANY (SELECT name FROM ledger)`,
	// Base tables, other schemas, catalogs.
	`SELECT * FROM ledger`, `SELECT * FROM voucher_version`, `SELECT * FROM public.ledger`, `SELECT * FROM agent.vouchers`,
	`SELECT * FROM pg_catalog.pg_tables`, `SELECT * FROM pg_class`, `SELECT * FROM pg_user`, `SELECT * FROM pg_shadow`,
	`SELECT * FROM pg_authid`, `SELECT * FROM information_schema.tables`, `SELECT * FROM pg_roles`, `SELECT * FROM pg_settings`,
	`SELECT * FROM entity_facts`, `SELECT * FROM tenant_profile`, `SELECT * FROM company`, `SELECT * FROM app_user`,
	`SELECT * FROM vouchers, ledger`, `SELECT * FROM vouchers CROSS JOIN ledgers`, `SELECT * FROM vouchers NATURAL JOIN ledgers`,
	`SELECT * FROM ONLY vouchers`, `SELECT * FROM vouchers TABLESAMPLE SYSTEM (10)`, `SELECT * FROM LATERAL (SELECT 1) x`,
	`SELECT * FROM ROWS FROM (generate_series(1,10))`, `SELECT * FROM vouchers v JOIN ledger l ON true`,
	`SELECT * FROM vouchers JOIN ledgers USING (ledger_name)`, `SELECT * FROM vouchers FULL JOIN ledgers ON true`,
	`SELECT * FROM vouchers RIGHT JOIN ledgers ON true`, `SELECT * FROM vouchers JOIN ledgers`,
	// Columns that are not published.
	`SELECT company_id FROM vouchers`, `SELECT v.company_id FROM vouchers v`, `SELECT payload FROM vouchers`,
	`SELECT description FROM bank_lines`, `SELECT narration FROM vouchers`, `SELECT pan FROM ledgers`,
	`SELECT ctid FROM vouchers`, `SELECT xmin FROM vouchers`, `SELECT tableoid FROM vouchers`, `SELECT oid FROM vouchers`,
	`SELECT x.ledger_name FROM ledgers l`, `SELECT ledger_name FROM ledgers l JOIN postings p ON p.ledger_name = l.ledger_name`,
	// Functions: settings, sleep, files, network, SRFs, sequences, locks, system.
	`SELECT current_setting('app.current_company_id') FROM vouchers`, `SELECT set_config('app.current_company_id', 'x', true) FROM vouchers`,
	`SELECT app_current_company_id() FROM vouchers`, `SELECT pg_sleep(10) FROM vouchers`, `SELECT pg_read_file('/etc/passwd') FROM vouchers`,
	`SELECT lo_import('/etc/passwd') FROM vouchers`, `SELECT dblink('host=x', 'select 1') FROM vouchers`,
	`SELECT generate_series(1, 1000000) FROM vouchers`, `SELECT unnest(ARRAY[1,2]) FROM vouchers`, `SELECT json_each('{}') FROM vouchers`,
	`SELECT regexp_split_to_table('a b', ' ') FROM vouchers`, `SELECT version() FROM vouchers`, `SELECT current_user FROM vouchers`,
	`SELECT session_user FROM vouchers`, `SELECT user FROM vouchers`, `SELECT current_database() FROM vouchers`,
	`SELECT txid_current() FROM vouchers`, `SELECT nextval('x') FROM vouchers`, `SELECT pg_advisory_lock(1) FROM vouchers`,
	`SELECT pg_terminate_backend(1) FROM vouchers`, `SELECT query_to_xml('select * from ledger', true, true, '') FROM vouchers`,
	`SELECT to_regclass('ledger') FROM vouchers`, `SELECT pg_catalog.count(*) FROM vouchers`, `SELECT lower(set_config('a','b',true)) FROM vouchers`,
	`SELECT sum(pg_sleep(1)) FROM vouchers`, `SELECT coalesce(current_setting('x'), '') FROM vouchers`, `SELECT md5(ledger_name) FROM ledgers`,
	`SELECT row_number() OVER () FROM vouchers`, `SELECT sum(amount) OVER (PARTITION BY ledger_name) FROM postings`,
	`SELECT count(*) FILTER (WHERE true) FROM vouchers`, `SELECT string_agg(ledger_name, ',') FROM ledgers`,
	`SELECT array_agg(ledger_name) FROM ledgers`, `SELECT count(*, *) FROM vouchers`, `SELECT sum(*) FROM postings`,
	`SELECT date_trunc('millennium', voucher_date) FROM vouchers`, `SELECT date_trunc(voucher_no, voucher_date) FROM vouchers`,
	`SELECT lower(DISTINCT ledger_name) FROM ledgers`, `SELECT count() FROM vouchers`, `SELECT round(1,2,3) FROM ledgers`,
	// Casts, arrays, rows, operators outside the closed set.
	`SELECT 'ledger'::regclass FROM vouchers`, `SELECT CAST('ledger' AS regclass) FROM vouchers`, `SELECT voucher_no::text FROM vouchers`,
	`SELECT ARRAY[1,2] FROM vouchers`, `SELECT ROW(1,2) FROM vouchers`, `SELECT ledger_name ~ '.*' FROM ledgers`,
	`SELECT ledger_name SIMILAR TO 'a' FROM ledgers`, `SELECT 1 & 2 FROM ledgers`, `SELECT 1 | 2 FROM ledgers`, `SELECT 1 # 2 FROM ledgers`,
	`SELECT 2 ^ 3 FROM ledgers`, `SELECT @ -1 FROM ledgers`, `SELECT ledger_name COLLATE "C" FROM ledgers`,
	`SELECT * FROM ledgers WHERE ledger_name OPERATOR(pg_catalog.=) 'x'`, `SELECT INTERVAL '1 day' FROM ledgers`,
	`SELECT TIMESTAMP '2025-01-01' FROM ledgers`, `SELECT CURRENT_DATE FROM ledgers`, `SELECT now() FROM ledgers`,
	// Lexical tricks.
	`SELECT * FROM "ledger"`, `SELECT "ledger_name" FROM ledgers`, `SELECT * FROM "Vouchers"`, `SELECT E'\x41' FROM ledgers`,
	`SELECT U&'\0041' FROM ledgers`, `SELECT B'101' FROM ledgers`, `SELECT X'1F' FROM ledgers`, `SELECT N'x' FROM ledgers`,
	`SELECT $$x$$ FROM ledgers`, `SELECT $tag$x$tag$ FROM ledgers`, `SELECT * FROM ledgers WHERE ledger_name = $1`,
	`SELECT * FROM ledgers WHERE ledger_name = ?`, `SELECT 'unterminated FROM ledgers`, "SELECT * FROM ledgers WHERE ledger_name = 'a\x00b'",
	`SELECT 1e10 FROM ledgers`, `SELECT 0x1F FROM ledgers`, `SELECT 1..2 FROM ledgers`, `SELECT 12abc FROM ledgers`,
	"SELECT * FROM ledgers WHERE ledger_name = 'é' AND ‘x’ = 'x'", "SELECT * FROM ledgers", `SELECT * FROM ledgers \g`,
	`SELECT * FROM ledgers WHERE ledger_name = 'x' \; DELETE FROM ledger`, "SELECT * FROM ledgers WHERE 1=1\x1a",
	// Keywords in identifier positions and grammar abuse.
	`SELECT from FROM ledgers`, `SELECT ledger_name AS select FROM ledgers`, `SELECT * FROM ledgers AS union`,
	`SELECT * FROM ledgers LIMIT ALL`, `SELECT * FROM ledgers LIMIT -1`, `SELECT * FROM ledgers LIMIT 1.5`,
	`SELECT * FROM ledgers LIMIT 99999999999`, `SELECT * FROM ledgers OFFSET 1 LIMIT 1`, `SELECT * FROM ledgers FETCH FIRST 1 ROWS ONLY`,
	`SELECT * FROM ledgers WINDOW w AS ()`, `SELECT`, ``, `   `, `SELECT 1`, `SELECT version()`, `SELECT * FROM`,
	`SELECT * FROM ledgers WHERE`, `SELECT * FROM ledgers GROUP ledger_name`, `SELECT * FROM ledgers ORDER ledger_name`,
	`SELECT * FROM ledgers WHERE (ledger_name = 'x'`, `SELECT * FROM ledgers WHERE ledger_name = 'x')`,
	`SELECT ledger_name, FROM ledgers`, `SELECT ledger_name ledger_name2 ledger_name3 FROM ledgers`, `SELECT a.* FROM ledgers a`,
	`SELECT * , ledger_name FROM ledgers`, `SELECT ledger_name AS x, group_name AS x FROM ledgers`, `SELECT * FROM ledgers l JOIN ledgers l ON true`,
	`SELECT * FROM ledgers WHERE ledger_name NOT = 'x'`, `SELECT * FROM ledgers WHERE ledger_name IS 'x'`, `SELECT * FROM ledgers WHERE ledger_name IN ()`,
	`SELECT CASE ledger_name WHEN 'a' THEN 1 END FROM ledgers`, `SELECT CASE WHEN true THEN 1 FROM ledgers`, `SELECT CASE END FROM ledgers`,
	`SELECT * FROM ledgers ORDER BY 1 NULLS WHATEVER`, `SELECT * FROM ledgers WHERE DATE 'x' = voucher_date`,
	`SELECT * FROM vouchers WHERE voucher_date = DATE '2025-13-01'`, `SELECT ledger_name FROM ledgers GROUP BY undefined_alias`,
	`SELECT ledger_name AS n FROM ledgers WHERE n = 'x'`, `SELECT voucher_date FROM vouchers v JOIN postings p ON p.voucher_id = v.voucher_id`,
	`SELECT * FROM ledgers WHERE ` + strings.Repeat("(", 60) + `true` + strings.Repeat(")", 60),
	`SELECT ` + strings.Repeat("-", 60) + `1 FROM ledgers`, `SELECT ` + strings.Repeat("NOT ", 60) + `true FROM ledgers`,
	`SELECT * FROM ledgers WHERE ledger_name = '` + strings.Repeat("A", MaxQueryBytes) + `'`,
	// Cross-tenant attempts phrased as SQL.
	`SELECT * FROM vouchers WHERE company_id = '00000000-0000-0000-0000-000000000001'`,
	`SELECT * FROM vouchers v WHERE v.company_id IS NOT NULL`, `SELECT * FROM vouchers /*company*/`,
	`SELECT * FROM vouchers WHERE 1=1 OR current_setting('app.current_company_id') IS NOT NULL`,
	`SELECT * FROM agent.vouchers WHERE true`, `SELECT * FROM vouchers AS "public".ledger`,
}

func TestAdversarialCasesAreRefused(t *testing.T) {
	for _, src := range adversarial {
		if q, err := Parse(src); err == nil {
			t.Errorf("ESCAPE: accepted %q\n  -> %s", src, q.SQL(200))
		} else if !errors.Is(err, ErrRejected) {
			t.Errorf("refused %q with a non-ErrRejected error %v", src, err)
		}
	}
	t.Logf("%d hand-written adversarial cases, all refused", len(adversarial))
}

// payloads are injected into VALID queries at every token boundary, and
// inside string literals. Each result is either refused, or accepted and held
// to invariantsOf — an injected payload that ends up INSIDE a string literal
// is data, and the invariants say so.
var payloads = []string{
	"; DELETE FROM ledger", " -- ", " /* ", " UNION SELECT name FROM ledger ", " OR 1=1 ",
	"' OR '1'='1", "'; DROP TABLE ledger; --", " ) OR ( 1=1 ", " FROM ledger ", " JOIN ledger ON true ",
	" pg_sleep(5) ", " , current_setting('app.current_company_id') ", "::regclass", " $$ ", "\\",
	" WHERE company_id IS NOT NULL ", " INTO x ", " FOR UPDATE ", " LIMIT ALL ", "\"ledger\"",
	" set_config('app.current_company_id','x',true) ", " generate_series(1,9) ", " pg_catalog.pg_user ",
	" (SELECT 1) ", " WITH x AS (SELECT 1) ",
}

// injectedSources splices every payload before every token of every valid
// query (and so also inside string literals, where a token boundary falls
// inside the quotes of the next literal's predecessor).
func injectedSources() []string {
	var out []string
	for _, base := range validQueries {
		toks, err := lex(base)
		if err != nil {
			panic(err)
		}
		for _, tk := range toks {
			for _, pl := range payloads {
				out = append(out, base[:tk.pos]+pl+base[tk.pos:])
			}
		}
	}
	return out
}

func TestInjectedPayloadsNeverEscape(t *testing.T) {
	total, accepted := 0, 0
	for _, src := range injectedSources() {
		total++
		q, err := Parse(src)
		if err != nil {
			continue
		}
		accepted++
		invariantsOf(t, src, q, 200)
	}
	if total < 500 {
		t.Fatalf("only %d injected cases — the suite must be >= 500", total)
	}
	t.Logf("%d injected cases: %d accepted (every one held to the re-emission invariants), %d refused", total, accepted, total-accepted)
}

// TestRandomTokenSoupNeverEscapes: a deterministic generator of queries built
// from a vocabulary heavy in dangerous tokens. Seeded, so a failure
// reproduces.
func TestRandomTokenSoupNeverEscapes(t *testing.T) {
	vocab := []string{"SELECT", "FROM", "WHERE", "vouchers", "ledgers", "ledger", "postings", "pg_class", "*", ",", "(", ")",
		"count", "sum", "current_setting", "pg_sleep", "'x'", "'", "1", "=", "OR", "AND", "NOT", "UNION", "JOIN", "ON",
		"voucher_no", "ledger_name", "company_id", ";", "--", "/*", "::", "$1", "AS", "LIMIT", "10", "GROUP", "BY",
		"ORDER", "DESC", ".", "agent", "public", "WITH", "IN", "IS", "NULL", "CASE", "WHEN", "THEN", "END", "INTO", "\"", "||"}
	r := rand.New(rand.NewSource(20260928))
	n, accepted := 3000, 0
	for i := 0; i < n; i++ {
		k := 3 + r.Intn(14)
		parts := []string{"SELECT"}
		for j := 0; j < k; j++ {
			parts = append(parts, vocab[r.Intn(len(vocab))])
		}
		src := strings.Join(parts, " ")
		q, err := Parse(src)
		if err != nil {
			continue
		}
		accepted++
		invariantsOf(t, src, q, 200)
	}
	t.Logf("%d random token-soup queries: %d accepted and held to the invariants", n, accepted)
}

// TestGrammarShapedRandomQueriesNeverEscape: token soup is almost always
// refused at the first token, which proves little. This generator builds
// queries that are MOSTLY well-formed — a real view, real columns, real
// operators — and splices a dangerous element into a random slot, so a large
// share parse and the invariants are exercised on accepted SQL.
func grammarSources(seed int64, n int) []string {
	r := rand.New(rand.NewSource(seed))
	var out []string
	pick := func(xs []string) string { return xs[r.Intn(len(xs))] }
	danger := []string{"ledger", "pg_class", "current_setting('x')", "company_id", "(SELECT 1)", "'a'';DROP TABLE x;--'",
		"pg_sleep(1)", "generate_series(1,2)", "voucher_no::text", "\"x\"", "$1", "agent.vouchers", "set_config('a','b',true)",
		"'--'", "'/*'", "';'", "'\\'", "''''", "count(*)", "sum(amount)", "1", "'x'", "NULL", "TRUE", "DATE '2025-04-01'"}
	ops := []string{"=", "<>", "<", ">=", "LIKE", "ILIKE", "IS NULL", "IS NOT NULL", "IN ('a','b')", "BETWEEN 1 AND 2"}
	for i := 0; i < n; i++ {
		v := Views[r.Intn(len(Views))]
		col := func() string {
			if r.Intn(8) == 0 {
				return pick(danger)
			}
			return pick(v.Columns)
		}
		var b strings.Builder
		b.WriteString("SELECT ")
		for k := 0; k <= r.Intn(3); k++ {
			if k > 0 {
				b.WriteString(", ")
			}
			switch r.Intn(4) {
			case 0:
				b.WriteString("count(*)")
			case 1:
				b.WriteString(pick([]string{"sum", "min", "max", "lower", "abs", "md5", "pg_sleep"}) + "(" + col() + ")")
			default:
				b.WriteString(col())
			}
		}
		b.WriteString(" FROM " + v.Name)
		if r.Intn(10) == 0 {
			b.WriteString(pick([]string{" x JOIN ledger ON true", ", pg_class", " UNION SELECT 1", ";", " --", " JOIN items i ON i.item_name = " + col()}))
		}
		if r.Intn(2) == 0 {
			op := pick(ops)
			b.WriteString(" WHERE " + col() + " " + op)
			if !strings.Contains(op, "NULL") && !strings.Contains(op, "(") && !strings.Contains(op, "BETWEEN") {
				b.WriteString(" " + pick(danger))
			}
		}
		if r.Intn(3) == 0 {
			b.WriteString(" LIMIT " + pick([]string{"1", "10", "100000", "ALL", "-1", "1; DROP TABLE x"}))
		}
		out = append(out, b.String())
	}
	return out
}

func TestGrammarShapedRandomQueriesNeverEscape(t *testing.T) {
	srcs := grammarSources(7, 4000)
	n, accepted := len(srcs), 0
	for _, src := range srcs {
		q, err := Parse(src)
		if err != nil {
			continue
		}
		accepted++
		invariantsOf(t, src, q, 200)
	}
	if accepted < 200 {
		t.Fatalf("only %d of %d generated queries parsed — the generator is not exercising the accept path", accepted, n)
	}
	t.Logf("%d grammar-shaped random queries: %d accepted and held to the invariants, %d refused", n, accepted, n-accepted)
}

// FuzzParse is the native fuzz target (`go test -fuzz FuzzParse
// ./internal/tierb/`); in a normal run it replays its seed corpus.
func FuzzParse(f *testing.F) {
	for _, s := range append(append([]string{}, validQueries...), adversarial...) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		q, err := Parse(src)
		if err != nil {
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("non-ErrRejected error: %v", err)
			}
			return
		}
		invariantsOf(t, src, q, 200)
	})
}
