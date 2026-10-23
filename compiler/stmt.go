package compiler

import (
	"fmt"
	"strings"

	"github.com/jrydval/tantal-6502-compiled-language/asm"
	"github.com/jrydval/tantal-6502-compiled-language/cpu"
	"github.com/jrydval/tantal-6502-compiled-language/lang"
)

func (g *gen) stmts(ss []lang.Stmt) {
	for _, s := range ss {
		g.stmt(s)
	}
}

func (g *gen) stmt(s lang.Stmt) {
	g.at(s.P())
	switch x := s.(type) {
	case *lang.VarDecl:
		if g.inst.Proc == nil {
			g.fail(x.Pos, "declarations of the main program belong at the top level of the file")
		}
		if len(x.Init) > 0 && !x.Type.Array {
			g.assign(&lang.Assign{Pos: x.Pos, Target: &lang.Ident{Pos: x.Pos, Name: x.Name}, Op: "=", Value: x.Init[0]})
		}
	case *lang.Directive:
		if x.Name != "cycles" {
			g.fail(x.Pos, "directive .%s belongs at the top level of the file", x.Name)
		}
		if !g.c.cyclesWarned {
			g.warn(x.Pos, ".cycles is not checked yet in version %s; the listing shows cycles", Version)
			g.c.cyclesWarned = true
		}
	case *lang.Assign:
		g.assign(x)
	case *lang.Push:
		g.ringPush(x)
	case *lang.If:
		// if condition then exit fi -> a single conditional branch
		if len(x.Conds) == 1 && x.Else == nil && len(x.Blocks[0]) == 1 && len(g.loops) > 0 {
			if _, ok := x.Blocks[0][0].(*lang.Exit); ok && (len(g.atomicAt) == 0 || g.atomicAt[len(g.atomicAt)-1] < len(g.loops)) {
				g.cond(x.Conds[0], g.loops[len(g.loops)-1], true)
				return
			}
		}
		end := g.newLabel("fi")
		for i, c := range x.Conds {
			next := end
			last := i == len(x.Conds)-1 && x.Else == nil
			if !last {
				next = g.newLabel("el")
			}
			g.cond(c, next, false)
			g.stmts(x.Blocks[i])
			if !last {
				if !endsWithJump(x.Blocks[i]) {
					g.jmp(end)
				}
				g.label(next)
			}
		}
		if x.Else != nil {
			g.stmts(x.Else)
		}
		g.label(end)
	case *lang.Loop:
		top, exit := g.newLabel("do"), g.newLabel("od")
		g.label(top)
		g.loops = append(g.loops, exit)
		g.stmts(x.Body)
		g.loops = g.loops[:len(g.loops)-1]
		if x.Until != nil {
			g.at(x.Until.P())
			g.cond(x.Until, top, false)
		} else {
			g.jmp(top)
		}
		g.label(exit)
	case *lang.While:
		top, exit := g.newLabel("wh"), g.newLabel("od")
		g.label(top)
		g.cond(x.Cond, exit, false)
		g.loops = append(g.loops, exit)
		g.stmts(x.Body)
		g.loops = g.loops[:len(g.loops)-1]
		g.jmp(top)
		g.label(exit)
	case *lang.For:
		g.forStmt(x)
	case *lang.Exit:
		if len(g.loops) == 0 {
			g.fail(x.Pos, "exit outside a loop")
		}
		if n := len(g.atomicAt); n > 0 && g.atomicAt[n-1] >= len(g.loops) {
			g.fail(x.Pos, "exit must not leave an atomic block")
		}
		g.jmp(g.loops[len(g.loops)-1])
	case *lang.Return:
		g.ret(x)
	case *lang.ExprStmt:
		switch e := x.X.(type) {
		case *lang.Call:
			g.call(e)
		case *lang.Wait:
			g.genWait(e)
		default:
			g.fail(x.Pos, "expression has no effect")
		}
	case *lang.Delay:
		g.delay(x)
	case *lang.Asm:
		g.asmBlock(x)
	case *lang.Atomic:
		g.imp("PHP")
		g.imp("SEI")
		g.atomicAt = append(g.atomicAt, len(g.loops))
		g.stmts(x.Body)
		g.atomicAt = g.atomicAt[:len(g.atomicAt)-1]
		g.at(x.Pos)
		g.imp("PLP")
	default:
		g.fail(s.P(), "this statement does not belong here")
	}
}

func endsWithJump(ss []lang.Stmt) bool {
	if len(ss) == 0 {
		return false
	}
	switch ss[len(ss)-1].(type) {
	case *lang.Return, *lang.Exit:
		return true
	}
	return false
}

func (g *gen) ret(x *lang.Return) {
	if len(g.atomicAt) > 0 {
		g.fail(x.Pos, "return must not leave an atomic block")
	}
	if g.inst.Main {
		if x.X != nil {
			g.fail(x.Pos, "the main program does not return a value")
		}
		g.jmp("__exit")
		return
	}
	p := g.inst.Proc
	if x.X != nil {
		switch p.Ret {
		case TVoid:
			g.fail(x.Pos, "procedure %s does not return a value", p.Name)
		case TByte:
			if g.typeOf(x.X).wide() {
				g.narrowWarn(x.X)
			}
			g.genByte(x.X)
		case TCard:
			g.genWord(x.X)
		}
	} else if p.Ret != TVoid {
		g.fail(x.Pos, "procedure %s must return a value", p.Name)
	}
	if p.Interrupt != "" {
		g.jmp(g.inst.Label + "_end")
		return
	}
	g.imp("RTS")
}

func (g *gen) narrowWarn(e lang.Expr) {
	if v, ok := g.c.constEval(e); ok && v >= -128 && v <= 255 {
		return
	}
	g.warn(e.P(), "a 16-bit value is stored into a byte, the high byte is lost")
}

// ---- assignment ----

func (g *gen) assign(x *lang.Assign) {
	switch t := x.Target.(type) {
	case *lang.Ident:
		switch s := g.lookup(t.Name).(type) {
		case *Var:
			if !s.Scalar() {
				g.fail(t.Pos, "cannot assign to the whole array %s; use an index", s.Name)
			}
			g.varAssign(s, x)
			return
		case *Bit:
			g.bitAssign(s, x.Op, x.Value, x.Pos)
			return
		case nil:
			g.fail(t.Pos, "unknown name %q", t.Name)
		}
		g.fail(t.Pos, "cannot assign to %s", t.Name)
	case *lang.BitRef:
		g.bitAssign(g.bitOf(t), x.Op, x.Value, x.Pos)
	case *lang.Index:
		value := x.Value
		if x.Op != "=" {
			value = &lang.Binary{Pos: x.Pos, Op: x.Op[:len(x.Op)-1], L: t, R: x.Value}
		}
		g.indexStore(t, value)
	default:
		g.fail(x.Pos, "cannot assign to this expression")
	}
}

func (g *gen) varAssign(v *Var, x *lang.Assign) {
	if v.ReadOnly {
		g.fail(x.Pos, "%s is read-only", v.Name)
	}
	if x.Op != "=" && v.WriteOnly && v.Shadow == nil {
		g.fail(x.Pos, "%s is write-only; compound assignment needs shadow", v.Name)
	}
	wide := v.Type().wide()
	if x.Op != "=" || wide {
		g.track(v.Name, x.Pos)
	}
	if c, ok := g.c.constEval(x.Value); ok && c == 1 && (x.Op == "+=" || x.Op == "-=") && v.Shadow == nil {
		op := g.vop(v)
		mn := "INC"
		if x.Op == "-=" {
			mn = "DEC"
		}
		if !wide {
			g.mem(mn, op)
			return
		}
		skip := g.newLabel("inc")
		hi := asm.S(op.Sym, op.Off+1)
		if op.Sym == "" {
			hi = asm.N(op.Off + 1)
		}
		if mn == "INC" {
			g.mem("INC", op)
			g.br("BNE", skip)
			g.mem("INC", hi)
		} else {
			g.mem("LDA", op)
			g.br("BNE", skip)
			g.mem("DEC", hi)
			g.label(skip)
			g.mem("DEC", op)
			return
		}
		g.label(skip)
		return
	}
	value := x.Value
	if x.Op != "=" {
		value = &lang.Binary{Pos: x.Pos, Op: x.Op[:len(x.Op)-1], L: &lang.Ident{Pos: x.Pos, Name: v.Name}, R: x.Value}
	}
	if wide {
		g.genWord(value)
		op := g.vop(v)
		g.mem("STA", op)
		g.mem("STX", asm.S(op.Sym, op.Off+1))
		if op.Sym == "" {
			g.p.Items[len(g.p.Items)-1].Arg = asm.N(op.Off + 1)
		}
		return
	}
	vt := g.typeOf(value)
	if vt.wide() {
		g.narrowWarn(value)
	}
	if vt == TBool {
		g.genBool(value)
	} else {
		g.genByte(value)
	}
	g.storeTo(v, x.Pos)
}

// ---- indexing ----

func (g *gen) indexBase(x *lang.Index) *Var {
	if x.Base == "mem" {
		return nil
	}
	v, ok := g.lookup(x.Base).(*Var)
	if !ok || !(v.Array || v.String || v.Pointer || v.Ring) {
		g.fail(x.Pos, "%s cannot be indexed", x.Base)
	}
	return v
}

// ptrOperand returns a zero page pointer operand (a frame pointer is copied to __mp; clobbers A).
func (g *gen) ptrOperand(v *Var) asm.Operand {
	if v.Static {
		return g.vop(v)
	}
	g.helper("__mp")
	mp := g.c.useMP()
	op := g.vop(v)
	g.mem("LDA", op)
	g.mem("STA", mp)
	g.mem("LDA", asm.S(op.Sym, op.Off+1))
	g.mem("STA", asm.S(mp.Sym, 1))
	return mp
}

func (g *gen) indexLoad(x *lang.Index) {
	if v, ok := g.simple(x); ok {
		g.part("LDA", v.lo())
		return
	}
	v := g.indexBase(x)
	idxWide := g.typeOf(x.Idx).wide()
	switch {
	case v == nil: // mem[address]
		g.helper("__mp")
		mp := g.c.useMP()
		g.genWord(x.Idx)
		g.mem("STA", mp)
		g.mem("STX", asm.S(mp.Sym, 1))
		g.imm("LDY", 0)
		g.indY("LDA", mp)
	case v.Ring:
		g.ringIndex(v, x.Idx)
	case v.Pointer:
		if idxWide {
			mp := g.c.useMP()
			g.genWord(x.Idx)
			pop := g.vop(v)
			g.imp("CLC")
			g.mem("ADC", pop)
			g.mem("STA", mp)
			g.imp("TXA")
			g.mem("ADC", asm.S(pop.Sym, pop.Off+1))
			g.mem("STA", asm.S(mp.Sym, 1))
			g.imm("LDY", 0)
			g.indY("LDA", mp)
			return
		}
		if r, ok := g.simple(x.Idx); ok {
			g.part("LDY", r.lo())
		} else {
			g.genByte(x.Idx)
			g.imp("TAY")
		}
		pp := g.ptrOperand(v)
		g.indY("LDA", pp)
	default:
		if v.WriteOnly {
			g.fail(x.Pos, "%s is write-only", v.Name)
		}
		base := g.vop(v)
		if idxWide || v.Len > 256 {
			g.addrInMP(base, x.Idx)
			g.indY("LDA", asm.S("__mp", 0))
			return
		}
		if r, ok := g.simple(x.Idx); ok {
			g.part("LDX", r.lo())
		} else {
			g.genByte(x.Idx)
			g.imp("TAX")
		}
		g.memX("LDA", base)
	}
}

// addrInMP: __mp = base + index (16 bits), Y = 0.
func (g *gen) addrInMP(base asm.Operand, idx lang.Expr) {
	g.helper("__mp")
	mp := g.c.useMP()
	g.genWord(idx)
	lo, hi := base, base
	lo.Part, hi.Part = asm.Lo, asm.Hi
	if base.Sym == "" {
		lo, hi = asm.N(base.Off&0xFF), asm.N((base.Off>>8)&0xFF)
	}
	g.imp("CLC")
	g.e("ADC", cpu.Imm, lo)
	g.mem("STA", mp)
	g.imp("TXA")
	g.e("ADC", cpu.Imm, hi)
	g.mem("STA", asm.S(mp.Sym, 1))
	g.imm("LDY", 0)
}

func (g *gen) indexStore(x *lang.Index, value lang.Expr) {
	v := g.indexBase(x)
	if v != nil && (v.ReadOnly || v.Const) {
		g.fail(x.Pos, "%s is read-only", v.Name)
	}
	if v != nil && v.Ring {
		g.fail(x.Pos, "write to a ring with %s <- value", v.Name)
	}
	if g.typeOf(value).wide() {
		g.narrowWarn(value)
	}
	loadValue := func() {
		if g.typeOf(value) == TBool {
			g.genBool(value)
		} else {
			g.genByte(value)
		}
	}
	k, isConst := g.c.constEval(x.Idx)
	idxWide := g.typeOf(x.Idx).wide()
	switch {
	case v == nil:
		if isConst {
			loadValue()
			g.mem("STA", asm.N(k))
			return
		}
		g.genWord(x.Idx)
		t := g.temp(2)
		g.storeTempWord(t)
		loadValue()
		g.copyToMPKeepA(t)
		g.free(2)
		g.indY("STA", asm.S("__mp", 0))
	case v.Pointer:
		if idxWide {
			pop := g.vop(v)
			g.genWord(x.Idx)
			g.imp("CLC")
			g.mem("ADC", pop)
			t := g.temp(2)
			g.mem("STA", t)
			g.imp("TXA")
			g.mem("ADC", asm.S(pop.Sym, pop.Off+1))
			g.mem("STA", asm.S(t.Sym, t.Off+1))
			loadValue()
			g.copyToMPKeepA(t)
			g.free(2)
			g.indY("STA", asm.S("__mp", 0))
			return
		}
		var idx val
		used := 0
		if r, ok := g.simple(x.Idx); ok {
			idx = r
		} else {
			g.genByte(x.Idx)
			t := g.temp(1)
			g.mem("STA", t)
			idx = memVal(t, TByte)
			used = 1
		}
		loadValue()
		if !v.Static {
			g.imp("TAX")
			pp := g.ptrOperand(v)
			g.part("LDY", idx.lo())
			g.imp("TXA")
			g.indY("STA", pp)
		} else {
			g.part("LDY", idx.lo())
			g.indY("STA", g.vop(v))
		}
		g.free(used)
	default:
		base := g.vop(v)
		if isConst {
			g.checkIndex(v, k, x.Pos)
			loadValue()
			op := base
			op.Off += k
			g.mem("STA", op)
			return
		}
		if idxWide || v.Len > 256 {
			g.genWord(x.Idx)
			lo, hi := base, base
			lo.Part, hi.Part = asm.Lo, asm.Hi
			if base.Sym == "" {
				lo, hi = asm.N(base.Off&0xFF), asm.N((base.Off>>8)&0xFF)
			}
			t := g.temp(2)
			g.imp("CLC")
			g.e("ADC", cpu.Imm, lo)
			g.mem("STA", t)
			g.imp("TXA")
			g.e("ADC", cpu.Imm, hi)
			g.mem("STA", asm.S(t.Sym, t.Off+1))
			loadValue()
			g.copyToMPKeepA(t)
			g.free(2)
			g.indY("STA", asm.S("__mp", 0))
			return
		}
		if r, ok := g.simple(x.Idx); ok {
			loadValue()
			g.part("LDX", r.lo())
		} else {
			g.genByte(x.Idx)
			t := g.temp(1)
			g.mem("STA", t)
			loadValue()
			g.mem("LDX", t)
			g.free(1)
		}
		g.memX("STA", base)
	}
}

func (g *gen) storeTempWord(t asm.Operand) {
	g.mem("STA", t)
	g.mem("STX", asm.S(t.Sym, t.Off+1))
}

// copyToMPKeepA copies the 16-bit address at t to __mp, keeps A and sets Y = 0.
func (g *gen) copyToMPKeepA(t asm.Operand) {
	g.helper("__mp")
	mp := g.c.useMP()
	g.imp("TAX")
	g.mem("LDA", t)
	g.mem("STA", mp)
	g.mem("LDA", asm.S(t.Sym, t.Off+1))
	g.mem("STA", asm.S(mp.Sym, 1))
	g.imp("TXA")
	g.imm("LDY", 0)
}

// ---- for ----

func (g *gen) forStmt(x *lang.For) {
	v, ok := g.lookup(x.Var).(*Var)
	if !ok || !v.Scalar() || v.Pointer || v.Fixed {
		g.fail(x.Pos, "the for loop counter must be a byte or card variable")
	}
	wide := v.Elem == TCard
	vop := g.vop(v)
	vhi := asm.S(vop.Sym, vop.Off+1)
	exit, top := g.newLabel("for_end"), g.newLabel("for")

	// start value
	if wide {
		g.genWord(x.From)
		g.mem("STA", vop)
		g.mem("STX", vhi)
	} else {
		g.genByte(x.From)
		g.mem("STA", vop)
	}

	step := 1
	if x.Step != nil {
		s, ok := g.c.constEval(x.Step)
		if !ok || s < 1 || s > 255 {
			g.fail(x.Step.P(), "step must be a constant from 1 to 255")
		}
		step = s
	}

	// bound: a constant, or evaluated once at the start
	boundWide := g.typeOf(x.To).wide() || wide
	var last val
	used := 0
	bc, bconst := g.c.constEval(x.To)
	cmpBound := val{}
	if bconst {
		t := TByte
		if bc < 0 || bc > 255 {
			t = TCard
			boundWide = true
		}
		cmpBound = val{imm: true, op: asm.N(bc), t: t}
	} else {
		if boundWide {
			g.genWord(x.To)
			t := g.temp(2)
			used += 2
			g.storeTempWord(t)
			cmpBound = memVal(t, TCard)
		} else {
			g.genByte(x.To)
			t := g.temp(1)
			used++
			g.mem("STA", t)
			cmpBound = memVal(t, TByte)
		}
	}
	// empty range: to: start > bound, until: start >= bound
	cur := memVal(vop, v.Elem)
	if boundWide {
		// start < bound (until) or start <= bound (to), otherwise done
		if x.Exclusive {
			g.part("LDA", cur.lo())
			g.part("CMP", cmpBound.lo())
			g.part("LDA", cur.hi())
			g.part("SBC", cmpBound.hi())
			g.br("BCS", exit)
		} else {
			g.part("LDA", cmpBound.lo())
			g.part("CMP", cur.lo())
			g.part("LDA", cmpBound.hi())
			g.part("SBC", cur.hi())
			g.br("BCC", exit)
		}
	} else {
		g.part("LDA", cur.lo())
		g.part("CMP", cmpBound.lo())
		if x.Exclusive {
			g.br("BCS", exit)
		} else {
			skip := g.newLabel("ok")
			g.br("BEQ", skip)
			g.br("BCS", exit)
			g.label(skip)
		}
	}
	// last value of the counter
	if bconst {
		lv := bc
		if x.Exclusive {
			lv--
		}
		if !wide && (lv > 255) {
			if x.Exclusive && bc == 256 {
				lv = 255
			} else {
				g.warn(x.To.P(), "the bound may exceed the range of a byte counter")
				lv &= 0xFF
			}
		}
		t := TByte
		if wide {
			t = TCard
		}
		last = val{imm: true, op: asm.N(lv & 0xFFFF), t: t}
	} else {
		if !wide && boundWide && !x.Exclusive {
			g.warn(x.To.P(), "the bound may exceed the range of a byte counter")
		}
		if x.Exclusive {
			b := cmpBound.op
			if wide || boundWide {
				// last = bound - 1 (16 bits); a byte counter uses the low byte
				skip := g.newLabel("dec")
				g.mem("LDA", b)
				g.br("BNE", skip)
				g.mem("DEC", asm.S(b.Sym, b.Off+1))
				g.label(skip)
				g.mem("DEC", b)
			} else {
				g.mem("DEC", b)
			}
		}
		t := TByte
		if wide {
			t = TCard
		}
		last = memVal(cmpBound.op, t)
	}

	g.label(top)
	g.loops = append(g.loops, exit)
	g.stmts(x.Body)
	g.loops = g.loops[:len(g.loops)-1]
	g.at(x.Pos)

	if step == 1 {
		if !wide {
			if last.imm && last.op.Off&0xFF == 255 {
				g.mem("INC", vop)
				g.br("BNE", top)
			} else {
				g.mem("LDA", vop)
				g.part("CMP", last.lo())
				g.br("BEQ", exit)
				g.mem("INC", vop)
				g.br("BNE", top)
			}
		} else {
			inc := g.newLabel("inc")
			g.mem("LDA", vop)
			g.part("CMP", last.lo())
			g.br("BNE", inc)
			g.mem("LDA", vhi)
			g.part("CMP", last.hi())
			g.br("BEQ", exit)
			g.label(inc)
			g.mem("INC", vop)
			g.br("BNE", top)
			g.mem("INC", vhi)
			g.jmp(top)
		}
	} else {
		// done when last - i < step
		if !wide {
			g.part("LDA", last.lo())
			g.imp("SEC")
			g.mem("SBC", vop)
			g.imm("CMP", step)
			g.br("BCC", exit)
			g.mem("LDA", vop)
			g.imp("CLC")
			g.imm("ADC", step)
			g.mem("STA", vop)
			g.jmp(top)
		} else {
			ok := g.newLabel("ok")
			t := g.temp(1)
			g.part("LDA", last.lo())
			g.imp("SEC")
			g.mem("SBC", vop)
			g.mem("STA", t)
			g.part("LDA", last.hi())
			g.mem("SBC", vhi)
			g.br("BNE", ok)
			g.mem("LDA", t)
			g.imm("CMP", step)
			g.br("BCC", exit)
			g.label(ok)
			g.free(1)
			g.imp("CLC")
			g.mem("LDA", vop)
			g.imm("ADC", step)
			g.mem("STA", vop)
			skip := g.newLabel("c")
			g.br("BCC", skip)
			g.mem("INC", vhi)
			g.label(skip)
			g.jmp(top)
		}
	}
	g.label(exit)
	g.free(used)
}

// ---- calls ----

func (c *Compiler) instFor(p *Proc, ctx string, pos lang.Pos) *Inst {
	key := "main"
	if p.IrqSafe {
		key = ctx
	}
	if in, ok := p.Insts[key]; ok {
		if !p.IrqSafe && in.Ctx != ctx {
			c.fail(pos, "procedure %s is called from the main program and from an interrupt (%s and %s); mark it irqsafe", p.Name, in.Ctx, ctx)
		}
		return in
	}
	label := "p_" + p.Name
	if p.IrqSafe && ctx != "main" {
		label += "__" + ctx
	}
	in := &Inst{Proc: p, Ctx: ctx, Label: label, FrameSym: "F_" + label, Calls: map[*Inst]bool{}}
	p.Insts[key] = in
	c.insts = append(c.insts, in)
	c.work = append(c.work, in)
	return in
}

func (g *gen) instFor(p *Proc, pos lang.Pos) *Inst {
	if p.Interrupt != "" {
		g.fail(pos, "interrupt handler %s cannot be called", p.Name)
	}
	in := g.c.instFor(p, g.inst.Ctx, pos)
	g.inst.Calls[in] = true
	return in
}

// call generates a call; the result is in A (byte) or A/X (card).
func (g *gen) call(x *lang.Call) {
	if g.builtin(x) {
		return
	}
	p, ok := g.c.globals[x.Name].(*Proc)
	if !ok {
		g.fail(x.Pos, "unknown procedure %q", x.Name)
	}
	if len(x.Args) != len(p.Params) {
		g.fail(x.Pos, "%s expects %d arguments, got %d", p.Name, len(p.Params), len(x.Args))
	}
	// argument values left to right; complex ones into temporaries.
	// A single complex byte argument for register A is evaluated last, directly into A.
	vals := make([]val, len(x.Args))
	used := 0
	lateA := -1
	complexN := 0
	for _, a := range x.Args {
		if _, ok := g.simple(a); !ok {
			complexN++
		}
	}
	nbytes := 0
	for _, prm := range p.Params {
		nbytes += prm.Size()
	}
	if nbytes > 3 && !p.Extern {
		complexN = 0 // slots in the callee frame: complex arguments must be evaluated beforehand
	}
	for i, a := range x.Args {
		if complexN == 1 && !p.Params[i].Type().wide() {
			if _, ok := g.simple(a); !ok {
				isA := (p.Extern && p.Params[i].Label == "A") || (!p.Extern && i == 0)
				if isA {
					lateA = i
					continue
				}
			}
		}
		pw := p.Params[i].Type().wide()
		if !pw && g.typeOf(a).wide() {
			g.narrowWarn(a)
		}
		if v, ok := g.simple(a); ok {
			vals[i] = v
			continue
		}
		if pw {
			g.genWord(a)
			t := g.temp(2)
			used += 2
			g.storeTempWord(t)
			vals[i] = memVal(t, TCard)
		} else {
			if g.typeOf(a) == TBool {
				g.genBool(a)
			} else {
				g.genByte(a)
			}
			t := g.temp(1)
			used++
			g.mem("STA", t)
			vals[i] = memVal(t, TByte)
		}
	}
	loadLateA := func() {
		if lateA < 0 {
			return
		}
		a := x.Args[lateA]
		if g.typeOf(a) == TBool {
			g.genBool(a)
		} else {
			g.genByte(a)
		}
	}
	if p.Extern {
		loadLateA()
		for i, prm := range p.Params {
			if i == lateA {
				continue
			}
			g.part("LD"+prm.Label, vals[i].lo())
		}
		g.e("JSR", cpu.Abs, asm.N(p.Addr))
		g.free(used)
		return
	}
	in := g.instFor(p, x.Pos)
	type src struct {
		p   part
		off int
	}
	var bytes []src
	for i, prm := range p.Params {
		bytes = append(bytes, src{vals[i].lo(), prm.Off})
		if prm.Type().wide() {
			bytes = append(bytes, src{vals[i].hi(), prm.Off + 1})
		}
	}
	for i := 3; i < len(bytes); i++ {
		g.part("LDA", bytes[i].p)
		g.mem("STA", asm.S(in.FrameSym, bytes[i].off))
	}
	loadLateA()
	regs := []string{"LDA", "LDX", "LDY"}
	for i := 0; i < len(bytes) && i < 3; i++ {
		if i == 0 && lateA == 0 {
			continue
		}
		g.part(regs[i], bytes[i].p)
	}
	g.e("JSR", cpu.Abs, asm.S(in.Label, 0))
	g.free(used)
}

func (g *gen) argc(x *lang.Call, n int) {
	if len(x.Args) != n {
		g.fail(x.Pos, "%s expects %d arguments", x.Name, n)
	}
}

func (g *gen) ringArg(x *lang.Call) *Var {
	id, ok := x.Args[0].(*lang.Ident)
	if ok {
		if v, ok := g.lookup(id.Name).(*Var); ok && v.Ring {
			return v
		}
	}
	g.fail(x.Pos, "%s needs a ring buffer (ring)", x.Name)
	return nil
}

func (g *gen) builtin(x *lang.Call) bool {
	switch x.Name {
	case "lo":
		g.argc(x, 1)
		g.genWord(x.Args[0])
	case "byte":
		g.argc(x, 1)
		if g.typeOf(x.Args[0]).wide() {
			g.genWord(x.Args[0])
		} else {
			g.genByte(x.Args[0])
		}
	case "hi":
		g.argc(x, 1)
		g.genWord(x.Args[0])
		g.imp("TXA")
	case "card":
		g.argc(x, 1)
		g.genWord(x.Args[0])
	case "sizeof":
		v, ok := g.c.constEval(x)
		if !ok {
			g.fail(x.Pos, "sizeof needs a global variable")
		}
		g.imm("LDA", v)
		g.imm("LDX", v>>8)
	case "len":
		g.argc(x, 1)
		id, ok := x.Args[0].(*lang.Ident)
		var v *Var
		if ok {
			v, _ = g.lookup(id.Name).(*Var)
		}
		switch {
		case v == nil:
			g.fail(x.Pos, "len needs a string, pointer or ring")
		case v.Ring:
			op := asm.S(v.Count.Label, 0)
			g.mem("LDA", op)
			if v.Len == 256 {
				g.mem("LDX", asm.S(op.Sym, 1))
			}
		case v.Pointer:
			g.imm("LDY", 0)
			g.indY("LDA", g.ptrOperand(v))
		case v.Array || v.String:
			g.mem("LDA", g.vop(v))
		default:
			g.fail(x.Pos, "len needs a string, pointer or ring")
		}
	case "pop":
		g.argc(x, 1)
		r := g.ringArg(x)
		g.track(r.Name, x.Pos)
		g.ringPop(r)
	case "last":
		g.argc(x, 2)
		g.ringLast(g.ringArg(x), x.Args[1])
	case "full":
		g.argc(x, 1)
		g.genBool(x)
	case "clear":
		g.argc(x, 1)
		r := g.ringArg(x)
		g.track(r.Name, x.Pos)
		g.imm("LDA", 0)
		g.mem("STA", asm.S(r.Head.Label, 0))
		g.mem("STA", asm.S(r.Count.Label, 0))
		if r.Len == 256 {
			g.mem("STA", asm.S(r.Count.Label, 1))
		}
	default:
		return false
	}
	return true
}

// ---- ring buffer ----

func (g *gen) ringPush(x *lang.Push) {
	v, ok := g.lookup(x.Ring).(*Var)
	if !ok || !v.Ring {
		g.fail(x.Pos, "%s is not a ring buffer (ring)", x.Ring)
	}
	if g.typeOf(x.Value).wide() {
		g.narrowWarn(x.Value)
	}
	if g.typeOf(x.Value) == TBool {
		g.genBool(x.Value)
	} else {
		g.genByte(x.Value)
	}
	g.track(v.Name, x.Pos)
	head := asm.S(v.Head.Label, 0)
	cnt := asm.S(v.Count.Label, 0)
	data := asm.S(v.Label, 0)
	g.mem("LDY", head)
	g.memY("STA", data)
	g.imp("INY")
	if v.Len < 256 {
		g.imp("TYA")
		g.imm("AND", v.Len-1)
		g.mem("STA", head)
	} else {
		g.mem("STY", head)
	}
	skip := g.newLabel("full")
	if v.Len < 256 {
		g.mem("LDA", cnt)
		g.imm("CMP", v.Len)
		g.br("BEQ", skip)
		g.mem("INC", cnt)
	} else {
		g.mem("LDA", asm.S(cnt.Sym, 1))
		g.br("BNE", skip)
		g.mem("INC", cnt)
		g.br("BNE", skip)
		g.mem("INC", asm.S(cnt.Sym, 1))
	}
	g.label(skip)
}

func (g *gen) ringPop(v *Var) {
	head := asm.S(v.Head.Label, 0)
	cnt := asm.S(v.Count.Label, 0)
	data := asm.S(v.Label, 0)
	empty, done := g.newLabel("empty"), g.newLabel("pop")
	g.mem("LDA", cnt)
	if v.Len == 256 {
		g.mem("ORA", asm.S(cnt.Sym, 1))
	}
	g.br("BEQ", empty)
	g.mem("LDA", head)
	g.imp("SEC")
	g.mem("SBC", cnt)
	if v.Len < 256 {
		g.imm("AND", v.Len-1)
	}
	g.imp("TAY")
	if v.Len == 256 {
		nz := g.newLabel("dc")
		g.mem("LDA", cnt)
		g.br("BNE", nz)
		g.mem("DEC", asm.S(cnt.Sym, 1))
		g.label(nz)
	}
	g.mem("DEC", cnt)
	g.memY("LDA", data)
	g.jmp(done)
	g.label(empty)
	g.imm("LDA", 0)
	g.label(done)
}

func (g *gen) ringIndex(v *Var, idx lang.Expr) {
	head := asm.S(v.Head.Label, 0)
	cnt := asm.S(v.Count.Label, 0)
	data := asm.S(v.Label, 0)
	if k, ok := g.c.constEval(idx); ok {
		g.mem("LDA", head)
		g.imp("SEC")
		g.mem("SBC", cnt)
		if k != 0 {
			g.imp("CLC")
			g.imm("ADC", k)
		}
	} else {
		var r val
		used := 0
		if s, ok := g.simple(idx); ok {
			r = s
		} else {
			g.genByte(idx)
			t := g.temp(1)
			g.mem("STA", t)
			r = memVal(t, TByte)
			used = 1
		}
		g.mem("LDA", head)
		g.imp("SEC")
		g.mem("SBC", cnt)
		g.imp("CLC")
		g.part("ADC", r.lo())
		g.free(used)
	}
	if v.Len < 256 {
		g.imm("AND", v.Len-1)
	}
	g.imp("TAY")
	g.memY("LDA", data)
}

func (g *gen) ringLast(v *Var, k lang.Expr) {
	head := asm.S(v.Head.Label, 0)
	data := asm.S(v.Label, 0)
	if c, ok := g.c.constEval(k); ok {
		g.mem("LDA", head)
		g.imp("SEC")
		g.imm("SBC", c+1)
	} else {
		var r val
		used := 0
		if s, ok := g.simple(k); ok {
			r = s
		} else {
			g.genByte(k)
			t := g.temp(1)
			g.mem("STA", t)
			r = memVal(t, TByte)
			used = 1
		}
		g.mem("LDA", head)
		g.imp("CLC") // head - k - 1
		g.part("SBC", r.lo())
		g.free(used)
	}
	if v.Len < 256 {
		g.imm("AND", v.Len-1)
	}
	g.imp("TAY")
	g.memY("LDA", data)
}

func (g *gen) ringFull(x *lang.Call, label string, jit bool) {
	g.argc(x, 1)
	v := g.ringArg(x)
	cnt := asm.S(v.Count.Label, 0)
	if v.Len == 256 {
		g.mem("LDA", asm.S(cnt.Sym, 1))
		if jit {
			g.br("BNE", label)
		} else {
			g.br("BEQ", label)
		}
		return
	}
	g.mem("LDA", cnt)
	g.imm("CMP", v.Len)
	if jit {
		g.br("BEQ", label)
	} else {
		g.br("BNE", label)
	}
}

// ---- inline assembler ----

func (g *gen) asmBlock(x *lang.Asm) {
	prefix := g.newLabel("asm")
	local := map[string]bool{}
	for _, line := range x.Lines {
		l, err := asm.ParseLine(line, func(string) (asm.Operand, bool) { return asm.N(0), true })
		if err == nil && l.Label != "" {
			local[l.Label] = true
		}
	}
	resolve := func(name string) (asm.Operand, bool) {
		if local[name] {
			return asm.S(prefix+"_"+name, 0), true
		}
		switch s := g.lookup(name).(type) {
		case *Var:
			return g.vop(s), true
		case *constVal:
			return asm.N(s.V), true
		case *Proc:
			if s.Extern {
				return asm.N(s.Addr), true
			}
			in := g.instFor(s, x.Pos)
			return asm.S(in.Label, 0), true
		}
		return asm.Operand{}, false
	}
	for i, line := range x.Lines {
		pos := lang.Pos{File: x.Pos.File, Line: x.First + i, Col: 1}
		g.at(pos)
		l, err := asm.ParseLine(line, resolve)
		if err != nil {
			g.fail(pos, "asm: %v", err)
		}
		if l.Label != "" {
			g.label(prefix + "_" + l.Label)
		}
		switch {
		case l.Mnem == ".BYTE":
			g.p.Bytes(l.Data...)
		case l.Mnem != "":
			g.e(l.Mnem, l.Mode, l.Arg)
		}
	}
}

// ---- whole program generation ----

func (c *Compiler) generate() {
	c.p = asm.New(c.org)
	c.mainInst = &Inst{Main: true, Ctx: "main", Label: "main", FrameSym: "F_main", Calls: map[*Inst]bool{}}
	c.insts = append(c.insts, c.mainInst)

	// interrupt handlers are roots
	var handlers []*Inst
	for _, p := range c.procs {
		if p.Interrupt != "" {
			label := "p_" + p.Name
			in := &Inst{Proc: p, Ctx: p.Name, Label: label, FrameSym: "F_" + label, Calls: map[*Inst]bool{}}
			p.Insts[p.Name] = in
			c.insts = append(c.insts, in)
			c.work = append(c.work, in)
			handlers = append(handlers, in)
		}
	}

	g := &gen{c: c, p: c.p, inst: c.mainInst}
	g.label("main")
	g.stmts(c.mainBody)
	g.at(lang.Pos{})
	g.label("__exit")
	if c.T.Exit >= 0 {
		g.e("JMP", cpu.Abs, asm.N(c.T.Exit))
	} else {
		g.jmp("__exit")
	}

	for len(c.work) > 0 {
		in := c.work[0]
		c.work = c.work[1:]
		c.genProc(in)
	}
	c.runtime()

	// data: const tables, literals, initial value images
	used := c.usedVars()
	for _, v := range c.gvars {
		if v.Const && used[v] {
			c.p.Label(v.Label)
			c.p.Bytes(v.Init...)
		}
	}
	for i := 0; i < len(c.data); i++ {
		c.data[i]()
	}

	// startup code goes before the main program
	usedNow := c.usedVars()
	body := c.p.Items
	c.p.Items = nil
	c.startup(handlers, usedNow)
	c.p.Items = append(c.p.Items, body...)
	// initial value images of arrays (created in startup)
	for _, f := range c.images {
		f()
	}
}

func (c *Compiler) genProc(in *Inst) {
	p := in.Proc
	g := &gen{c: c, p: c.p, inst: in}
	g.at(p.Decl.Pos)
	g.label(in.Label)
	if p.Interrupt != "" {
		g.imp("PHA")
		g.imp("TXA")
		g.imp("PHA")
		g.imp("TYA")
		g.imp("PHA")
		if p.Interrupt == "irq" && c.T.Brk >= 0 {
			body := g.newLabel("irq")
			g.imp("TSX")
			g.e("LDA", cpu.AbsX, asm.N(0x0104))
			g.imm("AND", 0x10)
			g.br("BEQ", body)
			g.imp("PLA")
			g.imp("TAY")
			g.imp("PLA")
			g.imp("TAX")
			g.imp("PLA")
			g.e("JMP", cpu.Abs, asm.N(c.T.Brk))
			g.label(body)
		}
	} else {
		var slots []int
		for _, prm := range p.Params {
			slots = append(slots, prm.Off)
			if prm.Type().wide() {
				slots = append(slots, prm.Off+1)
			}
		}
		st := []string{"STA", "STX", "STY"}
		for i := 0; i < len(slots) && i < 3; i++ {
			g.mem(st[i], asm.S(in.FrameSym, slots[i]))
		}
	}
	g.stmts(p.Decl.Body)
	g.at(p.Decl.EndPos)
	if p.Interrupt != "" {
		g.label(in.Label + "_end")
		g.imp("PLA")
		g.imp("TAY")
		g.imp("PLA")
		g.imp("TAX")
		g.imp("PLA")
		g.imp("RTI")
		return
	}
	if p.Ret != TVoid && !endsWithJump(p.Decl.Body) {
		c.warn(p.Decl.EndPos, "procedure %s may end without return; it returns an undefined value", p.Name)
	}
	if !endsWithJump(p.Decl.Body) {
		g.imp("RTS")
	}
}

// startup: CLD, initialization of global variables, interrupt vectors.
func (c *Compiler) startup(handlers []*Inst, used map[*Var]bool) {
	p := c.p
	p.CurLine, p.CurFile = 0, ""
	p.Label("start")
	p.Imp("CLD")
	var zeros []asm.Operand
	type nz struct {
		op asm.Operand
		v  asm.Operand
	}
	var nonzero []nz
	var arrays []*Var
	for _, v := range c.gvars {
		if !used[v] || v.Const || v.Fixed || (v.NoInit && v.HasInit) {
			continue
		}
		if v.NoInit || v.Hidden && (v == c.mp || v == c.rt) {
			continue
		}
		if v.Ring {
			continue // ring data is not cleared, only head and count
		}
		if !v.Scalar() {
			arrays = append(arrays, v)
			continue
		}
		for i := 0; i < v.Size(); i++ {
			op := asm.S(v.Label, i)
			if v.HasInit && i < len(v.Init) && !(v.Init[i].Sym == "" && v.Init[i].Off == 0) {
				nonzero = append(nonzero, nz{op, v.Init[i]})
			} else {
				zeros = append(zeros, op)
			}
		}
	}
	if len(zeros) > 0 {
		p.I("LDA", cpu.Imm, asm.N(0))
		for _, op := range zeros {
			p.I("STA", asm.Auto, op)
		}
	}
	for _, n := range nonzero {
		p.I("LDA", cpu.Imm, n.v)
		p.I("STA", asm.Auto, n.op)
	}
	for _, v := range arrays {
		size := v.Size()
		img := ""
		if v.HasInit {
			img = "__img_" + v.Label
			data := v.Init
			c.images = append(c.images, func() {
				p.Label(img)
				p.Bytes(data...)
			})
		} else {
			p.I("LDA", cpu.Imm, asm.N(0))
		}
		for off := 0; off < size; off += 256 {
			m := size - off
			if m > 256 {
				m = 256
			}
			loop := c.newLabel("init")
			if m == 256 {
				p.I("LDX", cpu.Imm, asm.N(0))
				p.Label(loop)
				if img != "" {
					p.I("LDA", cpu.AbsX, asm.S(img, off))
				}
				p.I("STA", asm.AutoX, asm.S(v.Label, off))
				p.Imp("INX")
			} else {
				p.I("LDX", cpu.Imm, asm.N(m))
				p.Label(loop)
				if img != "" {
					p.I("LDA", cpu.AbsX, asm.S(img, off-1))
				}
				p.I("STA", asm.AutoX, asm.S(v.Label, off-1))
				p.Imp("DEX")
			}
			p.I("BNE", cpu.Rel, asm.S(loop, 0))
		}
	}
	// shadow registers with an initial value
	for _, g := range c.globals {
		if v, ok := g.(*Var); ok && v.Shadow != nil && v.ShadowInit && used[v.Shadow] {
			p.I("LDA", asm.Auto, asm.S(v.Shadow.Label, 0))
			p.I("STA", asm.Auto, asm.N(v.Addr))
		}
	}
	cli := false
	for _, h := range handlers {
		vec := c.T.VectorIRQ
		if h.Proc.Interrupt == "nmi" {
			vec = c.T.VectorNMI
		} else {
			cli = true
		}
		if vec < 0 {
			c.fail(h.Proc.Decl.Pos, "target %s has no vector for %s", c.T.Name, h.Proc.Interrupt)
		}
		lo, hi := asm.S(h.Label, 0), asm.S(h.Label, 0)
		lo.Part, hi.Part = asm.Lo, asm.Hi
		p.I("LDA", cpu.Imm, lo)
		p.I("STA", cpu.Abs, asm.N(vec))
		p.I("LDA", cpu.Imm, hi)
		p.I("STA", cpu.Abs, asm.N(vec+1))
	}
	if cli {
		p.Imp("CLI")
	}
}

// runtime adds the multiplication and division routines when needed.
func (c *Compiler) runtime() {
	p := c.p
	p.CurLine, p.CurFile = 0, ""
	c.delayRuntime()
	rt := func(k int) asm.Operand { return asm.S("__rt", k) }
	if c.needMul {
		p.Label("__mul16") // rt0..1 * rt2..3 -> A/X (rt4..5)
		p.I("LDA", cpu.Imm, asm.N(0))
		p.I("STA", asm.Auto, rt(4))
		p.I("STA", asm.Auto, rt(5))
		p.I("LDY", cpu.Imm, asm.N(16))
		p.Label("__mul16_l")
		p.I("LSR", asm.Auto, rt(3))
		p.I("ROR", asm.Auto, rt(2))
		p.I("BCC", cpu.Rel, asm.S("__mul16_s", 0))
		p.Imp("CLC")
		p.I("LDA", asm.Auto, rt(4))
		p.I("ADC", asm.Auto, rt(0))
		p.I("STA", asm.Auto, rt(4))
		p.I("LDA", asm.Auto, rt(5))
		p.I("ADC", asm.Auto, rt(1))
		p.I("STA", asm.Auto, rt(5))
		p.Label("__mul16_s")
		p.I("ASL", asm.Auto, rt(0))
		p.I("ROL", asm.Auto, rt(1))
		p.Imp("DEY")
		p.I("BNE", cpu.Rel, asm.S("__mul16_l", 0))
		p.I("LDA", asm.Auto, rt(4))
		p.I("LDX", asm.Auto, rt(5))
		p.Imp("RTS")
	}
	if c.needDiv {
		p.Label("__div16") // rt0..1 / rt2..3 -> quotient A/X (rt0..1), remainder rt4..5
		p.I("LDA", cpu.Imm, asm.N(0))
		p.I("STA", asm.Auto, rt(4))
		p.I("STA", asm.Auto, rt(5))
		p.I("LDY", cpu.Imm, asm.N(16))
		p.Label("__div16_l")
		p.I("ASL", asm.Auto, rt(0))
		p.I("ROL", asm.Auto, rt(1))
		p.I("ROL", asm.Auto, rt(4))
		p.I("ROL", asm.Auto, rt(5))
		p.I("BCS", cpu.Rel, asm.S("__div16_d", 0))
		p.I("LDA", asm.Auto, rt(4))
		p.I("CMP", asm.Auto, rt(2))
		p.I("LDA", asm.Auto, rt(5))
		p.I("SBC", asm.Auto, rt(3))
		p.I("BCC", cpu.Rel, asm.S("__div16_s", 0))
		p.Label("__div16_d")
		p.I("LDA", asm.Auto, rt(4))
		p.Imp("SEC")
		p.I("SBC", asm.Auto, rt(2))
		p.I("STA", asm.Auto, rt(4))
		p.I("LDA", asm.Auto, rt(5))
		p.I("SBC", asm.Auto, rt(3))
		p.I("STA", asm.Auto, rt(5))
		p.I("INC", asm.Auto, rt(0))
		p.Label("__div16_s")
		p.Imp("DEY")
		p.I("BNE", cpu.Rel, asm.S("__div16_l", 0))
		p.I("LDA", asm.Auto, rt(0))
		p.I("LDX", asm.Auto, rt(1))
		p.Imp("RTS")
	}
}

func hexBytes(b []byte) string {
	var s []string
	for _, x := range b {
		s = append(s, fmt.Sprintf("%02X", x))
	}
	return strings.Join(s, " ")
}
