package lang

import (
	"fmt"
	"strings"
)

// Error is an error with a position.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

// Parser is a recursive descent parser.
type Parser struct {
	toks     []Token
	i        int
	lines    []string
	Warnings []string
}

// Parse parses a file into a list of statements and declarations.
func Parse(file, src string) ([]Stmt, []string, error) {
	toks, err := Lex(file, src)
	if err != nil {
		return nil, nil, err
	}
	p := &Parser{toks: toks, lines: strings.Split(src, "\n")}
	var out []Stmt
	err = p.catch(func() {
		out = p.block(nil)
		if p.tok().Kind != EOF {
			p.fail(p.tok().Pos, "unexpected %q", p.tok().Text)
		}
	})
	return out, p.Warnings, err
}

type bailout struct{ err error }

func (p *Parser) catch(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if b, ok := r.(bailout); ok {
				err = b.err
				return
			}
			panic(r)
		}
	}()
	f()
	return nil
}

func (p *Parser) fail(pos Pos, f string, a ...any) {
	panic(bailout{&Error{pos, fmt.Sprintf(f, a...)}})
}

func (p *Parser) warn(pos Pos, f string, a ...any) {
	p.Warnings = append(p.Warnings, fmt.Sprintf("%s: warning: %s", pos, fmt.Sprintf(f, a...)))
}

func (p *Parser) tok() Token  { return p.toks[p.i] }
func (p *Parser) peek() Token { return p.toks[min(p.i+1, len(p.toks)-1)] }
func (p *Parser) next() Token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *Parser) is(kind TokKind, text string) bool {
	t := p.tok()
	return t.Kind == kind && t.Text == text
}
func (p *Parser) isOp(s string) bool { return p.is(OP, s) }
func (p *Parser) isKw(s string) bool { return p.is(KEYWORD, s) }

func (p *Parser) expectOp(s string) Token {
	if !p.isOp(s) {
		p.fail(p.tok().Pos, "expected %q, found %q", s, p.desc())
	}
	return p.next()
}

func (p *Parser) expectKw(s string) Token {
	if !p.isKw(s) {
		p.fail(p.tok().Pos, "expected %q, found %q", s, p.desc())
	}
	return p.next()
}

func (p *Parser) desc() string {
	t := p.tok()
	switch t.Kind {
	case NEWLINE:
		return "end of line"
	case EOF:
		return "end of file"
	}
	return t.Text
}

func (p *Parser) ident() Token {
	if p.tok().Kind != IDENT {
		p.fail(p.tok().Pos, "expected a name, found %q", p.desc())
	}
	return p.next()
}

func (p *Parser) skipNL() {
	for p.tok().Kind == NEWLINE {
		p.next()
	}
}

var terminators = map[string]bool{"fi": true, "od": true, "elif": true, "else": true, "endproc": true, "until": true, "endatomic": true, "endasm": true}

func (p *Parser) endOfStmt() {
	t := p.tok()
	if t.Kind == NEWLINE || t.Kind == EOF || (t.Kind == KEYWORD && terminators[t.Text]) {
		return
	}
	p.fail(t.Pos, "unexpected %q after statement", p.desc())
}

// block reads statements until it reaches a keyword from ends (or EOF when ends == nil).
func (p *Parser) block(ends map[string]bool) []Stmt {
	var out []Stmt
	for {
		p.skipNL()
		t := p.tok()
		if t.Kind == EOF {
			if ends != nil {
				var names []string
				for _, k := range []string{"fi", "od", "endproc", "endatomic", "endasm"} {
					if ends[k] {
						names = append(names, k)
					}
				}
				p.fail(t.Pos, "unexpected end of file, missing %s", strings.Join(names, " or "))
			}
			return out
		}
		if t.Kind == KEYWORD && terminators[t.Text] {
			if ends != nil && ends[t.Text] {
				return out
			}
			p.fail(t.Pos, "unexpected %q", t.Text)
		}
		out = append(out, p.stmt()...)
		p.endOfStmt()
	}
}

func (p *Parser) stmt() []Stmt {
	t := p.tok()
	switch t.Kind {
	case DIRECTIVE:
		return []Stmt{p.directive()}
	case IDENT:
		return []Stmt{p.simpleStmt()}
	case KEYWORD:
		switch t.Text {
		case "byte", "card", "string", "ring":
			return p.varDecl(false)
		case "const":
			p.next()
			if p.isKw("byte") || p.isKw("card") || p.isKw("string") {
				return p.varDecl(true)
			}
			name := p.ident()
			p.expectOp("=")
			return []Stmt{&ConstDecl{Pos: t.Pos, Name: name.Text, X: p.expr()}}
		case "bit", "bits":
			return []Stmt{p.bitDecl()}
		case "proc", "interrupt", "extern", "irqsafe":
			return []Stmt{p.procDecl()}
		case "if":
			return []Stmt{p.ifStmt()}
		case "do":
			p.next()
			body := p.block(map[string]bool{"od": true, "until": true})
			l := &Loop{Pos: t.Pos, Body: body}
			if p.isKw("until") {
				p.next()
				l.Until = p.expr()
				p.skipNL()
			}
			p.expectKw("od")
			return []Stmt{l}
		case "while":
			p.next()
			c := p.expr()
			p.expectKw("do")
			body := p.block(map[string]bool{"od": true})
			p.expectKw("od")
			return []Stmt{&While{Pos: t.Pos, Cond: c, Body: body}}
		case "for":
			return []Stmt{p.forStmt()}
		case "exit":
			p.next()
			return []Stmt{&Exit{Pos: t.Pos}}
		case "return":
			p.next()
			r := &Return{Pos: t.Pos}
			if p.tok().Kind != NEWLINE && !(p.tok().Kind == KEYWORD && terminators[p.tok().Text]) {
				r.X = p.expr()
			}
			return []Stmt{r}
		case "wait":
			return []Stmt{&ExprStmt{Pos: t.Pos, X: p.waitExpr(true)}}
		case "delay":
			p.next()
			d := &Delay{Pos: t.Pos, X: p.mulExpr()}
			u := p.ident()
			switch u.Text {
			case "us", "ms", "s", "cycles":
				d.Unit = u.Text
			default:
				p.fail(u.Pos, "delay unit must be us, ms, s or cycles")
			}
			return []Stmt{d}
		case "asm":
			return []Stmt{p.asmBlock()}
		case "atomic":
			p.next()
			body := p.block(map[string]bool{"endatomic": true})
			p.expectKw("endatomic")
			return []Stmt{&Atomic{Pos: t.Pos, Body: body}}
		}
	}
	p.fail(t.Pos, "unexpected %q", p.desc())
	return nil
}

func (p *Parser) directive() Stmt {
	t := p.next()
	d := &Directive{Pos: t.Pos, Name: t.Text}
	switch t.Text {
	case "org", "end", "vars":
		d.Args = []Expr{p.expr()}
	case "zp":
		w := p.ident()
		if w.Text != "exclude" {
			p.fail(w.Pos, "expected .zp exclude")
		}
		d.Text = "exclude"
		lo := p.unary()
		hi := lo
		if p.isOp("-") {
			p.next()
			hi = p.unary()
		}
		d.Args = []Expr{lo, hi}
	case "include":
		s := p.next()
		if s.Kind != STRING {
			p.fail(s.Pos, "expected a file name in quotes")
		}
		d.Text = s.Text
	case "target", "cpu", "timeout":
		s := p.next()
		d.Text = strings.ToLower(s.Text)
	case "cycles":
		if p.isOp("<=") {
			p.next()
			d.Args = []Expr{p.expr()}
		}
	default:
		p.fail(t.Pos, "unknown directive .%s", t.Text)
	}
	return d
}

// typeSpec reads byte/card [pointer] [ '[' len ']' ], string [ '[' len ']' ], ring byte[N].
func (p *Parser) typeSpec() TypeSpec {
	var ts TypeSpec
	if p.isKw("ring") {
		p.next()
		ts.Ring = true
	}
	t := p.next()
	switch t.Text {
	case "byte", "card":
		ts.Base = t.Text
	case "string":
		ts.Base = "byte"
		ts.String = true
	default:
		p.fail(t.Pos, "expected a type")
	}
	if p.isKw("pointer") {
		p.next()
		ts.Pointer = true
		return ts
	}
	if p.isOp("[") {
		p.next()
		ts.Array = true
		if !p.isOp("]") {
			ts.LenExpr = p.expr()
		}
		p.expectOp("]")
	} else if ts.String {
		ts.Array = true
	}
	if ts.Ring && (!ts.Array || ts.LenExpr == nil) {
		p.fail(t.Pos, "ring needs a size, e.g. ring byte[256] log")
	}
	return ts
}

func (p *Parser) qualifiers(d *VarDecl) {
	for p.tok().Kind == KEYWORD {
		switch p.tok().Text {
		case "readonly":
			d.ReadOnly = true
		case "writeonly":
			d.WriteOnly = true
		case "shadow":
			d.Shadow = true
		case "noinit":
			d.NoInit = true
		default:
			return
		}
		p.next()
	}
}

func (p *Parser) varDecl(isConst bool) []Stmt {
	pos := p.tok().Pos
	ts := p.typeSpec()
	var out []Stmt
	for {
		name := p.ident()
		d := &VarDecl{Pos: name.Pos, Name: name.Text, Type: ts, Const: isConst}
		if p.isOp("@") {
			p.next()
			d.Addr = p.addExpr()
		}
		p.qualifiers(d)
		if p.isOp("=") {
			p.next()
			if ts.Array {
				if p.tok().Kind == IDENT && p.tok().Text == "file" && p.peek().Kind == STRING {
					p.next()
					d.File = p.next().Text
				} else {
					d.Init = p.valueList()
				}
			} else {
				d.Init = []Expr{p.expr()}
			}
			p.qualifiers(d)
		}
		out = append(out, d)
		if ts.Array || !p.isOp(",") {
			break
		}
		p.next()
		p.skipNL()
	}
	_ = pos
	return out
}

func (p *Parser) valueList() []Expr {
	var vals []Expr
	for {
		vals = append(vals, p.expr())
		if !p.isOp(",") {
			return vals
		}
		p.next()
		p.skipNL()
	}
}

func (p *Parser) bitDecl() Stmt {
	kw := p.next()
	name := p.ident()
	d := &BitDecl{Pos: name.Pos, Name: name.Text}
	if !p.isOp("=") {
		if kw.Text == "bits" {
			p.fail(name.Pos, "bits needs a register, e.g. bits MODE = PAD.4..6")
		}
		return d
	}
	p.next()
	if p.tok().Kind == NUMBER {
		// bit READY = 1 : flag in RAM with an initial value
		d.Init = p.expr()
		return d
	}
	reg := p.ident()
	p.expectOp(".")
	lo := p.next()
	if lo.Kind != NUMBER || lo.Num > 7 {
		p.fail(lo.Pos, "expected a bit number 0-7")
	}
	d.Reg, d.Lo, d.Hi = reg.Text, lo.Num, lo.Num
	if p.isOp("..") {
		p.next()
		hi := p.next()
		if hi.Kind != NUMBER || hi.Num > 7 || hi.Num < lo.Num {
			p.fail(hi.Pos, "invalid bit range")
		}
		d.Hi = hi.Num
	}
	if kw.Text == "bit" && d.Hi != d.Lo {
		p.fail(name.Pos, "use bits for a bit range")
	}
	return d
}

func (p *Parser) procDecl() Stmt {
	start := p.tok()
	d := &ProcDecl{Pos: start.Pos}
	isExtern := false
	switch {
	case p.isKw("interrupt"):
		p.next()
		k := p.ident()
		if k.Text != "irq" && k.Text != "nmi" {
			p.fail(k.Pos, "expected irq or nmi")
		}
		d.Interrupt = k.Text
	case p.isKw("extern"):
		p.next()
		isExtern = true
	case p.isKw("irqsafe"):
		p.next()
		d.IrqSafe = true
	}
	p.expectKw("proc")
	d.Name = p.ident().Text
	p.expectOp("(")
	for !p.isOp(")") {
		ts := p.typeSpec()
		pn := p.ident()
		prm := Param{Name: pn.Text, Type: ts}
		if p.isOp("@") {
			p.next()
			r := p.ident()
			prm.Reg = strings.ToUpper(r.Text)
		}
		d.Params = append(d.Params, prm)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	p.expectOp(")")
	if p.isOp(":") {
		p.next()
		rt := p.next()
		if rt.Text != "byte" && rt.Text != "card" {
			p.fail(rt.Pos, "return type must be byte or card")
		}
		d.Ret = rt.Text
		if p.isOp("@") && p.peek().Kind == IDENT && len(p.peek().Text) == 1 {
			p.next()
			d.RetReg = strings.ToUpper(p.next().Text)
		}
	}
	if isExtern {
		p.expectOp("@")
		d.Extern = p.addExpr()
		return d
	}
	d.Body = p.block(map[string]bool{"endproc": true})
	d.EndPos = p.tok().Pos
	p.expectKw("endproc")
	return d
}

func (p *Parser) ifStmt() Stmt {
	t := p.next()
	s := &If{Pos: t.Pos}
	ends := map[string]bool{"elif": true, "else": true, "fi": true}
	s.Conds = append(s.Conds, p.expr())
	p.expectKw("then")
	s.Blocks = append(s.Blocks, p.block(ends))
	for p.isKw("elif") {
		p.next()
		s.Conds = append(s.Conds, p.expr())
		p.expectKw("then")
		s.Blocks = append(s.Blocks, p.block(ends))
	}
	if p.isKw("else") {
		p.next()
		s.Else = p.block(map[string]bool{"fi": true})
	}
	p.expectKw("fi")
	return s
}

func (p *Parser) forStmt() Stmt {
	t := p.next()
	v := p.ident()
	p.expectOp("=")
	s := &For{Pos: t.Pos, Var: v.Text, From: p.expr()}
	switch {
	case p.isKw("to"):
		p.next()
	case p.isKw("until"):
		p.next()
		s.Exclusive = true
	default:
		p.fail(p.tok().Pos, "expected to or until")
	}
	s.To = p.expr()
	if p.isKw("step") {
		p.next()
		s.Step = p.expr()
	}
	p.expectKw("do")
	s.Body = p.block(map[string]bool{"od": true})
	p.expectKw("od")
	return s
}

func (p *Parser) asmBlock() Stmt {
	t := p.next()
	start := t.Pos.Line
	end := -1
	for ln := start; ln < len(p.lines); ln++ {
		if strings.TrimSpace(strings.SplitN(p.lines[ln], ";", 2)[0]) == "endasm" {
			end = ln + 1 // 1-based number of the endasm line
			break
		}
	}
	if end < 0 {
		p.fail(t.Pos, "missing endasm")
	}
	a := &Asm{Pos: t.Pos, First: start + 1, Lines: p.lines[start : end-1]}
	for !(p.isKw("endasm") && p.tok().Pos.Line == end) {
		if p.tok().Kind == EOF {
			p.fail(t.Pos, "missing endasm")
		}
		p.next()
	}
	p.next()
	return a
}

func (p *Parser) simpleStmt() Stmt {
	start := p.tok()
	if (start.Text == "int" || start.Text == "sbyte") && p.peek().Kind == IDENT {
		p.fail(start.Pos, "type %s is not supported yet; version 0.1 has byte and card", start.Text)
	}
	lv := p.postfix()
	t := p.tok()
	if t.Kind == OP {
		switch t.Text {
		case "=", "+=", "-=", "&=", "|=", "^=", "<<=", ">>=":
			p.next()
			return &Assign{Pos: start.Pos, Target: lv, Op: t.Text, Value: p.value()}
		case "<-":
			id, ok := lv.(*Ident)
			if !ok {
				p.fail(t.Pos, "<- needs the name of a ring buffer")
			}
			p.next()
			return &Push{Pos: start.Pos, Ring: id.Name, Value: p.value()}
		}
	}
	if c, ok := lv.(*Call); ok {
		return &ExprStmt{Pos: start.Pos, X: c}
	}
	p.fail(t.Pos, "expected an assignment or a call")
	return nil
}

// value reads the right side of an assignment or push. A wait with a timeout
// must be the whole value: on timeout the assignment does not happen.
func (p *Parser) value() Expr {
	if p.isKw("wait") {
		i, nw := p.i, len(p.Warnings)
		if w := p.waitExpr(true); w.Timeout != nil {
			return w
		}
		p.i, p.Warnings = i, p.Warnings[:nw] // without a timeout, wait can be part of an expression
	}
	return p.expr()
}

// ---- expressions ----

func (p *Parser) expr() Expr { return p.orExpr() }

func (p *Parser) orExpr() Expr {
	l := p.andExpr()
	for p.isKw("or") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: "or", L: l, R: p.andExpr()}
	}
	return l
}

func (p *Parser) andExpr() Expr {
	l := p.cmpExpr()
	for p.isKw("and") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: "and", L: l, R: p.cmpExpr()}
	}
	return l
}

func (p *Parser) cmpExpr() Expr {
	l := p.bitOr()
	for {
		t := p.tok()
		if t.Kind != OP {
			return l
		}
		switch t.Text {
		case "=", "<>", "<", "<=", ">", ">=":
			p.next()
			l = &Binary{Pos: t.Pos, Op: t.Text, L: l, R: p.bitOr()}
		case "<-":
			// inside an expression <- reads as < and unary minus
			p.next()
			p.warn(t.Pos, "\"<-\" in an expression reads as \"< -\"; add a space")
			r := p.bitOr()
			l = &Binary{Pos: t.Pos, Op: "<", L: l, R: &Unary{Pos: t.Pos, Op: "-", X: r}}
		default:
			return l
		}
	}
}

func (p *Parser) bitOr() Expr {
	l := p.bitAnd()
	for p.isOp("|") || p.isOp("^") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: t.Text, L: l, R: p.bitAnd()}
	}
	return l
}

func (p *Parser) bitAnd() Expr {
	l := p.shift()
	for p.isOp("&") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: "&", L: l, R: p.shift()}
	}
	return l
}

func (p *Parser) shift() Expr {
	l := p.addExpr()
	for p.isOp("<<") || p.isOp(">>") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: t.Text, L: l, R: p.addExpr()}
	}
	return l
}

func (p *Parser) addExpr() Expr {
	l := p.mulExpr()
	for p.isOp("+") || p.isOp("-") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: t.Text, L: l, R: p.mulExpr()}
	}
	return l
}

func (p *Parser) mulExpr() Expr {
	l := p.unary()
	for p.isOp("*") || p.isOp("/") || p.isKw("mod") {
		t := p.next()
		l = &Binary{Pos: t.Pos, Op: t.Text, L: l, R: p.unary()}
	}
	return l
}

func (p *Parser) unary() Expr {
	t := p.tok()
	switch {
	case p.isOp("-") || p.isOp("~"):
		p.next()
		return &Unary{Pos: t.Pos, Op: t.Text, X: p.unary()}
	case p.isKw("not"):
		p.next()
		return &Unary{Pos: t.Pos, Op: "not", X: p.unary()}
	case p.isOp("@"):
		p.next()
		id := p.ident()
		return &Unary{Pos: t.Pos, Op: "@", X: &Ident{Pos: id.Pos, Name: id.Text}}
	case p.isKw("wait"):
		return p.waitExpr(false)
	}
	return p.postfix()
}

func (p *Parser) postfix() Expr {
	t := p.tok()
	switch t.Kind {
	case NUMBER:
		p.next()
		return &Num{Pos: t.Pos, V: t.Num}
	case STRING:
		p.next()
		return &Str{Pos: t.Pos, S: t.Text, ZTerm: t.ZTerm}
	case KEYWORD:
		if (t.Text == "byte" || t.Text == "card") && p.peek().Kind == OP && p.peek().Text == "(" {
			p.next()
			p.next()
			x := p.expr()
			p.expectOp(")")
			return &Call{Pos: t.Pos, Name: t.Text, Args: []Expr{x}}
		}
	case OP:
		if t.Text == "(" {
			p.next()
			x := p.expr()
			p.expectOp(")")
			return x
		}
	case IDENT:
		p.next()
		switch {
		case p.isOp("("):
			p.next()
			c := &Call{Pos: t.Pos, Name: t.Text}
			for !p.isOp(")") {
				c.Args = append(c.Args, p.expr())
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			p.expectOp(")")
			return c
		case p.isOp("["):
			p.next()
			x := p.expr()
			p.expectOp("]")
			return &Index{Pos: t.Pos, Base: t.Text, Idx: x}
		case p.isOp(".") && p.peek().Kind == NUMBER:
			p.next()
			lo := p.next()
			if lo.Num > 7 {
				p.fail(lo.Pos, "bit number must be 0-7")
			}
			b := &BitRef{Pos: t.Pos, Base: t.Text, Lo: lo.Num, Hi: lo.Num}
			if p.isOp("..") {
				p.next()
				hi := p.next()
				if hi.Kind != NUMBER || hi.Num > 7 || hi.Num < lo.Num {
					p.fail(hi.Pos, "invalid bit range")
				}
				b.Hi = hi.Num
			}
			return b
		}
		return &Ident{Pos: t.Pos, Name: t.Text}
	}
	p.fail(t.Pos, "expected an expression, found %q", p.desc())
	return nil
}

// waitExpr reads a wait; stmt reports whether it is the whole statement or
// the whole value of an assignment or push, which a timeout requires.
func (p *Parser) waitExpr(stmt bool) *Wait {
	t := p.expectKw("wait")
	w := &Wait{Pos: t.Pos}
	if p.tok().Kind == IDENT {
		switch p.tok().Text {
		case "change":
			p.next()
			w.Kind = "change"
			w.Target = p.postfix()
			if p.tok().Kind == IDENT && p.tok().Text == "from" {
				p.next()
				w.From = p.bitOr()
			}
		case "rising", "falling":
			w.Kind = p.next().Text
			w.Target = p.postfix()
			if _, ok := w.Target.(*BitRef); !ok {
				p.fail(t.Pos, "wait %s needs a bit, e.g. PAD.7", w.Kind)
			}
		}
	}
	if w.Kind == "" {
		w.Kind = "cond"
		w.Cond = p.expr()
	}
	if p.tok().Kind == IDENT && p.tok().Text == "timeout" {
		p.next()
		w.Timeout = p.mulExpr()
		u := p.ident()
		switch u.Text {
		case "us", "ms", "s":
			w.Unit = u.Text
		default:
			p.fail(u.Pos, "timeout unit must be us, ms or s")
		}
		if !stmt {
			p.fail(t.Pos, "wait with a timeout must be a statement of its own: wait ..., x = wait ... or buf <- wait ...")
		}
		if p.isKw("then") {
			p.next()
			w.OnTimeout = p.block(map[string]bool{"fi": true})
			p.expectKw("fi")
		}
	}
	return w
}
