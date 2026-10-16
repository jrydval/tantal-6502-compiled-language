package compiler

import (
	"github.com/jrydval/tantal-6502-compiled-language/asm"
	"github.com/jrydval/tantal-6502-compiled-language/cpu"
	"github.com/jrydval/tantal-6502-compiled-language/lang"
)

// timeoutPlan: how to program the RIOT timer for a given timeout.
type timeoutPlan struct {
	off     int // offset of the write address (divider 1, 8, 64, 1024)
	first   int // ticks of the first countdown
	reloads int // number of further countdowns of 255 ticks (divider 1024)
}

func planTimeout(us int) timeoutPlan {
	divs := []int{1, 8, 64, 1024}
	for k, d := range divs {
		t := (us + d - 1) / d
		if t <= 255 {
			if t < 1 {
				t = 1
			}
			return timeoutPlan{off: k, first: t}
		}
	}
	total := (us + 1023) / 1024
	n := (total + 254) / 255
	return timeoutPlan{off: 3, first: total - (n-1)*255, reloads: n - 1}
}

type waitCtx struct {
	g      *gen
	tmo    bool
	base   int
	plan   timeoutPlan
	tcnt   asm.Operand
	tmoLbl string
}

func (w *waitCtx) setup(x *lang.Wait) {
	g := w.g
	if x.Timeout == nil {
		return
	}
	w.tmo = true
	w.base = g.c.T.TimeoutTimer
	if w.base < 0 {
		g.fail(x.Pos, "target %s has no timer for wait ... timeout", g.c.T.Name)
	}
	v, ok := g.c.constEval(x.Timeout)
	if !ok || v <= 0 {
		g.fail(x.Pos, "timeout must be a positive constant")
	}
	mult := map[string]int{"us": 1, "ms": 1000, "s": 1000000}[x.Unit]
	clk := g.c.T.Clock
	us := v * mult
	if us > 60000000 {
		g.fail(x.Pos, "timeout can be at most 60 s")
	}
	// convert to CPU cycles (the timer counts clock ticks)
	cycles := int(int64(us) * int64(clk) / 1000000)
	w.plan = planTimeout(cycles)
	w.tmoLbl = g.newLabel("tmo")
	to := asm.S(g.c.useTimedOut().Label, 0)
	g.imm("LDA", 0)
	g.mem("STA", to)
	if w.plan.reloads > 0 {
		w.tcnt = g.temp(1)
		g.imm("LDA", w.plan.reloads)
		g.mem("STA", w.tcnt)
	}
	g.imm("LDA", w.plan.first)
	g.e("STA", cpu.Abs, asm.N(w.base+w.plan.off))
}

// check: timer test at the end of a wait loop pass.
func (w *waitCtx) check(loop string) {
	g := w.g
	g.e("BIT", cpu.Abs, asm.N(w.base+3))
	g.br("BPL", loop)
	if w.plan.reloads > 0 {
		g.mem("LDA", w.tcnt)
		g.br("BEQ", w.tmoLbl)
		g.mem("DEC", w.tcnt)
		g.imm("LDA", 255)
		g.e("STA", cpu.Abs, asm.N(w.base+3))
		g.jmp(loop)
	} else {
		g.jmp(w.tmoLbl)
	}
}

func (w *waitCtx) finish(read func(), done, end string) {
	g := w.g
	if !w.tmo {
		return
	}
	g.label(w.tmoLbl)
	g.imm("LDA", 1)
	g.mem("STA", asm.S(g.c.useTimedOut().Label, 0))
	if read != nil {
		read()
	}
	g.jmp(end)
	if w.plan.reloads > 0 {
		g.free(1)
	}
}

// genWait generates a wait; the result (the port byte) is in A.
func (g *gen) genWait(x *lang.Wait) {
	w := &waitCtx{g: g}
	done, end := g.newLabel("wdone"), g.newLabel("wend")
	switch x.Kind {
	case "cond":
		w.setup(x)
		loop := g.newLabel("wait")
		g.label(loop)
		if w.tmo {
			g.cond(x.Cond, done, true)
			w.check(loop)
		} else {
			g.cond(x.Cond, loop, false)
		}
		w.finish(nil, done, end)
		g.label(done)
		g.label(end)
	case "change":
		var src asm.Operand
		mask := 0xFF
		switch t := x.Target.(type) {
		case *lang.Ident:
			v, ok := g.lookup(t.Name).(*Var)
			if !ok || !v.Scalar() || v.Type() != TByte {
				g.fail(t.Pos, "wait change needs a byte variable or register")
			}
			src = g.readOp(v, t.Pos)
		case *lang.BitRef:
			b := g.bitOf(t)
			src = g.bitSrc(b, t.Pos)
			mask = b.mask()
		default:
			g.fail(x.Pos, "wait change needs a variable, register or bit")
		}
		var ref asm.Operand
		usedTemp := false
		if x.From != "" {
			if g.lookup(x.From) == nil {
				g.fail(x.Pos, "unknown name %q", x.From)
			}
			fv, ok := g.lookup(x.From).(*Var)
			if !ok || !fv.Scalar() || fv.Type() != TByte || fv.ReadOnly {
				g.fail(x.Pos, "from needs a byte variable")
			}
			ref = g.vop(fv)
		} else {
			ref = g.temp(1)
			usedTemp = true
			g.mem("LDA", src)
			if mask != 0xFF {
				g.imm("AND", mask)
			}
			g.mem("STA", ref)
		}
		w.setup(x)
		loop := g.newLabel("wait")
		g.label(loop)
		g.mem("LDA", src)
		if mask != 0xFF {
			g.imp("TAX")
			g.imm("AND", mask)
		}
		g.mem("CMP", ref)
		if w.tmo {
			g.br("BNE", done)
			w.check(loop)
		} else {
			g.br("BEQ", loop)
		}
		w.finish(func() { g.mem("LDA", src) }, done, end)
		g.label(done)
		if x.From != "" {
			g.mem("STA", ref)
		}
		if mask != 0xFF {
			g.imp("TXA")
		}
		g.label(end)
		if usedTemp {
			g.free(1)
		}
	case "rising", "falling":
		b := g.bitOf(x.Target.(*lang.BitRef))
		if b.Lo != b.Hi {
			g.fail(x.Pos, "wait %s needs a single bit", x.Kind)
		}
		src := g.bitSrc(b, x.Pos)
		m := b.mask()
		rising := x.Kind == "rising"
		w.setup(x)
		p1, p2 := g.newLabel("w1"), g.newLabel("w2")
		usesX := false
		// phase 1: wait for the opposite level; test branches when the bit is set/clear
		test := func(set bool, target string) {
			switch m {
			case 0x80:
				g.mem("BIT", src)
				g.br(map[bool]string{true: "BMI", false: "BPL"}[set], target)
			case 0x40:
				g.mem("BIT", src)
				g.br(map[bool]string{true: "BVS", false: "BVC"}[set], target)
			default:
				g.mem("LDA", src)
				g.imm("AND", m)
				g.br(map[bool]string{true: "BNE", false: "BEQ"}[set], target)
			}
		}
		g.label(p1)
		if w.tmo {
			test(!rising, p2) // opposite level reached
			w.check(p1)
		} else {
			test(rising, p1) // while still at the starting level
		}
		g.label(p2)
		// phase 2: wait for the target level and return the whole byte
		if m == 0x80 {
			g.mem("LDA", src)
			if w.tmo {
				g.br(map[bool]string{true: "BMI", false: "BPL"}[rising], done)
				w.check(p2)
			} else {
				g.br(map[bool]string{true: "BPL", false: "BMI"}[rising], p2)
			}
		} else {
			usesX = true
			g.mem("LDA", src)
			g.imp("TAX")
			g.imm("AND", m)
			if w.tmo {
				g.br(map[bool]string{true: "BNE", false: "BEQ"}[rising], done)
				w.check(p2)
			} else {
				g.br(map[bool]string{true: "BEQ", false: "BNE"}[rising], p2)
			}
		}
		w.finish(func() { g.mem("LDA", src) }, done, end)
		g.label(done)
		if usesX {
			g.imp("TXA")
		}
		g.label(end)
	}
}
