package compiler

import (
	"github.com/jrydval/tantal/asm"
	"github.com/jrydval/tantal/cpu"
	"github.com/jrydval/tantal/lang"
)

// gen generates code for one procedure instance (or the main program).
type gen struct {
	c        *Compiler
	p        *asm.Program
	inst     *Inst
	tempTop  int
	loops    []string
	atomicAt []int // loop depth when entering atomic
}

// val is a simple operand: an immediate value or a memory location.
type val struct {
	imm bool
	op  asm.Operand
	t   Type
}

type part struct {
	imm bool
	op  asm.Operand
}

func (v val) lo() part {
	if v.imm {
		o := v.op
		if o.Sym != "" {
			o.Part = asm.Lo
		} else {
			o = asm.N(o.Off & 0xFF)
		}
		return part{true, o}
	}
	return part{false, v.op}
}

func (v val) hi() part {
	if !v.t.wide() {
		return part{true, asm.N(0)}
	}
	if v.imm {
		o := v.op
		if o.Sym != "" {
			o.Part = asm.Hi
		} else {
			o = asm.N((o.Off >> 8) & 0xFF)
		}
		return part{true, o}
	}
	o := v.op
	o.Off++
	return part{false, o}
}

func memVal(op asm.Operand, t Type) val { return val{op: op, t: t} }

// ---- emitting ----

func (g *gen) at(pos lang.Pos) {
	g.p.CurLine = pos.Line
	g.p.CurFile = pos.File
}

func (g *gen) e(mn string, m cpu.Mode, op asm.Operand) { g.p.I(mn, m, op) }
func (g *gen) imp(mn string)                           { g.p.Imp(mn) }
func (g *gen) imm(mn string, v int)                    { g.p.I(mn, cpu.Imm, asm.N(v&0xFF)) }
func (g *gen) mem(mn string, op asm.Operand) {
	// LDA right after STA to the same (non-fixed) address is redundant
	if mn == "LDA" && op.Sym != "" {
		if last := g.p.Last(); last != nil && last.Mnem == "STA" && last.Mode == asm.Auto && last.Arg == op {
			return
		}
	}
	g.p.I(mn, asm.Auto, op)
}
func (g *gen) memX(mn string, op asm.Operand) { g.p.I(mn, asm.AutoX, op) }
func (g *gen) memY(mn string, op asm.Operand) { g.p.I(mn, asm.AutoY, op) }
func (g *gen) indY(mn string, op asm.Operand) { g.p.I(mn, cpu.IndY, op) }
func (g *gen) br(mn, label string)            { g.p.I(mn, cpu.Rel, asm.S(label, 0)) }
func (g *gen) jmp(label string)               { g.p.I("JMP", cpu.Abs, asm.S(label, 0)) }
func (g *gen) label(l string)                 { g.p.Label(l) }
func (g *gen) newLabel(p string) string       { return g.c.newLabel(p) }
func (g *gen) acc(mn string)                  { g.p.I(mn, cpu.Acc, asm.Operand{}) }

func (g *gen) part(mn string, p part) {
	if p.imm {
		g.p.I(mn, cpu.Imm, p.op)
	} else {
		g.mem(mn, p.op)
	}
}

func (g *gen) fail(pos lang.Pos, f string, a ...any) { g.c.fail(pos, f, a...) }

// ---- temporaries in the instance frame ----

func (g *gen) localSize() int {
	if g.inst.Proc == nil {
		return 0
	}
	return g.inst.Proc.LocalSize
}

func (g *gen) temp(n int) asm.Operand {
	op := asm.S(g.inst.FrameSym, g.localSize()+g.tempTop)
	g.tempTop += n
	if g.tempTop > g.inst.Temps {
		g.inst.Temps = g.tempTop
	}
	return op
}

func (g *gen) free(n int) { g.tempTop -= n }

// ---- symbols ----

func (g *gen) lookup(name string) any {
	if name == "timedout" {
		return g.c.useTimedOut()
	}
	if g.inst.Proc != nil {
		if v, ok := g.inst.Proc.Locals[name]; ok {
			return v
		}
	}
	return g.c.globals[name]
}

func (g *gen) vop(v *Var) asm.Operand {
	switch {
	case v.Fixed:
		return asm.N(v.Addr)
	case v.Proc != nil && !v.Static:
		if g.inst.Proc != v.Proc {
			g.c.fail(v.Pos, "internal error: access to a local variable of another procedure")
		}
		return asm.S(g.inst.FrameSym, v.Off)
	}
	return asm.S(v.Label, 0)
}

func (c *Compiler) useTimedOut() *Var {
	if c.timedOut == nil {
		c.timedOut = &Var{Name: "timedout", Elem: TByte, Label: "v_timedout", Hidden: true}
		c.timedOut.Order = len(c.gvars)
		c.gvars = append(c.gvars, c.timedOut)
	}
	return c.timedOut
}

// track records a read-modify-write of a possibly shared variable.
func (g *gen) track(name string, pos lang.Pos) {
	atomic := len(g.atomicAt) > 0 || (g.inst.Proc != nil && g.inst.Proc.Interrupt != "")
	g.c.shared[name] = append(g.c.shared[name], access{ctx: g.inst.Ctx, atomic: atomic, pos: pos})
}

func (g *gen) helper(name string) {
	if g.c.helperCtx[name] == nil {
		g.c.helperCtx[name] = map[string]bool{}
	}
	g.c.helperCtx[name][g.inst.Ctx] = true
}

func (c *Compiler) useMP() asm.Operand {
	if c.mp == nil {
		c.mp = &Var{Name: "__mp", Pointer: true, Elem: TByte, Label: "__mp", Hidden: true, Static: true}
		c.mp.Order = len(c.gvars)
		c.gvars = append(c.gvars, c.mp)
	}
	return asm.S("__mp", 0)
}

func (c *Compiler) useRT() asm.Operand {
	if c.rt == nil {
		c.rt = &Var{Name: "__rt", Elem: TByte, Label: "__rt", Hidden: true, Len: 6, Array: true}
		c.rt.Order = len(c.gvars)
		c.gvars = append(c.gvars, c.rt)
	}
	return asm.S("__rt", 0)
}

// ---- constants ----

func (c *Compiler) mustConst(e lang.Expr) int {
	v, ok := c.constEval(e)
	if !ok {
		c.fail(e.P(), "constant expected")
	}
	return v
}

func (c *Compiler) constEval(e lang.Expr) (int, bool) {
	switch x := e.(type) {
	case *lang.Num:
		return x.V, true
	case *lang.Ident:
		if k, ok := c.globals[x.Name].(*constVal); ok {
			return k.V, true
		}
	case *lang.Unary:
		if x.Op == "@" {
			if id, ok := x.X.(*lang.Ident); ok {
				if v, ok := c.globals[id.Name].(*Var); ok && v.Fixed {
					return v.Addr, true
				}
			}
			return 0, false
		}
		v, ok := c.constEval(x.X)
		if !ok {
			return 0, false
		}
		switch x.Op {
		case "-":
			return -v, true
		case "~":
			return ^v, true
		case "not":
			return b2i(v == 0), true
		}
	case *lang.Binary:
		l, ok1 := c.constEval(x.L)
		r, ok2 := c.constEval(x.R)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch x.Op {
		case "+":
			return l + r, true
		case "-":
			return l - r, true
		case "*":
			return l * r, true
		case "/":
			if r == 0 {
				return 0xFFFF, true
			}
			return l / r, true
		case "mod":
			if r == 0 {
				return l, true
			}
			return l % r, true
		case "&":
			return l & r, true
		case "|":
			return l | r, true
		case "^":
			return l ^ r, true
		case "<<":
			return l << uint(r), true
		case ">>":
			return l >> uint(r), true
		case "=":
			return b2i(l == r), true
		case "<>":
			return b2i(l != r), true
		case "<":
			return b2i(l < r), true
		case "<=":
			return b2i(l <= r), true
		case ">":
			return b2i(l > r), true
		case ">=":
			return b2i(l >= r), true
		case "and":
			return b2i(l != 0 && r != 0), true
		case "or":
			return b2i(l != 0 || r != 0), true
		}
	case *lang.Call:
		if x.Name == "sizeof" && len(x.Args) == 1 {
			if id, ok := x.Args[0].(*lang.Ident); ok {
				if v, ok := c.globals[id.Name].(*Var); ok {
					return v.Size(), true
				}
			}
			return 0, false
		}
		if len(x.Args) != 1 {
			return 0, false
		}
		v, ok := c.constEval(x.Args[0])
		if !ok {
			return 0, false
		}
		switch x.Name {
		case "lo", "byte":
			return v & 0xFF, true
		case "hi":
			return (v >> 8) & 0xFF, true
		case "card":
			return v & 0xFFFF, true
		}
	}
	return 0, false
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// constOp: a constant or a symbolic address (global variable, string literal).
func (c *Compiler) constOp(e lang.Expr) (asm.Operand, bool) {
	if v, ok := c.constEval(e); ok {
		return asm.N(v), true
	}
	switch x := e.(type) {
	case *lang.Str:
		return asm.S(c.strLit(x), 0), true
	case *lang.Unary:
		if x.Op == "@" {
			if id, ok := x.X.(*lang.Ident); ok {
				if v, ok := c.globals[id.Name].(*Var); ok && v.Proc == nil {
					return asm.S(v.Label, 0), true
				}
			}
		}
	case *lang.Binary:
		if x.Op == "+" || x.Op == "-" {
			l, ok1 := c.constOp(x.L)
			r, ok2 := c.constEval(x.R)
			if ok1 && ok2 && l.Sym != "" {
				if x.Op == "-" {
					r = -r
				}
				l.Off += r
				return l, true
			}
		}
	}
	return asm.Operand{}, false
}

func (c *Compiler) strLit(s *lang.Str) string {
	key := s.S
	if s.ZTerm {
		key = "z:" + key
	} else {
		key = "l:" + key
	}
	if l, ok := c.strLits[key]; ok {
		return l
	}
	if len(s.S) > 255 {
		c.fail(s.Pos, "string is longer than 255 characters")
	}
	l := c.newLabel("str")
	c.strLits[key] = l
	data := []asm.Operand{}
	if !s.ZTerm {
		data = append(data, asm.N(len(s.S)))
	}
	for i := 0; i < len(s.S); i++ {
		data = append(data, asm.N(int(s.S[i])))
	}
	if s.ZTerm {
		data = append(data, asm.N(0))
	}
	c.data = append(c.data, func() {
		c.p.Label(l)
		c.p.Bytes(data...)
	})
	return l
}

// ---- types ----

func (g *gen) typeOf(e lang.Expr) Type {
	switch x := e.(type) {
	case *lang.Num:
		if x.V >= -128 && x.V <= 255 {
			return TByte
		}
		return TCard
	case *lang.Str:
		return TAddr
	case *lang.Ident:
		switch s := g.lookup(x.Name).(type) {
		case *Var:
			if !s.Scalar() {
				return TAddr
			}
			return s.Type()
		case *Bit:
			return TByte
		case *constVal:
			if s.V >= -128 && s.V <= 255 {
				return TByte
			}
			return TCard
		case nil:
			g.fail(x.Pos, "unknown name %q", x.Name)
		}
		g.fail(x.Pos, "%s cannot be used as a value", x.Name)
	case *lang.Index, *lang.BitRef, *lang.Wait:
		return TByte
	case *lang.Unary:
		switch x.Op {
		case "not":
			return TBool
		case "@":
			return TAddr
		}
		t := g.typeOf(x.X)
		if t == TBool {
			return TByte
		}
		if t == TAddr {
			return TCard
		}
		return t
	case *lang.Binary:
		switch x.Op {
		case "=", "<>", "<", "<=", ">", ">=", "and", "or":
			return TBool
		}
		l, r := g.typeOf(x.L), g.typeOf(x.R)
		if x.Op == "<<" || x.Op == ">>" {
			if l.wide() {
				return TCard
			}
			return TByte
		}
		if l.wide() || r.wide() {
			return TCard
		}
		return TByte
	case *lang.Call:
		switch x.Name {
		case "lo", "hi", "byte", "pop", "last":
			return TByte
		case "card":
			return TCard
		case "full":
			return TBool
		case "clear":
			return TVoid
		case "sizeof":
			v, _ := g.c.constEval(x)
			if v > 255 {
				return TCard
			}
			return TByte
		case "len":
			if len(x.Args) == 1 {
				if id, ok := x.Args[0].(*lang.Ident); ok {
					if v, ok := g.lookup(id.Name).(*Var); ok && v.Ring && v.Len == 256 {
						return TCard
					}
				}
			}
			return TByte
		}
		if p, ok := g.c.globals[x.Name].(*Proc); ok {
			if p.Extern && p.Decl.Ret != "" {
				return TByte
			}
			return p.Ret
		}
		g.fail(x.Pos, "unknown procedure %q", x.Name)
	}
	return TByte
}

// simple returns a side-effect-free operand when the expression needs no code.
func (g *gen) simple(e lang.Expr) (val, bool) {
	if v, ok := g.c.constEval(e); ok {
		t := TByte
		if v < -128 || v > 255 {
			t = TCard
		}
		return val{imm: true, op: asm.N(v), t: t}, true
	}
	switch x := e.(type) {
	case *lang.Str:
		return val{imm: true, op: asm.S(g.c.strLit(x), 0), t: TAddr}, true
	case *lang.Ident:
		v, ok := g.lookup(x.Name).(*Var)
		if !ok || !v.Scalar() {
			return val{}, false
		}
		return memVal(g.readOp(v, x.Pos), v.Type()), true
	case *lang.Unary:
		if x.Op == "@" {
			id := x.X.(*lang.Ident)
			v, ok := g.lookup(id.Name).(*Var)
			if !ok {
				if p, ok := g.c.globals[id.Name].(*Proc); ok && !p.Extern {
					in := g.instFor(p, id.Pos)
					return val{imm: true, op: asm.S(in.Label, 0), t: TAddr}, true
				}
				g.fail(x.Pos, "@ needs a variable")
			}
			return val{imm: true, op: g.vop(v), t: TAddr}, true
		}
	case *lang.Index:
		k, ok := g.c.constEval(x.Idx)
		if !ok {
			return val{}, false
		}
		if x.Base == "mem" {
			if k < 0 || k > 0xFFFF {
				g.fail(x.Pos, "adresa mimo rozsah")
			}
			return memVal(asm.N(k), TByte), true
		}
		v, ok := g.lookup(x.Base).(*Var)
		if !ok || !(v.Array || v.String) || v.Ring {
			return val{}, false
		}
		g.checkIndex(v, k, x.Pos)
		if v.WriteOnly {
			g.fail(x.Pos, "%s is write-only", v.Name)
		}
		op := g.vop(v)
		op.Off += k
		return memVal(op, TByte), true
	}
	return val{}, false
}

func (g *gen) checkIndex(v *Var, k int, pos lang.Pos) {
	if k < 0 || (v.Len > 0 && k >= v.Len) {
		g.fail(pos, "index %d is outside array %s (size %d)", k, v.Name, v.Len)
	}
}

// readOp: operand for reading a scalar variable (a shadow register is read from its copy).
func (g *gen) readOp(v *Var, pos lang.Pos) asm.Operand {
	if v.Shadow != nil {
		return asm.S(v.Shadow.Label, 0)
	}
	if v.WriteOnly {
		g.fail(pos, "%s is write-only", v.Name)
	}
	return g.vop(v)
}

// ---- expressions ----

// genByte evaluates an expression into A.
func (g *gen) genByte(e lang.Expr) {
	if v, ok := g.simple(e); ok {
		g.part("LDA", v.lo())
		return
	}
	t := g.typeOf(e)
	if t.wide() {
		g.genWord(e)
		return
	}
	if t == TBool {
		g.genBool(e)
		return
	}
	switch x := e.(type) {
	case *lang.Ident:
		switch s := g.lookup(x.Name).(type) {
		case *Bit:
			g.bitValue(s, x.Pos)
			return
		case *Var:
			g.fail(x.Pos, "array %s cannot be used as a value (its address is @%s)", s.Name, s.Name)
		}
		g.fail(x.Pos, "%s cannot be used as a value", x.Name)
	case *lang.Index:
		g.indexLoad(x)
	case *lang.BitRef:
		g.bitValue(g.bitOf(x), x.Pos)
	case *lang.Unary:
		g.genByte(x.X)
		switch x.Op {
		case "-":
			g.imm("EOR", 0xFF)
			g.imp("CLC")
			g.imm("ADC", 1)
		case "~":
			g.imm("EOR", 0xFF)
		}
	case *lang.Binary:
		switch x.Op {
		case "+", "-", "&", "|", "^":
			g.byteBin(x)
		case "<<", ">>":
			g.byteShift(x)
		case "*", "/", "mod":
			g.mulDiv(x, false)
		}
	case *lang.Call:
		g.call(x)
	case *lang.Wait:
		g.genWait(x)
	default:
		g.fail(e.P(), "unsupported expression")
	}
}

var aluMn = map[string]string{"+": "ADC", "-": "SBC", "&": "AND", "|": "ORA", "^": "EOR"}

func (g *gen) carryFor(op string) {
	switch op {
	case "+":
		g.imp("CLC")
	case "-":
		g.imp("SEC")
	}
}

func (g *gen) byteBin(x *lang.Binary) {
	mn := aluMn[x.Op]
	if r, ok := g.simple(x.R); ok {
		g.genByte(x.L)
		g.carryFor(x.Op)
		g.part(mn, r.lo())
		return
	}
	if l, ok := g.simple(x.L); ok && x.Op != "-" {
		g.genByte(x.R)
		g.carryFor(x.Op)
		g.part(mn, l.lo())
		return
	}
	g.genByte(x.L)
	t := g.temp(1)
	g.mem("STA", t)
	g.genByte(x.R)
	t2 := g.temp(1)
	g.mem("STA", t2)
	g.mem("LDA", t)
	g.carryFor(x.Op)
	g.mem(mn, t2)
	g.free(2)
}

func (g *gen) byteShift(x *lang.Binary) {
	mn := "ASL"
	if x.Op == ">>" {
		mn = "LSR"
	}
	if n, ok := g.c.constEval(x.R); ok {
		g.genByte(x.L)
		if n >= 8 {
			g.imm("LDA", 0)
			return
		}
		for i := 0; i < n; i++ {
			g.acc(mn)
		}
		return
	}
	if r, ok := g.simple(x.R); ok {
		g.genByte(x.L)
		g.part("LDX", r.lo())
	} else {
		g.genByte(x.R)
		t := g.temp(1)
		g.mem("STA", t)
		g.genByte(x.L)
		g.mem("LDX", t)
		g.free(1)
	}
	loop, done := g.newLabel("sh"), g.newLabel("shd")
	g.br("BEQ", done)
	g.label(loop)
	g.acc(mn)
	g.imp("DEX")
	g.br("BNE", loop)
	g.label(done)
}

// genWord evaluates an expression into A (low byte) and X (high byte).
func (g *gen) genWord(e lang.Expr) {
	t := g.typeOf(e)
	if v, ok := g.simple(e); ok {
		g.part("LDA", v.lo())
		g.part("LDX", v.hi())
		return
	}
	if !t.wide() {
		g.genByte(e)
		g.imm("LDX", 0)
		return
	}
	switch x := e.(type) {
	case *lang.Unary:
		g.genWord(x.X)
		t := g.temp(1)
		switch x.Op {
		case "-":
			g.imm("EOR", 0xFF)
			g.imp("CLC")
			g.imm("ADC", 1)
			g.mem("STA", t)
			g.imp("TXA")
			g.imm("EOR", 0xFF)
			g.imm("ADC", 0)
		case "~":
			g.imm("EOR", 0xFF)
			g.mem("STA", t)
			g.imp("TXA")
			g.imm("EOR", 0xFF)
		}
		g.imp("TAX")
		g.mem("LDA", t)
		g.free(1)
	case *lang.Binary:
		switch x.Op {
		case "+", "-", "&", "|", "^":
			g.wordBin(x)
		case "<<", ">>":
			g.wordShift(x)
		case "*", "/", "mod":
			g.mulDiv(x, true)
		default:
			g.fail(x.Pos, "operator %s does not produce a 16-bit value", x.Op)
		}
	case *lang.Call:
		g.call(x)
	default:
		g.fail(e.P(), "unsupported 16-bit expression")
	}
}

func (g *gen) wordBin(x *lang.Binary) {
	mn := aluMn[x.Op]
	r, ok := g.simple(x.R)
	used := 0
	if !ok {
		g.genWord(x.L)
		t1 := g.temp(2)
		g.mem("STA", t1)
		g.mem("STX", asm.S(t1.Sym, t1.Off+1))
		g.genWord(x.R)
		t2 := g.temp(2)
		g.mem("STA", t2)
		g.mem("STX", asm.S(t2.Sym, t2.Off+1))
		g.mem("LDA", t1)
		g.mem("LDX", asm.S(t1.Sym, t1.Off+1))
		r = memVal(t2, TCard)
		used = 4
	} else {
		g.genWord(x.L)
	}
	hi := r.hi()
	if hi.imm && hi.op.Sym == "" && hi.op.Off == 0 && (x.Op == "+" || x.Op == "-") {
		skip := g.newLabel("w")
		g.carryFor(x.Op)
		g.part(mn, r.lo())
		if x.Op == "+" {
			g.br("BCC", skip)
			g.imp("INX")
		} else {
			g.br("BCS", skip)
			g.imp("DEX")
		}
		g.label(skip)
		g.free(used)
		return
	}
	t := g.temp(1)
	g.carryFor(x.Op)
	g.part(mn, r.lo())
	g.mem("STA", t)
	g.imp("TXA")
	g.part(mn, hi)
	g.imp("TAX")
	g.mem("LDA", t)
	g.free(1 + used)
}

func (g *gen) wordShift(x *lang.Binary) {
	g.genWord(x.L)
	t := g.temp(2)
	th := asm.S(t.Sym, t.Off+1)
	g.mem("STA", t)
	g.mem("STX", th)
	step := func() {
		if x.Op == "<<" {
			g.mem("ASL", t)
			g.mem("ROL", th)
		} else {
			g.mem("LSR", th)
			g.mem("ROR", t)
		}
	}
	if n, ok := g.c.constEval(x.R); ok {
		if n >= 16 {
			g.imm("LDA", 0)
			g.imm("LDX", 0)
			g.free(2)
			return
		}
		for i := 0; i < n; i++ {
			step()
		}
	} else {
		g.genByte(x.R)
		g.imp("TAY")
		loop, done := g.newLabel("ws"), g.newLabel("wsd")
		g.br("BEQ", done)
		g.label(loop)
		step()
		g.imp("DEY")
		g.br("BNE", loop)
		g.label(done)
	}
	g.mem("LDA", t)
	g.mem("LDX", th)
	g.free(2)
}

func log2(n int) (int, bool) {
	if n <= 0 || n&(n-1) != 0 {
		return 0, false
	}
	k := 0
	for n > 1 {
		n >>= 1
		k++
	}
	return k, true
}

// mulDiv: * / mod. Powers of 2 become shifts and masks, otherwise a runtime routine.
func (g *gen) mulDiv(x *lang.Binary, wide bool) {
	if n, ok := g.c.constEval(x.R); ok {
		if k, ok := log2(n); ok {
			var e lang.Expr
			switch x.Op {
			case "*":
				e = &lang.Binary{Pos: x.Pos, Op: "<<", L: x.L, R: &lang.Num{Pos: x.Pos, V: k}}
			case "/":
				e = &lang.Binary{Pos: x.Pos, Op: ">>", L: x.L, R: &lang.Num{Pos: x.Pos, V: k}}
			case "mod":
				e = &lang.Binary{Pos: x.Pos, Op: "&", L: x.L, R: &lang.Num{Pos: x.Pos, V: n - 1}}
			}
			if wide {
				g.genWord(e)
			} else {
				g.genByte(e)
			}
			return
		}
	}
	rt := g.c.useRT()
	g.helper("__rt")
	rtOff := func(k int) asm.Operand { return asm.S(rt.Sym, k) }
	g.genWord(x.L)
	t := g.temp(2)
	g.mem("STA", t)
	g.mem("STX", asm.S(t.Sym, t.Off+1))
	g.genWord(x.R)
	g.mem("STA", rtOff(2))
	g.mem("STX", rtOff(3))
	g.mem("LDA", t)
	g.mem("STA", rtOff(0))
	g.mem("LDA", asm.S(t.Sym, t.Off+1))
	g.mem("STA", rtOff(1))
	g.free(2)
	if x.Op == "*" {
		g.c.needMul = true
		g.e("JSR", cpu.Abs, asm.S("__mul16", 0))
	} else {
		g.c.needDiv = true
		g.e("JSR", cpu.Abs, asm.S("__div16", 0))
		if x.Op == "mod" {
			g.mem("LDA", rtOff(4))
			g.mem("LDX", rtOff(5))
		}
	}
}

// genBool evaluates a boolean into A as 0 or 1.
func (g *gen) genBool(e lang.Expr) {
	if v, ok := g.c.constEval(e); ok {
		g.imm("LDA", b2i(v != 0))
		return
	}
	f, end := g.newLabel("f"), g.newLabel("b")
	g.cond(e, f, false)
	g.imm("LDA", 1)
	g.br("BNE", end)
	g.label(f)
	g.imm("LDA", 0)
	g.label(end)
}

// ensureFlags makes sure the Z and N flags reflect A.
func (g *gen) ensureFlags() {
	last := g.p.Last()
	if last != nil {
		switch last.Mnem {
		case "LDA", "TXA", "TYA", "PLA", "AND", "ORA", "EOR", "ADC", "SBC":
			return
		case "ASL", "LSR", "ROL", "ROR":
			if last.Mode == cpu.Acc {
				return
			}
		}
	}
	g.imm("CMP", 0)
}

var invertRel = map[string]string{"=": "<>", "<>": "=", "<": ">=", ">=": "<", ">": "<=", "<=": ">"}
var swapRel = map[string]string{"=": "=", "<>": "<>", "<": ">", ">": "<", "<=": ">=", ">=": "<="}

// cond jumps to label when the expression equals jit (true/false).
func (g *gen) cond(e lang.Expr, label string, jit bool) {
	if v, ok := g.c.constEval(e); ok {
		if (v != 0) == jit {
			g.jmp(label)
		}
		return
	}
	switch x := e.(type) {
	case *lang.Unary:
		if x.Op == "not" {
			g.cond(x.X, label, !jit)
			return
		}
	case *lang.Binary:
		switch x.Op {
		case "and":
			if jit {
				skip := g.newLabel("and")
				g.cond(x.L, skip, false)
				g.cond(x.R, label, true)
				g.label(skip)
			} else {
				g.cond(x.L, label, false)
				g.cond(x.R, label, false)
			}
			return
		case "or":
			if jit {
				g.cond(x.L, label, true)
				g.cond(x.R, label, true)
			} else {
				skip := g.newLabel("or")
				g.cond(x.L, skip, true)
				g.cond(x.R, label, false)
				g.label(skip)
			}
			return
		case "=", "<>", "<", "<=", ">", ">=":
			g.compare(x, label, jit)
			return
		}
	case *lang.Ident:
		if b, ok := g.lookup(x.Name).(*Bit); ok {
			g.bitTest(b, label, jit, x.Pos)
			return
		}
	case *lang.BitRef:
		g.bitTest(g.bitOf(x), label, jit, x.Pos)
		return
	case *lang.Call:
		if x.Name == "full" {
			g.ringFull(x, label, jit)
			return
		}
	}
	if g.typeOf(e).wide() {
		g.genWord(e)
		t := g.temp(1)
		g.mem("STX", t)
		g.mem("ORA", t)
		g.free(1)
	} else {
		g.genByte(e)
		g.ensureFlags()
	}
	if jit {
		g.br("BNE", label)
	} else {
		g.br("BEQ", label)
	}
}

// singleBit returns the bit if the expression is a single-bit alias or X.n.
func (g *gen) singleBit(e lang.Expr) (*Bit, bool) {
	switch x := e.(type) {
	case *lang.Ident:
		if b, ok := g.lookup(x.Name).(*Bit); ok && b.Lo == b.Hi {
			return b, true
		}
	case *lang.BitRef:
		if x.Lo == x.Hi {
			return g.bitOf(x), true
		}
	}
	return nil, false
}

func (g *gen) compare(x *lang.Binary, label string, jit bool) {
	op := x.Op
	if b, ok := g.singleBit(x.L); ok && (op == "=" || op == "<>") {
		if c, ok := g.c.constEval(x.R); ok && (c == 0 || c == 1) {
			set := (op == "=") == (c == 1)
			g.bitTest(b, label, jit == set, x.Pos)
			return
		}
	}
	lt, rt := g.typeOf(x.L), g.typeOf(x.R)
	if lt.wide() || rt.wide() {
		g.wordCompare(x, label, jit)
		return
	}
	if !jit {
		op = invertRel[op]
	}
	if c, ok := g.c.constEval(x.R); ok && c == 0 && (op == "=" || op == "<>") {
		g.genByte(x.L)
		g.ensureFlags()
		if op == "=" {
			g.br("BEQ", label)
		} else {
			g.br("BNE", label)
		}
		return
	}
	if r, ok := g.simple(x.R); ok {
		g.genByte(x.L)
		g.part("CMP", r.lo())
	} else if l, ok := g.simple(x.L); ok {
		g.genByte(x.R)
		g.part("CMP", l.lo())
		op = swapRel[op]
	} else {
		g.genByte(x.L)
		t := g.temp(1)
		g.mem("STA", t)
		g.genByte(x.R)
		t2 := g.temp(1)
		g.mem("STA", t2)
		g.mem("LDA", t)
		g.mem("CMP", t2)
		g.free(2)
	}
	g.relBranch(op, label)
}

func (g *gen) relBranch(op, label string) {
	switch op {
	case "=":
		g.br("BEQ", label)
	case "<>":
		g.br("BNE", label)
	case "<":
		g.br("BCC", label)
	case ">=":
		g.br("BCS", label)
	case ">":
		skip := g.newLabel("gt")
		g.br("BEQ", skip)
		g.br("BCS", label)
		g.label(skip)
	case "<=":
		g.br("BCC", label)
		g.br("BEQ", label)
	}
}

func (g *gen) wordCompare(x *lang.Binary, label string, jit bool) {
	op := x.Op
	if !jit {
		op = invertRel[op]
	}
	used := 0
	operand := func(e lang.Expr) val {
		if v, ok := g.simple(e); ok {
			return v
		}
		g.genWord(e)
		t := g.temp(2)
		used += 2
		g.mem("STA", t)
		g.mem("STX", asm.S(t.Sym, t.Off+1))
		return memVal(t, TCard)
	}
	l := operand(x.L)
	r := operand(x.R)
	if op == ">" || op == "<=" {
		l, r = r, l
		op = swapRel[op]
	}
	switch op {
	case "=":
		skip := g.newLabel("ne")
		g.part("LDA", l.lo())
		g.part("CMP", r.lo())
		g.br("BNE", skip)
		g.part("LDA", l.hi())
		g.part("CMP", r.hi())
		g.br("BEQ", label)
		g.label(skip)
	case "<>":
		g.part("LDA", l.lo())
		g.part("CMP", r.lo())
		g.br("BNE", label)
		g.part("LDA", l.hi())
		g.part("CMP", r.hi())
		g.br("BNE", label)
	case "<", ">=":
		g.part("LDA", l.lo())
		g.part("CMP", r.lo())
		g.part("LDA", l.hi())
		g.part("SBC", r.hi())
		if op == "<" {
			g.br("BCC", label)
		} else {
			g.br("BCS", label)
		}
	}
	g.free(used)
}

// ---- bits ----

func (g *gen) bitOf(x *lang.BitRef) *Bit {
	v, ok := g.lookup(x.Base).(*Var)
	if !ok || !v.Scalar() || v.Elem != TByte || v.Pointer {
		g.fail(x.Pos, "%s is not a byte variable or register", x.Base)
	}
	return &Bit{Name: x.Base, Reg: v, Lo: x.Lo, Hi: x.Hi}
}

func (g *gen) bitSrc(b *Bit, pos lang.Pos) asm.Operand {
	return g.readOp(b.Reg, pos)
}

func (g *gen) bitTest(b *Bit, label string, jitSet bool, pos lang.Pos) {
	src := g.bitSrc(b, pos)
	m := b.mask()
	switch {
	case m == 0x80:
		g.mem("BIT", src)
		if jitSet {
			g.br("BMI", label)
		} else {
			g.br("BPL", label)
		}
	case m == 0x40:
		g.mem("BIT", src)
		if jitSet {
			g.br("BVS", label)
		} else {
			g.br("BVC", label)
		}
	default:
		g.mem("LDA", src)
		g.imm("AND", m)
		if jitSet {
			g.br("BNE", label)
		} else {
			g.br("BEQ", label)
		}
	}
}

func (g *gen) bitValue(b *Bit, pos lang.Pos) {
	g.mem("LDA", g.bitSrc(b, pos))
	g.imm("AND", b.mask())
	for i := 0; i < b.Lo; i++ {
		g.acc("LSR")
	}
}

// storeTo stores A into a scalar byte variable (for shadow into both the copy and the register).
func (g *gen) storeTo(v *Var, pos lang.Pos) {
	if v.ReadOnly {
		g.fail(pos, "%s is read-only", v.Name)
	}
	if v.Shadow != nil {
		g.mem("STA", asm.S(v.Shadow.Label, 0))
	}
	g.mem("STA", g.vop(v))
}

func (g *gen) bitAssign(b *Bit, op string, value lang.Expr, pos lang.Pos) {
	r := b.Reg
	if r.ReadOnly {
		g.fail(pos, "%s is read-only", r.Name)
	}
	if r.WriteOnly && r.Shadow == nil {
		g.fail(pos, "%s is write-only; writing a bit needs shadow", r.Name)
	}
	g.track(r.Name, pos)
	src := g.bitSrc(b, pos)
	m := b.mask()
	width := b.Hi - b.Lo + 1
	if op == "^=" {
		c, ok := g.c.constEval(value)
		if !ok {
			g.fail(pos, "^= on a bit needs a constant")
		}
		g.mem("LDA", src)
		g.imm("EOR", (c<<b.Lo)&m)
		g.storeTo(r, pos)
		return
	}
	if op != "=" {
		value = &lang.Binary{Pos: pos, Op: op[:len(op)-1], L: &lang.BitRef{Pos: pos, Base: r.Name, Lo: b.Lo, Hi: b.Hi}, R: value}
		if g.lookup(r.Name) != r {
			g.fail(pos, "compound assignment to this bit is not supported")
		}
	}
	if c, ok := g.c.constEval(value); ok {
		if c < 0 || c >= 1<<width {
			g.warn(pos, "value %d does not fit in %d bits", c, width)
		}
		bits := (c << b.Lo) & m
		g.mem("LDA", src)
		if bits != m {
			g.imm("AND", ^m&0xFF)
		}
		if bits != 0 {
			g.imm("ORA", bits)
		}
		g.storeTo(r, pos)
		return
	}
	if g.typeOf(value) == TBool && width == 1 {
		g.genBool(value)
	} else {
		g.genByte(value)
	}
	for i := 0; i < b.Lo; i++ {
		g.acc("ASL")
	}
	g.imm("AND", m)
	t := g.temp(1)
	g.mem("STA", t)
	g.mem("LDA", src)
	g.imm("AND", ^m&0xFF)
	g.mem("ORA", t)
	g.free(1)
	g.storeTo(r, pos)
}

func (g *gen) warn(pos lang.Pos, f string, a ...any) { g.c.warn(pos, f, a...) }
