package asm

import (
	"fmt"

	"github.com/jrydval/tantal-6502-compiled-language/cpu"
)

// TimedLoop is a wait loop whose timeout is measured by counting loop passes.
// Each pass ends with the counter code at a Pass.Count label:
//
//	Count: DEC c   / BNE Start
//	       DEC c+1 / BNE Start
//	       DEC c+2 / BNE Start
//
// After layout, the assembler finds the shortest pass and stores the initial
// counter bytes in Equ[Init[0..2]], so the timeout never expires early.
type TimedLoop struct {
	Cycles int       // wanted timeout in CPU cycles
	Init   [3]string // Equ symbols for the initial counter bytes
	Passes []Pass
	Line   int
	File   string

	// results
	MinPass, MaxPass int // cycles of one pass without the counter code
	Count            int // number of passes until the timeout
	Total            int // cycles until the timeout with the shortest passes
}

// Pass is one loop of a timed wait: from the Start label to the counter code at Count.
type Pass struct{ Start, Count string }

// Timed registers a timed loop; its Line and File are taken from the current position.
func (p *Program) Timed(t *TimedLoop) {
	t.Line, t.File = p.CurLine, p.CurFile
	p.Loops = append(p.Loops, t)
}

// branchCost returns the cycles of a branch item when taken and when not taken.
func branchCost(it *Item, target int) (taken, not int) {
	cross := func(a, b int) int {
		if a>>8 != b>>8 {
			return 1
		}
		return 0
	}
	if it.Long {
		// inverted Bxx +3 over a JMP: "not taken" means the inverted branch is taken
		return 2 + 3, 3 + cross(it.Addr+2, it.Addr+5)
	}
	return 3 + cross(it.Addr+2, target), 2
}

func (p *Program) timedErr(t *TimedLoop, it *Item, f string, a ...any) error {
	if it == nil || it.Line == 0 {
		it = &Item{Line: t.Line, File: t.File}
	}
	return p.errAt(it, fmt.Errorf(f, a...))
}

// resolveTimed computes the counter values of all timed loops; addresses must be final.
func (p *Program) resolveTimed(labels map[string]int) error {
	index := map[string]int{}
	for i, it := range p.Items {
		if it.Kind == KLabel {
			index[it.Name] = i
		}
	}
	target := func(it *Item) (int, error) { return p.value(it.Arg, labels, true) }
	for _, t := range p.Loops {
		type seg struct {
			pass   int    // shortest pass to the counter code
			dec    [3]int // cycles of the DEC instructions
			bt, bn [3]int // cycles of the BNE instructions, taken / not taken
		}
		var segs []seg
		t.MinPass, t.MaxPass = -1, -1
		for _, ps := range t.Passes {
			s, k := index[ps.Start], index[ps.Count]
			type span struct {
				min, max int
				ok       bool
			}
			memo := map[int]span{}
			var walk func(i int) (span, error)
			walk = func(i int) (span, error) {
				if i == k {
					return span{0, 0, true}, nil
				}
				if r, ok := memo[i]; ok {
					return r, nil
				}
				// a path that leaves [s, k] exits the wait and does not count
				if i < s || i > k || i >= len(p.Items) {
					return span{}, nil
				}
				it := p.Items[i]
				var r span
				add := func(lo, hi int, next int) error {
					if next <= i {
						return p.timedErr(t, it, "wait ... timeout: the wait condition contains a loop; use .timeout timer")
					}
					sub, err := walk(next)
					if err != nil || !sub.ok {
						return err
					}
					if !r.ok || lo+sub.min < r.min {
						r.min = lo + sub.min
					}
					if !r.ok || hi+sub.max > r.max {
						r.max = hi + sub.max
					}
					r.ok = true
					return nil
				}
				var err error
				switch {
				case it.Kind == KLabel:
					err = add(0, 0, i+1)
				case it.Kind != KInstr:
					err = p.timedErr(t, it, "internal error: data inside a timed wait loop")
				case cpu.IsBranch(it.Mnem):
					to, e := target(it)
					if e != nil {
						return r, p.errAt(it, e)
					}
					taken, not := branchCost(it, to)
					if err = add(not, not, i+1); err == nil {
						if j, ok := index[it.Arg.Sym]; ok && it.Arg.Off == 0 && j >= s && j <= k {
							err = add(taken, taken, j)
						}
					}
				case it.Mnem == "JMP" && it.Mode == cpu.Abs:
					if j, ok := index[it.Arg.Sym]; ok && it.Arg.Off == 0 && j >= s && j <= k {
						err = add(3, 3, j)
					}
				case it.Mnem == "JMP" || it.Mnem == "JSR" || it.Mnem == "RTS" || it.Mnem == "RTI" || it.Mnem == "BRK":
					err = p.timedErr(t, it, "wait ... timeout: %s in the wait condition, its time is unknown; use .timeout timer", it.Mnem)
				default:
					c, extra := it.Cycles()
					hi := c
					if extra != "" {
						hi++
					}
					err = add(c, hi, i+1)
				}
				if err != nil {
					return r, err
				}
				memo[i] = r
				return r, nil
			}
			r, err := walk(s)
			if err != nil {
				return err
			}
			if !r.ok {
				return p.timedErr(t, nil, "internal error: timed wait loop never reaches its counter")
			}
			var sg seg
			sg.pass = r.min
			for n := 0; n < 3; n++ {
				dec, bne := k+1+2*n, k+2+2*n
				if bne >= len(p.Items) || p.Items[dec].Mnem != "DEC" || p.Items[bne].Mnem != "BNE" {
					return p.timedErr(t, nil, "internal error: unexpected timed wait counter code")
				}
				sg.dec[n], _ = p.Items[dec].Cycles()
				to, e := target(p.Items[bne])
				if e != nil {
					return p.errAt(p.Items[bne], e)
				}
				sg.bt[n], sg.bn[n] = branchCost(p.Items[bne], to)
			}
			segs = append(segs, sg)
			if t.MinPass < 0 || r.min < t.MinPass {
				t.MinPass = r.min
			}
			if r.max > t.MaxPass {
				t.MaxPass = r.max
			}
		}
		// cycles of n passes, the counter code included, for one loop
		total := func(sg seg, n int) int {
			m := n - 1
			d1 := m/256 + 1   // executions of DEC c+1
			d2 := m/65536 + 1 // executions of DEC c+2
			return n*(sg.pass+sg.dec[0]) + (n-d1)*sg.bt[0] + d1*(sg.bn[0]+sg.dec[1]) +
				(d1-d2)*sg.bt[1] + d2*(sg.bn[1]+sg.dec[2]) + (d2-1)*sg.bt[2] + sg.bn[2]
		}
		shortest := func(n int) int {
			v := -1
			for _, sg := range segs {
				if x := total(sg, n); v < 0 || x < v {
					v = x
				}
			}
			return v
		}
		const maxCount = 1 << 24
		if shortest(maxCount) < t.Cycles {
			return p.timedErr(t, nil, "wait ... timeout: %d cycles is too long for a loop counter; use .timeout timer", t.Cycles)
		}
		lo, hi := 1, maxCount
		for lo < hi {
			mid := (lo + hi) / 2
			if shortest(mid) >= t.Cycles {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		t.Count, t.Total = lo, shortest(lo)
		m := lo - 1
		p.Equ[t.Init[0]] = (m%256 + 1) & 0xFF
		p.Equ[t.Init[1]] = ((m/256)%256 + 1) & 0xFF
		p.Equ[t.Init[2]] = (m/65536 + 1) & 0xFF
	}
	return nil
}
