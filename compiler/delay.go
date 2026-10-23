package compiler

import (
	"github.com/jrydval/tantal-6502-compiled-language/asm"
	"github.com/jrydval/tantal-6502-compiled-language/cpu"
	"github.com/jrydval/tantal-6502-compiled-language/lang"
)

// delay N us|ms|s|cycles is a busy wait.
//
// A constant delay takes exactly N cycles (from the end of the previous
// statement to the start of the next one). Short delays are NOP padding;
// longer ones load A/X/Y and call __dly, a loop that the assembler keeps
// within one page, so the time does not depend on where the code lands.
// The inline code has no branches for the same reason.
//
// A variable delay (t ms or t s, t a byte or card) calls __dlyms, a loop of
// exact milliseconds counted in X:Y.

const (
	dlyInlineMax = 40         // up to this many cycles: NOPs only
	dlyCall      = 12         // LDA #, LDX #, LDY #, JSR
	dlyMaxA      = 255        // A counts 1..255 (SBC #1 must not borrow)
	maxDelayUS   = 60_000_000 // 60 s, as for wait ... timeout
)

// dlyCycles returns the cycles of LDA #a / LDX #x / LDY #y / JSR __dly for the
// pass index m (number of DEY executions - 1).
func dlyCycles(m int) int {
	y, x, a := m%256+1, (m/256)%256+1, m/65536+1
	ny := y + 256*(x-1) + 65536*(a-1)
	nx := x + 256*(a-1)
	return dlyCall + 5*ny + 4*nx + 4*a + 7
}

// dlyCount returns the largest pass index whose call leaves a padding the NOPs can
// make (0 or at least 2 cycles), and that padding; ok is false if c is too long.
func dlyCount(c int) (m, pad int, ok bool) {
	maxM := dlyMaxA*65536 - 1
	if dlyCycles(maxM)+14 < c {
		return 0, 0, false
	}
	lo, hi := 0, maxM
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if dlyCycles(mid) <= c {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	for lo > 0 && c-dlyCycles(lo) == 1 {
		lo--
	}
	return lo, c - dlyCycles(lo), true
}

// padCycles emits instructions taking exactly n cycles (n = 0 or n >= 2) without
// changing registers; BIT changes only the flags.
func padCycles(p *asm.Program, n int) {
	if n%2 == 1 {
		p.I("BIT", cpu.ZP, asm.N(0))
		n -= 3
	}
	for ; n > 0; n -= 2 {
		p.Imp("NOP")
	}
}

// constDelay emits an exact delay of c cycles (c = 0 or c >= 2) at the current position.
func (g *gen) constDelay(c int, pos lang.Pos) {
	if c <= dlyInlineMax {
		padCycles(g.p, c)
		return
	}
	m, pad, ok := dlyCount(c)
	if !ok {
		g.fail(pos, "delay is too long")
	}
	g.imm("LDA", m/65536+1)
	g.imm("LDX", (m/256)%256+1)
	g.imm("LDY", m%256+1)
	g.e("JSR", cpu.Abs, asm.S("__dly", 0))
	g.c.needDly = true
	padCycles(g.p, pad)
}

// aDelay emits an exact delay of c cycles using only A and the flags, as SBC
// loops; used inside __dlyms, which is kept within one page.
func aDelay(p *asm.Program, c int, label func() string) {
	for c > dlyInlineMax {
		// SEC, LDA #n, n x (SBC #1, BNE) = 5n + 3 cycles
		n := (c - 3) / 5
		if n > 255 {
			n = 255
		}
		if c-(5*n+3) == 1 {
			n--
		}
		l := label()
		p.Imp("SEC")
		p.I("LDA", cpu.Imm, asm.N(n))
		p.Label(l)
		p.I("SBC", cpu.Imm, asm.N(1))
		p.I("BNE", cpu.Rel, asm.S(l, 0))
		c -= 5*n + 3
	}
	padCycles(p, c)
}

func (g *gen) delay(x *lang.Delay) {
	clk := g.c.T.Clock
	for _, p := range g.c.procs {
		if p.Interrupt != "" {
			g.warn(x.Pos, "time spent in interrupt handlers is not counted by delay; it gets longer")
			break
		}
	}
	if v, ok := g.c.constEval(x.X); ok {
		if v < 0 {
			g.fail(x.Pos, "delay must not be negative")
		}
		var c int64
		switch x.Unit {
		case "cycles":
			c = int64(v)
		case "us":
			c = (int64(v)*int64(clk) + 500_000) / 1_000_000
		case "ms":
			c = (int64(v)*int64(clk) + 500) / 1000
		case "s":
			c = int64(v) * int64(clk)
		}
		if c > int64(clk)*maxDelayUS/1_000_000 {
			g.fail(x.Pos, "delay can be at most 60 s")
		}
		if c == 1 {
			g.fail(x.Pos, "a delay of 1 cycle cannot be made; the shortest delay is 2 cycles")
		}
		g.constDelay(int(c), x.Pos)
		return
	}
	if x.Unit != "ms" && x.Unit != "s" {
		g.fail(x.Pos, "a delay given by a variable needs ms or s")
	}
	if clk < 20_000 {
		g.fail(x.Pos, "a delay given by a variable needs a clock of at least 20 kHz")
	}
	g.c.needDlyMs = true
	wide := g.typeOf(x.X) == TCard
	if x.Unit == "ms" {
		// count in X:Y
		if wide {
			g.genWord(x.X)
			g.imp("TAY")
		} else {
			g.genByte(x.X)
			g.imp("TAY")
			g.imm("LDX", 0)
		}
		g.e("JSR", cpu.Abs, asm.S("__dlyms", 0))
		return
	}
	// t s: t times 1000 ms
	n := 1
	if wide {
		n = 2
	}
	t := g.temp(n)
	hi := asm.S(t.Sym, t.Off+1)
	if wide {
		g.genWord(x.X)
		g.mem("STA", t)
		g.mem("STX", hi)
	} else {
		g.genByte(x.X)
		g.mem("STA", t)
	}
	top, end, dec := g.newLabel("dly"), g.newLabel("dlyend"), g.newLabel("dlydec")
	g.label(top)
	g.mem("LDA", t)
	if wide {
		g.mem("ORA", hi)
	}
	g.br("BEQ", end)
	if wide {
		g.mem("LDA", t)
		g.br("BNE", dec)
		g.mem("DEC", hi)
		g.label(dec)
	}
	g.mem("DEC", t)
	g.imm("LDY", 1000&0xFF)
	g.imm("LDX", 1000>>8)
	g.e("JSR", cpu.Abs, asm.S("__dlyms", 0))
	g.jmp(top)
	g.label(end)
	g.free(n)
}

// delayRuntime adds the delay routines when needed. Each is kept within one page,
// so that its branches never cross a page and its timing is exact.
func (c *Compiler) delayRuntime() {
	p := c.p
	if c.needDly {
		// A, X, Y = pass counts (X and Y: 0 means 256); see dlyCycles
		p.Align(11)
		p.Label("__dly")
		p.Imp("SEC")
		p.Label("__dly_l")
		p.Imp("DEY")
		p.I("BNE", cpu.Rel, asm.S("__dly_l", 0))
		p.Imp("DEX")
		p.I("BNE", cpu.Rel, asm.S("__dly_l", 0))
		p.I("SBC", cpu.Imm, asm.N(1))
		p.I("BNE", cpu.Rel, asm.S("__dly_l", 0))
		p.Imp("RTS")
	}
	if c.needDlyMs {
		// X:Y = number of milliseconds; every millisecond takes exactly clock/1000
		// cycles, except that the borrow into X adds 5 cycles once per 256 ms.
		var body asm.Program
		body.Equ, body.Alias = map[string]int{}, map[string]asm.Operand{}
		label := func() string { return c.newLabel("dlyms") }
		// pass: TYA, BNE (taken), DEY, delay, JMP = 10 cycles + delay
		aDelay(&body, c.T.Clock/1000-10, label)
		size := 8 + 3 + 1 // TYA BNE TXA BEQ DEX DEY, JMP, RTS
		for _, it := range body.Items {
			if it.Kind == asm.KInstr {
				size += it.Mode.Size()
			}
		}
		p.Align(size)
		p.Label("__dlyms")
		p.Label("__dlyms_l")
		p.Imp("TYA")
		p.I("BNE", cpu.Rel, asm.S("__dlyms_d", 0))
		p.Imp("TXA")
		p.I("BEQ", cpu.Rel, asm.S("__dlyms_r", 0))
		p.Imp("DEX")
		p.Label("__dlyms_d")
		p.Imp("DEY")
		for _, it := range body.Items {
			if it.Kind == asm.KLabel {
				p.Label(it.Name)
			} else {
				p.I(it.Mnem, it.Mode, it.Arg)
			}
		}
		p.I("JMP", cpu.Abs, asm.S("__dlyms_l", 0))
		p.Label("__dlyms_r")
		p.Imp("RTS")
	}
}
