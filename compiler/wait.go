package compiler

import (
	"fmt"

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
	loop   bool // timeout counted in loop passes, not with the timer
	timed  *asm.TimedLoop
	base   int
	plan   timeoutPlan
	tcnt   asm.Operand
	tmoLbl string
	skip   string      // where the timeout path continues
	block  []lang.Stmt // timeout ... then block
}

// loopTimeout reports whether wait ... timeout counts loop passes instead of using the timer.
func (c *Compiler) loopTimeout() bool {
	mode := c.timeoutMode
	if mode == "" {
		mode = c.T.TimeoutMode
	}
	if mode == "" {
		return c.T.TimeoutTimer < 0
	}
	return mode == "loop"
}

func (w *waitCtx) setup(x *lang.Wait) {
	g := w.g
	if x.Timeout == nil {
		return
	}
	if g.tmoSkip == "" {
		g.fail(x.Pos, "wait with a timeout must be a statement of its own")
	}
	w.tmo = true
	w.skip, w.block = g.tmoSkip, x.OnTimeout
	g.tmoSkip = ""
	w.loop = g.c.loopTimeout()
	w.base = g.c.T.TimeoutTimer
	if !w.loop && w.base < 0 {
		g.fail(x.Pos, "target %s has no timer for wait ... timeout; use .timeout loop", g.c.T.Name)
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
	w.tmoLbl = g.newLabel("tmo")
	if w.loop {
		// a 24-bit pass counter; the assembler computes its start value after layout
		for _, p := range g.c.procs {
			if p.Interrupt != "" {
				g.warn(x.Pos, "time spent in interrupt handlers is not counted by the loop timeout; it gets longer")
				break
			}
		}
		w.tcnt = g.temp(3)
		w.timed = &asm.TimedLoop{Cycles: cycles}
		for i := range w.timed.Init {
			w.timed.Init[i] = fmt.Sprintf("%s_n%d", w.tmoLbl, i)
			g.e("LDA", cpu.Imm, asm.S(w.timed.Init[i], 0))
			g.mem("STA", asm.S(w.tcnt.Sym, w.tcnt.Off+i))
		}
		g.p.Timed(w.timed)
		return
	}
	w.plan = planTimeout(cycles)
	if w.plan.reloads > 0 {
		w.tcnt = g.temp(1)
		g.imm("LDA", w.plan.reloads)
		g.mem("STA", w.tcnt)
	}
	g.imm("LDA", w.plan.first)
	g.e("STA", cpu.Abs, asm.N(w.base+w.plan.off))
}

// check: timeout test at the end of a wait loop pass; with last set, the
// timeout code follows right after it.
func (w *waitCtx) check(loop string, last bool) {
	g := w.g
	if w.loop {
		count := g.newLabel("tcnt")
		w.timed.Passes = append(w.timed.Passes, asm.Pass{Start: loop, Count: count})
		g.label(count)
		for i := 0; i < 3; i++ {
			g.mem("DEC", asm.S(w.tcnt.Sym, w.tcnt.Off+i))
			g.br("BNE", loop)
		}
		if !last {
			g.jmp(w.tmoLbl)
		}
		return
	}
	g.e("BIT", cpu.Abs, asm.N(w.base+3))
	g.br("BPL", loop)
	if w.plan.reloads > 0 {
		g.mem("LDA", w.tcnt)
		g.br("BEQ", w.tmoLbl)
		g.mem("DEC", w.tcnt)
		g.imm("LDA", 255)
		g.e("STA", cpu.Abs, asm.N(w.base+3))
		g.jmp(loop)
	} else if !last {
		g.jmp(w.tmoLbl)
	}
}

// finish generates the timeout path: the block after then, and a jump past
// the rest of the statement, so an assignment or push does not happen.
func (w *waitCtx) finish(x *lang.Wait) {
	g := w.g
	if !w.tmo {
		return
	}
	g.label(w.tmoLbl)
	if w.loop {
		g.free(3)
	} else if w.plan.reloads > 0 {
		g.free(1)
	}
	g.stmts(w.block)
	if !endsWithJump(w.block) {
		g.jmp(w.skip)
	}
	g.at(x.Pos)
}

// genWait generates a wait; the result (the port byte) is in A.
func (g *gen) genWait(x *lang.Wait) {
	w := &waitCtx{g: g}
	done := g.newLabel("wdone")
	switch x.Kind {
	case "cond":
		w.setup(x)
		loop := g.newLabel("wait")
		g.label(loop)
		if w.tmo {
			g.cond(x.Cond, done, true)
			w.check(loop, true)
		} else {
			g.cond(x.Cond, loop, false)
		}
		w.finish(x)
		g.label(done)
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
		// ref: the value the port is compared with, masked for a bit field
		var ref part
		usedTemp := false
		if x.From != nil && g.typeOf(x.From).wide() {
			g.fail(x.From.P(), "from needs a byte value")
		}
		if v, ok := g.simpleFrom(x.From); ok && (mask == 0xFF || v.imm) {
			ref = v.lo()
			if v.imm && mask != 0xFF {
				ref.op = asm.N(ref.op.Off & mask)
			}
		} else {
			t := g.temp(1)
			ref, usedTemp = part{op: t}, true
			if x.From != nil {
				g.genByte(x.From)
			} else {
				g.mem("LDA", src)
			}
			if mask != 0xFF {
				g.imm("AND", mask)
			}
			g.mem("STA", t)
		}
		w.setup(x)
		loop := g.newLabel("wait")
		g.label(loop)
		g.mem("LDA", src)
		if mask != 0xFF {
			g.imp("TAX")
			g.imm("AND", mask)
		}
		g.part("CMP", ref)
		if w.tmo {
			g.br("BNE", done)
			w.check(loop, true)
		} else {
			g.br("BEQ", loop)
		}
		w.finish(x)
		g.label(done)
		if mask != 0xFF {
			g.imp("TXA")
		}
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
		// With a loop-counted timeout both phases share one pass counter, so
		// phase 1 takes as many cycles per pass as phase 2 (LDA/TAX/AND).
		same := w.loop && m != 0x80
		test := func(set bool, target string) {
			switch {
			case m == 0x80:
				g.mem("BIT", src)
				g.br(map[bool]string{true: "BMI", false: "BPL"}[set], target)
			case m == 0x40 && !same:
				g.mem("BIT", src)
				g.br(map[bool]string{true: "BVS", false: "BVC"}[set], target)
			default:
				g.mem("LDA", src)
				if same {
					g.imp("TAX")
				}
				g.imm("AND", m)
				g.br(map[bool]string{true: "BNE", false: "BEQ"}[set], target)
			}
		}
		g.label(p1)
		if w.tmo {
			test(!rising, p2) // opposite level reached
			w.check(p1, false)
		} else {
			test(rising, p1) // while still at the starting level
		}
		g.label(p2)
		// phase 2: wait for the target level and return the whole byte
		if m == 0x80 {
			g.mem("LDA", src)
			if w.tmo {
				g.br(map[bool]string{true: "BMI", false: "BPL"}[rising], done)
				w.check(p2, true)
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
				w.check(p2, true)
			} else {
				g.br(map[bool]string{true: "BEQ", false: "BNE"}[rising], p2)
			}
		}
		w.finish(x)
		g.label(done)
		if usesX {
			g.imp("TXA")
		}
	}
}

// simpleFrom returns the comparison value of change ... from when it can be
// used directly (a constant or a variable), without a temporary copy.
func (g *gen) simpleFrom(e lang.Expr) (val, bool) {
	if e == nil {
		return val{}, false
	}
	if c, ok := g.c.constEval(e); ok {
		return val{imm: true, op: asm.N(c & 0xFF), t: TByte}, true
	}
	return g.simple(e)
}
