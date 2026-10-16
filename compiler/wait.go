package compiler

import (
	"tantal/asm"
	"tantal/cpu"
	"tantal/lang"
)

// timeoutPlan: jak nastavit časovač RIOT pro daný limit.
type timeoutPlan struct {
	off     int // posun zápisové adresy (dělitel 1, 8, 64, 1024)
	first   int // počet tiků prvního odpočtu
	reloads int // počet dalších odpočtů po 255 tiků (dělitel 1024)
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
		g.fail(x.Pos, "target %s nemá časovač pro wait ... timeout", g.c.T.Name)
	}
	v, ok := g.c.constEval(x.Timeout)
	if !ok || v <= 0 {
		g.fail(x.Pos, "časový limit musí být kladná konstanta")
	}
	mult := map[string]int{"us": 1, "ms": 1000, "s": 1000000}[x.Unit]
	clk := g.c.T.Clock
	us := v * mult
	if us > 60000000 {
		g.fail(x.Pos, "časový limit může být nejvýš 60 s")
	}
	// převod na cykly procesoru (časovač počítá takty)
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

// check: kontrola časovače na konci průchodu čekací smyčkou.
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

// genWait vygeneruje čekání; výsledek (bajt portu) je v A.
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
				g.fail(t.Pos, "wait change vyžaduje byte proměnnou nebo registr")
			}
			src = g.readOp(v, t.Pos)
		case *lang.BitRef:
			b := g.bitOf(t)
			src = g.bitSrc(b, t.Pos)
			mask = b.mask()
		default:
			g.fail(x.Pos, "wait change vyžaduje proměnnou, registr nebo bit")
		}
		var ref asm.Operand
		usedTemp := false
		if x.From != "" {
			if g.lookup(x.From) == nil {
				g.fail(x.Pos, "neznámé jméno %q", x.From)
			}
			fv, ok := g.lookup(x.From).(*Var)
			if !ok || !fv.Scalar() || fv.Type() != TByte || fv.ReadOnly {
				g.fail(x.Pos, "from vyžaduje byte proměnnou")
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
			g.fail(x.Pos, "wait %s vyžaduje jeden bit", x.Kind)
		}
		src := g.bitSrc(b, x.Pos)
		m := b.mask()
		rising := x.Kind == "rising"
		w.setup(x)
		p1, p2 := g.newLabel("w1"), g.newLabel("w2")
		usesX := false
		// fáze 1: počkat na opačnou úroveň; testBranch skočí, když je bit nastaven/nulový
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
			test(!rising, p2) // opačná úroveň dosažena
			w.check(p1)
		} else {
			test(rising, p1) // dokud je stále výchozí úroveň
		}
		g.label(p2)
		// fáze 2: počkat na cílovou úroveň a vrátit celý bajt
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
