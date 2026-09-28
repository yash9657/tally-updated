package tierb

import (
	"strconv"
	"strings"
)

// parse.go is the accepted grammar, and it is the whole of it:
//
//	query    := SELECT [DISTINCT] items FROM source [WHERE expr]
//	            [GROUP BY expr {, expr}] [HAVING expr]
//	            [ORDER BY oitem {, oitem}] [LIMIT int] [OFFSET int]
//	items    := '*' | item {, item}          item := expr [[AS] alias]
//	source   := view [[AS] alias] { [INNER | LEFT [OUTER]] JOIN view [[AS] alias] ON expr }
//	oitem    := expr [ASC | DESC] [NULLS (FIRST | LAST)]
//	expr     := or ;  or := and {OR and} ;  and := not {AND not} ;  not := NOT not | pred
//	pred     := sum [ cmp sum | [NOT] BETWEEN sum AND sum | [NOT] IN ( expr {, expr} )
//	                  | IS [NOT] NULL | [NOT] (LIKE | ILIKE) sum ]
//	sum      := prod {(+ | - | ||) prod} ;  prod := unary {(* | / | %) unary} ;  unary := - unary | prim
//	prim     := number | 'string' | NULL | TRUE | FALSE | DATE 'yyyy-mm-dd'
//	          | [alias .] column | fn ( [DISTINCT] expr {, expr} | * ) | ( expr )
//	          | CASE WHEN expr THEN expr {WHEN expr THEN expr} [ELSE expr] END
//
// Anything else — a subquery, a CTE, a set operation, a window, a cast, an
// array, a comma join, a schema-qualified name, a keyword in an identifier
// position, a trailing token — is a refusal.

// Expr is a node of the validated tree. Only the printer turns it into SQL.
type Expr interface{ exprNode() }

type (
	litNum  struct{ v string }
	litStr  struct{ v string }
	litNull struct{}
	litBool struct{ v bool }
	litDate struct{ v string }
	colRef  struct{ alias, col string }
	outRef  struct{ name string } // an output alias in GROUP BY / ORDER BY
	star    struct{}
	unary   struct{ x Expr }
	binary  struct {
		op   string
		l, r Expr
	}
	notE    struct{ x Expr }
	between struct {
		x, lo, hi Expr
		not       bool
	}
	inList struct {
		x    Expr
		list []Expr
		not  bool
	}
	isNull struct {
		x   Expr
		not bool
	}
	like struct {
		x, pat     Expr
		not, ilike bool
	}
	funcCall struct {
		name     string
		distinct bool
		star     bool
		args     []Expr
	}
	caseE struct {
		whens [][2]Expr
		els   Expr
	}
)

func (litNum) exprNode()   {}
func (litStr) exprNode()   {}
func (litNull) exprNode()  {}
func (litBool) exprNode()  {}
func (litDate) exprNode()  {}
func (colRef) exprNode()   {}
func (outRef) exprNode()   {}
func (star) exprNode()     {}
func (unary) exprNode()    {}
func (binary) exprNode()   {}
func (notE) exprNode()     {}
func (between) exprNode()  {}
func (inList) exprNode()   {}
func (isNull) exprNode()   {}
func (like) exprNode()     {}
func (funcCall) exprNode() {}
func (caseE) exprNode()    {}

type selItem struct {
	x     Expr
	alias string
}

type fromItem struct {
	view, alias string
	join        string // "" for the first, "inner" | "left"
	on          Expr
}

type orderItem struct {
	x     Expr
	desc  bool
	nulls string // "" | "first" | "last"
}

// Query is a validated Tier-B query.
type Query struct {
	distinct bool
	all      bool // SELECT *
	items    []selItem
	from     []fromItem
	where    Expr
	groupBy  []Expr
	having   Expr
	orderBy  []orderItem
	limit    int // -1 = none given
	offset   int // -1 = none given
}

type parser struct {
	toks    []token
	i       int
	depth   int
	aliases map[string]string // alias -> view
	order   []string          // aliases in FROM order
	outs    map[string]bool   // output aliases
}

// Parse validates src and returns the tree. It is the ONLY way to obtain a
// Query, so a Query value is proof the text passed.
func Parse(src string) (*Query, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, aliases: map[string]string{}, outs: map[string]bool{}}
	q, err := p.query()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, reject("unexpected %q after the end of the query (one SELECT, nothing after LIMIT/OFFSET)", p.peek().text)
	}
	return q, nil
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }
func (p *parser) isKw(w string) bool {
	t := p.peek()
	return t.kind == tIdent && t.text == w
}
func (p *parser) acceptKw(w string) bool {
	if p.isKw(w) {
		p.i++
		return true
	}
	return false
}
func (p *parser) expectKw(w string) error {
	if !p.acceptKw(w) {
		return reject("expected %s near %q", strings.ToUpper(w), p.peek().text)
	}
	return nil
}
func (p *parser) isOp(o string) bool { t := p.peek(); return t.kind == tOp && t.text == o }
func (p *parser) acceptOp(o string) bool {
	if p.isOp(o) {
		p.i++
		return true
	}
	return false
}
func (p *parser) expectOp(o string) error {
	if !p.acceptOp(o) {
		return reject("expected %q near %q", o, p.peek().text)
	}
	return nil
}

// ident reads a NON-reserved identifier.
func (p *parser) ident(what string) (string, error) {
	t := p.peek()
	if t.kind != tIdent {
		return "", reject("expected %s near %q", what, t.text)
	}
	if reserved[t.text] {
		return "", reject("%q is a reserved word and cannot be a %s", t.text, what)
	}
	p.i++
	return t.text, nil
}

func (p *parser) query() (*Query, error) {
	if !p.acceptKw("select") {
		return nil, reject("the query must be a single SELECT")
	}
	q := &Query{limit: -1, offset: -1}
	q.distinct = p.acceptKw("distinct")

	// Items are parsed AFTER the FROM clause is known, because a column is
	// resolved against the views it names. Remember where they start, skip
	// to FROM at paren depth 0, parse FROM, then come back.
	itemsStart := p.i
	depth := 0
	for {
		t := p.peek()
		if t.kind == tEOF {
			return nil, reject("a FROM clause naming a published view is required")
		}
		if t.kind == tOp && t.text == "(" {
			depth++
		}
		if t.kind == tOp && t.text == ")" {
			depth--
		}
		if depth == 0 && t.kind == tIdent && t.text == "from" {
			break
		}
		p.i++
	}
	itemsEnd := p.i
	p.i++ // FROM
	if err := p.source(q); err != nil {
		return nil, err
	}
	afterFrom := p.i

	// Back to the select list.
	p.i = itemsStart
	if p.acceptOp("*") {
		q.all = true
	} else {
		for {
			x, err := p.expr(false)
			if err != nil {
				return nil, err
			}
			it := selItem{x: x}
			if p.acceptKw("as") {
				if it.alias, err = p.ident("column alias"); err != nil {
					return nil, err
				}
			} else if t := p.peek(); t.kind == tIdent && !reserved[t.text] {
				it.alias = t.text
				p.i++
			}
			if it.alias != "" {
				if p.outs[it.alias] {
					return nil, reject("duplicate output alias %q", it.alias)
				}
				p.outs[it.alias] = true
			}
			q.items = append(q.items, it)
			if !p.acceptOp(",") {
				break
			}
		}
	}
	if p.i != itemsEnd {
		return nil, reject("unexpected %q in the select list", p.peek().text)
	}
	p.i = afterFrom

	var err error
	if p.acceptKw("where") {
		if q.where, err = p.expr(false); err != nil {
			return nil, err
		}
	}
	if p.acceptKw("group") {
		if err := p.expectKw("by"); err != nil {
			return nil, err
		}
		for {
			x, err := p.expr(true)
			if err != nil {
				return nil, err
			}
			q.groupBy = append(q.groupBy, x)
			if !p.acceptOp(",") {
				break
			}
		}
	}
	if p.acceptKw("having") {
		if q.having, err = p.expr(false); err != nil {
			return nil, err
		}
	}
	if p.acceptKw("order") {
		if err := p.expectKw("by"); err != nil {
			return nil, err
		}
		for {
			x, err := p.expr(true)
			if err != nil {
				return nil, err
			}
			oi := orderItem{x: x}
			if p.acceptKw("desc") {
				oi.desc = true
			} else {
				p.acceptKw("asc")
			}
			if p.acceptKw("nulls") {
				switch {
				case p.acceptKw("first"):
					oi.nulls = "first"
				case p.acceptKw("last"):
					oi.nulls = "last"
				default:
					return nil, reject("NULLS must be followed by FIRST or LAST")
				}
			}
			q.orderBy = append(q.orderBy, oi)
			if !p.acceptOp(",") {
				break
			}
		}
	}
	if p.acceptKw("limit") {
		if q.limit, err = p.intLit("LIMIT"); err != nil {
			return nil, err
		}
	}
	if p.acceptKw("offset") {
		if q.offset, err = p.intLit("OFFSET"); err != nil {
			return nil, err
		}
	}
	return q, nil
}

func (p *parser) intLit(what string) (int, error) {
	t := p.next()
	if t.kind != tNumber || strings.Contains(t.text, ".") {
		return 0, reject("%s takes a whole number", what)
	}
	n, err := strconv.Atoi(t.text)
	if err != nil || n < 0 || n > 1_000_000_000 {
		return 0, reject("%s out of range", what)
	}
	return n, nil
}

func (p *parser) source(q *Query) error {
	add := func(join string) error {
		v, err := p.ident("view name")
		if err != nil {
			return err
		}
		if p.isOp(".") {
			return reject("schema-qualified names are not allowed; name a published view: %s", strings.Join(viewNames(), ", "))
		}
		if _, ok := viewCols[v]; !ok {
			return reject("%q is not a published view; the views are: %s", v, strings.Join(viewNames(), ", "))
		}
		alias := v
		if p.acceptKw("as") {
			if alias, err = p.ident("table alias"); err != nil {
				return err
			}
		} else if t := p.peek(); t.kind == tIdent && !reserved[t.text] {
			alias = t.text
			p.i++
		}
		if _, dup := p.aliases[alias]; dup {
			return reject("duplicate table alias %q", alias)
		}
		p.aliases[alias] = v
		p.order = append(p.order, alias)
		q.from = append(q.from, fromItem{view: v, alias: alias, join: join})
		return nil
	}
	if err := add(""); err != nil {
		return err
	}
	for {
		var join string
		switch {
		case p.acceptKw("join"):
			join = "inner"
		case p.acceptKw("inner"):
			if err := p.expectKw("join"); err != nil {
				return err
			}
			join = "inner"
		case p.acceptKw("left"):
			p.acceptKw("outer")
			if err := p.expectKw("join"); err != nil {
				return err
			}
			join = "left"
		case p.isOp(","):
			return reject("comma joins are not allowed; use JOIN ... ON")
		default:
			return nil
		}
		if err := add(join); err != nil {
			return err
		}
		if err := p.expectKw("on"); err != nil {
			return err
		}
		on, err := p.expr(false)
		if err != nil {
			return err
		}
		q.from[len(q.from)-1].on = on
	}
}

func viewNames() []string {
	out := make([]string, len(Views))
	for i, v := range Views {
		out[i] = v.Name
	}
	return out
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return reject("expression nested deeper than %d", maxDepth)
	}
	return nil
}

// expr parses an expression; allowOut permits a bare output alias (GROUP BY /
// ORDER BY, exactly where Postgres allows one).
func (p *parser) expr(allowOut bool) (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	l, err := p.and(allowOut)
	if err != nil {
		return nil, err
	}
	for p.acceptKw("or") {
		r, err := p.and(allowOut)
		if err != nil {
			return nil, err
		}
		l = binary{"OR", l, r}
	}
	return l, nil
}

func (p *parser) and(allowOut bool) (Expr, error) {
	l, err := p.not(allowOut)
	if err != nil {
		return nil, err
	}
	for p.acceptKw("and") {
		r, err := p.not(allowOut)
		if err != nil {
			return nil, err
		}
		l = binary{"AND", l, r}
	}
	return l, nil
}

func (p *parser) not(allowOut bool) (Expr, error) {
	if p.acceptKw("not") {
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		x, err := p.not(allowOut)
		if err != nil {
			return nil, err
		}
		return notE{x}, nil
	}
	return p.pred(allowOut)
}

var cmpOps = map[string]string{"=": "=", "<>": "<>", "!=": "<>", "<": "<", ">": ">", "<=": "<=", ">=": ">="}

func (p *parser) pred(allowOut bool) (Expr, error) {
	x, err := p.sum(allowOut)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind == tOp {
		if op, ok := cmpOps[t.text]; ok {
			p.i++
			r, err := p.sum(allowOut)
			if err != nil {
				return nil, err
			}
			return binary{op, x, r}, nil
		}
	}
	neg := p.acceptKw("not")
	switch {
	case p.acceptKw("between"):
		lo, err := p.sum(allowOut)
		if err != nil {
			return nil, err
		}
		if err := p.expectKw("and"); err != nil {
			return nil, err
		}
		hi, err := p.sum(allowOut)
		if err != nil {
			return nil, err
		}
		return between{x, lo, hi, neg}, nil
	case p.acceptKw("in"):
		if err := p.expectOp("("); err != nil {
			return nil, err
		}
		var list []Expr
		for {
			e, err := p.expr(false)
			if err != nil {
				return nil, err
			}
			list = append(list, e)
			if !p.acceptOp(",") {
				break
			}
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		return inList{x, list, neg}, nil
	case p.isKw("like") || p.isKw("ilike"):
		il := p.next().text == "ilike"
		pat, err := p.sum(allowOut)
		if err != nil {
			return nil, err
		}
		return like{x, pat, neg, il}, nil
	case !neg && p.acceptKw("is"):
		n := p.acceptKw("not")
		if err := p.expectKw("null"); err != nil {
			return nil, err
		}
		return isNull{x, n}, nil
	}
	if neg {
		return nil, reject("NOT must be followed by BETWEEN, IN, LIKE or ILIKE here")
	}
	return x, nil
}

func (p *parser) sum(allowOut bool) (Expr, error) {
	l, err := p.prod(allowOut)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tOp && (t.text == "+" || t.text == "-" || t.text == "||") {
			p.i++
			r, err := p.prod(allowOut)
			if err != nil {
				return nil, err
			}
			l = binary{t.text, l, r}
			continue
		}
		return l, nil
	}
}

func (p *parser) prod(allowOut bool) (Expr, error) {
	l, err := p.unaryE(allowOut)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tOp && (t.text == "*" || t.text == "/" || t.text == "%") {
			p.i++
			r, err := p.unaryE(allowOut)
			if err != nil {
				return nil, err
			}
			l = binary{t.text, l, r}
			continue
		}
		return l, nil
	}
}

func (p *parser) unaryE(allowOut bool) (Expr, error) {
	if p.acceptOp("-") {
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		x, err := p.unaryE(allowOut)
		if err != nil {
			return nil, err
		}
		return unary{x}, nil
	}
	return p.prim(allowOut)
}

func (p *parser) prim(allowOut bool) (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tNumber:
		p.i++
		return litNum{t.text}, nil
	case tString:
		p.i++
		return litStr{t.text}, nil
	case tOp:
		if t.text == "(" {
			p.i++
			if p.isKw("select") {
				return nil, reject("subqueries are not allowed")
			}
			x, err := p.expr(allowOut)
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return x, nil
		}
		return nil, reject("unexpected %q", t.text)
	case tEOF:
		return nil, reject("unexpected end of query")
	}
	// Identifier-shaped.
	switch t.text {
	case "null":
		p.i++
		return litNull{}, nil
	case "true", "false":
		p.i++
		return litBool{t.text == "true"}, nil
	case "date":
		p.i++
		s := p.next()
		if s.kind != tString || !isISODate(s.text) {
			return nil, reject("DATE takes a 'YYYY-MM-DD' literal")
		}
		return litDate{s.text}, nil
	case "case":
		p.i++
		return p.caseExpr(allowOut)
	}
	if reserved[t.text] {
		return nil, reject("%q is not allowed here", t.text)
	}
	p.i++
	name := t.text
	// Function call?
	if p.isOp("(") {
		return p.call(name)
	}
	// alias.column?
	if p.acceptOp(".") {
		c := p.next()
		if c.kind != tIdent || reserved[c.text] {
			return nil, reject("expected a column after %q.", name)
		}
		if p.isOp(".") || p.isOp("(") {
			return nil, reject("only alias.column references are allowed")
		}
		view, ok := p.aliases[name]
		if !ok {
			return nil, reject("%q is not a table alias in FROM", name)
		}
		if !viewCols[view][c.text] {
			return nil, reject("view %s has no column %q", view, c.text)
		}
		return colRef{name, c.text}, nil
	}
	// Bare column: exactly one FROM view must have it.
	var found []string
	for _, a := range p.order {
		if viewCols[p.aliases[a]][name] {
			found = append(found, a)
		}
	}
	switch len(found) {
	case 1:
		return colRef{found[0], name}, nil
	case 0:
		if allowOut && p.outs[name] {
			return outRef{name}, nil
		}
		return nil, reject("no published view in FROM has a column %q", name)
	default:
		return nil, reject("column %q is ambiguous; qualify it with an alias", name)
	}
}

func (p *parser) call(name string) (Expr, error) {
	ar, ok := Functions[name]
	if !ok {
		return nil, reject("function %q is not allowed; allowed: %s", name, strings.Join(FunctionNames(), ", "))
	}
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	p.i++ // (
	f := funcCall{name: name}
	if p.acceptOp("*") {
		if name != "count" {
			return nil, reject("only count(*) takes *")
		}
		f.star = true
	} else if !p.isOp(")") {
		if p.acceptKw("distinct") {
			f.distinct = true
		}
		for {
			e, err := p.expr(false)
			if err != nil {
				return nil, err
			}
			f.args = append(f.args, e)
			if !p.acceptOp(",") {
				break
			}
		}
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	n := len(f.args)
	if f.star {
		n = 0
	}
	if n < ar[0] || (ar[1] >= 0 && n > ar[1]) || (name == "count" && n == 0 && !f.star) {
		return nil, reject("%s takes %d..%d arguments", name, ar[0], ar[1])
	}
	if f.distinct && !isAggregate(name) {
		return nil, reject("DISTINCT is only allowed inside an aggregate")
	}
	if name == "date_trunc" || name == "date_part" {
		s, ok := f.args[0].(litStr)
		if !ok || !dateFields[strings.ToLower(s.v)] {
			return nil, reject("%s's first argument must be one of 'day','week','month','quarter','year','dow'", name)
		}
	}
	if p.isKw("over") || p.isKw("filter") || p.isKw("within") {
		return nil, reject("window and FILTER clauses are not allowed")
	}
	return f, nil
}

func isAggregate(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max":
		return true
	}
	return false
}

func (p *parser) caseExpr(allowOut bool) (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	var c caseE
	for p.acceptKw("when") {
		w, err := p.expr(allowOut)
		if err != nil {
			return nil, err
		}
		if err := p.expectKw("then"); err != nil {
			return nil, err
		}
		th, err := p.expr(allowOut)
		if err != nil {
			return nil, err
		}
		c.whens = append(c.whens, [2]Expr{w, th})
	}
	if len(c.whens) == 0 {
		return nil, reject("CASE needs at least one WHEN (simple CASE x WHEN is not supported)")
	}
	if p.acceptKw("else") {
		e, err := p.expr(allowOut)
		if err != nil {
			return nil, err
		}
		c.els = e
	}
	if err := p.expectKw("end"); err != nil {
		return nil, err
	}
	return c, nil
}

func isISODate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i := 0; i < 10; i++ {
		if i == 4 || i == 7 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	m, _ := strconv.Atoi(s[5:7])
	d, _ := strconv.Atoi(s[8:10])
	return m >= 1 && m <= 12 && d >= 1 && d <= 31
}
