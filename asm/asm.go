// Package asm is a simple two-pass 6502 assembler with branch relaxation.
package asm

import (
	"fmt"
	"strings"

	"github.com/jrydval/tantal-6502-compiled-language/cpu"
)

// Operand parts.
const (
	Full = 0
	Lo   = 1 // <expr
	Hi   = 2 // >expr
)

// Operand is a value: a number or symbol + offset, optionally its low/high byte.
type Operand struct {
	Sym  string
	Off  int
	Part int
}

// N creates a numeric operand.
func N(v int) Operand { return Operand{Off: v} }

// S creates a symbolic operand.
func S(sym string, off int) Operand { return Operand{Sym: sym, Off: off} }

func (o Operand) String() string {
	s := ""
	switch {
	case o.Sym == "":
		if o.Off >= 0 && o.Off < 256 {
			s = fmt.Sprintf("$%02X", o.Off)
		} else {
			s = fmt.Sprintf("$%04X", o.Off&0xFFFF)
		}
	case o.Off > 0:
		s = fmt.Sprintf("%s+%d", o.Sym, o.Off)
	case o.Off < 0:
		s = fmt.Sprintf("%s%d", o.Sym, o.Off)
	default:
		s = o.Sym
	}
	switch o.Part {
	case Lo:
		return "<" + s
	case Hi:
		return ">" + s
	}
	return s
}

// Pseudo modes: the assembler picks ZP or Abs depending on the operand value.
const (
	Auto  cpu.Mode = 100
	AutoX cpu.Mode = 101
	AutoY cpu.Mode = 102
)

// Kind is the kind of a program item.
type Kind int

const (
	KInstr Kind = iota
	KLabel
	KBytes
	KReserve
	KAlign // padding so that the next Size bytes do not cross a page
)

// Item is one program item.
type Item struct {
	Kind Kind
	Mnem string
	Mode cpu.Mode
	Arg  Operand
	Long bool // relaxed branch: inverted Bxx +3 / JMP target
	Name string
	Data []Operand
	Size int
	Line int    // source line (0 = none)
	File string // source file
	Addr int
}

// Program is a sequence of items starting at address Org.
type Program struct {
	Org   int
	Items []*Item
	Equ   map[string]int
	// Alias: name = another symbol + offset (e.g. a procedure frame)
	Alias map[string]Operand
	// Loops are wait loops timed by counting passes.
	Loops []*TimedLoop
	// CurLine/CurFile are attached to every new item.
	CurLine int
	CurFile string
}

// New creates an empty program.
func New(org int) *Program {
	return &Program{Org: org, Equ: map[string]int{}, Alias: map[string]Operand{}}
}

func (p *Program) add(it *Item) *Item {
	it.Line = p.CurLine
	it.File = p.CurFile
	p.Items = append(p.Items, it)
	return it
}

// I adds an instruction.
func (p *Program) I(mn string, mode cpu.Mode, arg Operand) *Item {
	return p.add(&Item{Kind: KInstr, Mnem: mn, Mode: mode, Arg: arg})
}

// Imp adds an instruction without an operand.
func (p *Program) Imp(mn string) *Item { return p.I(mn, cpu.Imp, Operand{}) }

// Label defines a label.
func (p *Program) Label(name string) { p.add(&Item{Kind: KLabel, Name: name}) }

// Bytes adds data.
func (p *Program) Bytes(data ...Operand) { p.add(&Item{Kind: KBytes, Data: data}) }

// Align keeps the next span bytes within one page, padding with NOPs when needed.
func (p *Program) Align(span int) { p.add(&Item{Kind: KAlign, Data: []Operand{N(span)}}) }

// Reserve reserves space (emits no bytes into the binary; only at the end of the program).
func (p *Program) Reserve(name string, size int) {
	p.add(&Item{Kind: KReserve, Name: name, Size: size})
}

// Last returns the last instruction (or nil if the last item is not an instruction).
func (p *Program) Last() *Item {
	if len(p.Items) == 0 {
		return nil
	}
	it := p.Items[len(p.Items)-1]
	if it.Kind != KInstr {
		return nil
	}
	return it
}

// Result is the assembly result.
type Result struct {
	Org    int
	Code   []byte
	End    int // first address after code and data
	Top    int // first address after reserved variables
	Labels map[string]int
	Items  []*Item
	Loops  []*TimedLoop
}

func (p *Program) value(o Operand, labels map[string]int, strict bool) (int, error) {
	v := o.Off
	if o.Sym != "" {
		if a, ok := p.Alias[o.Sym]; ok {
			x, err := p.value(Operand{Sym: a.Sym, Off: a.Off}, labels, strict)
			if err != nil {
				return 0, err
			}
			v += x
		} else if x, ok := p.Equ[o.Sym]; ok {
			v += x
		} else if x, ok := labels[o.Sym]; ok {
			v += x
		} else {
			if strict {
				return 0, fmt.Errorf("unknown symbol %q", o.Sym)
			}
			return 0x1000, nil
		}
	}
	switch o.Part {
	case Lo:
		v &= 0xFF
	case Hi:
		v = (v >> 8) & 0xFF
	}
	return v, nil
}

func (it *Item) size() int {
	switch it.Kind {
	case KInstr:
		if cpu.IsBranch(it.Mnem) && it.Long {
			return 5
		}
		return it.Mode.Size()
	case KBytes:
		return len(it.Data)
	case KReserve, KAlign:
		return it.Size
	}
	return 0
}

// alignPad returns the padding of an Align item at address addr.
func (it *Item) alignPad(addr int) int {
	span := it.Data[0].Off
	if addr&0xFF+span > 256 {
		return 256 - addr&0xFF
	}
	return 0
}

func zpVariant(m cpu.Mode) (cpu.Mode, cpu.Mode) {
	switch m {
	case Auto:
		return cpu.ZP, cpu.Abs
	case AutoX:
		return cpu.ZPX, cpu.AbsX
	case AutoY:
		return cpu.ZPY, cpu.AbsY
	}
	return m, m
}

// Assemble assembles the program.
func (p *Program) Assemble() (*Result, error) {
	// Choose ZP/Abs using values known from Equ.
	for _, it := range p.Items {
		if it.Kind != KInstr || it.Mode < Auto {
			continue
		}
		zm, am := zpVariant(it.Mode)
		_, hasZ := cpu.Lookup[it.Mnem][zm]
		_, hasA := cpu.Lookup[it.Mnem][am]
		small := false
		if it.Arg.Sym == "" {
			small = it.Arg.Off >= 0 && it.Arg.Off < 256
		} else if v, ok := p.equValue(it.Arg.Sym, 0); ok {
			x := v + it.Arg.Off
			small = x >= 0 && x < 256
		}
		switch {
		case small && hasZ:
			it.Mode = zm
		case hasA:
			it.Mode = am
		case hasZ:
			it.Mode = zm
		default:
			return nil, p.errAt(it, fmt.Errorf("%s does not have the required addressing mode", it.Mnem))
		}
	}
	for _, it := range p.Items {
		if it.Kind == KInstr {
			if _, ok := cpu.Lookup[it.Mnem][it.Mode]; !ok {
				return nil, p.errAt(it, fmt.Errorf("%s does not support this addressing mode", it.Mnem))
			}
		}
	}

	labels := map[string]int{}
	for iter := 0; iter < 100; iter++ {
		addr := p.Org
		for _, it := range p.Items {
			it.Addr = addr
			if it.Kind == KAlign {
				it.Size = it.alignPad(addr)
			}
			if it.Kind == KLabel || it.Kind == KReserve {
				if _, dup := labels[it.Name]; dup && iter == 0 {
					return nil, p.errAt(it, fmt.Errorf("label %q is defined twice", it.Name))
				}
				labels[it.Name] = addr
			}
			addr += it.size()
		}
		changed := false
		for _, it := range p.Items {
			if it.Kind == KInstr && cpu.IsBranch(it.Mnem) && !it.Long {
				t, err := p.value(it.Arg, labels, false)
				if err != nil {
					return nil, err
				}
				d := t - (it.Addr + 2)
				if d < -128 || d > 127 {
					it.Long = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	if err := p.resolveTimed(labels); err != nil {
		return nil, err
	}
	res := &Result{Org: p.Org, Labels: labels, Items: p.Items, Loops: p.Loops}
	var code []byte
	emitted := p.Org
	for _, it := range p.Items {
		switch it.Kind {
		case KInstr:
			v, err := p.value(it.Arg, labels, true)
			if err != nil && it.Mode != cpu.Imp && it.Mode != cpu.Acc {
				return nil, p.errAt(it, err)
			}
			if cpu.IsBranch(it.Mnem) {
				if it.Long {
					inv := cpu.InvertBranch(it.Mnem)
					code = append(code, cpu.Lookup[inv][cpu.Rel], 3, cpu.Lookup["JMP"][cpu.Abs], byte(v), byte(v>>8))
				} else {
					d := v - (it.Addr + 2)
					code = append(code, cpu.Lookup[it.Mnem][cpu.Rel], byte(int8(d)))
				}
				break
			}
			code = append(code, cpu.Lookup[it.Mnem][it.Mode])
			switch it.Mode.Size() {
			case 2:
				if v < -128 || v > 255 {
					return nil, p.errAt(it, fmt.Errorf("operand %s=$%X does not fit in a byte", it.Arg, v))
				}
				code = append(code, byte(v))
			case 3:
				code = append(code, byte(v), byte(v>>8))
			}
		case KBytes:
			for _, d := range it.Data {
				v, err := p.value(d, labels, true)
				if err != nil {
					return nil, p.errAt(it, err)
				}
				code = append(code, byte(v))
			}
		case KAlign:
			for i := 0; i < it.Size; i++ {
				code = append(code, 0xEA)
			}
		case KReserve:
			continue
		}
		emitted = it.Addr + it.size()
	}
	res.Code = code
	res.End = emitted
	res.Top = p.Org
	if n := len(p.Items); n > 0 {
		last := p.Items[n-1]
		res.Top = last.Addr + last.size()
	}
	if res.End != p.Org+len(code) {
		return nil, fmt.Errorf("internal error: reserved space must be at the end of the program")
	}
	return res, nil
}

func (p *Program) errAt(it *Item, err error) error {
	if it.Line > 0 {
		return fmt.Errorf("%s:%d: %v", it.File, it.Line, err)
	}
	return err
}

// Text returns the instruction as assembler text.
func (it *Item) Text() string {
	switch it.Mode {
	case cpu.Imp:
		return it.Mnem
	case cpu.Acc:
		return it.Mnem + " A"
	case cpu.Imm:
		return it.Mnem + " #" + it.Arg.String()
	case cpu.ZP, cpu.Abs, cpu.Rel:
		return it.Mnem + " " + it.Arg.String()
	case cpu.ZPX, cpu.AbsX:
		return it.Mnem + " " + it.Arg.String() + ",X"
	case cpu.ZPY, cpu.AbsY:
		return it.Mnem + " " + it.Arg.String() + ",Y"
	case cpu.Ind:
		return it.Mnem + " (" + it.Arg.String() + ")"
	case cpu.IndX:
		return it.Mnem + " (" + it.Arg.String() + ",X)"
	case cpu.IndY:
		return it.Mnem + " (" + it.Arg.String() + "),Y"
	}
	return it.Mnem
}

// Cycles returns the base cycle count and a description of extra cycles.
func (it *Item) Cycles() (int, string) {
	if it.Kind != KInstr {
		return 0, ""
	}
	op := cpu.Table[cpu.Lookup[it.Mnem][it.Mode]]
	if cpu.IsBranch(it.Mnem) {
		if it.Long {
			return 2, "+3 taken"
		}
		return 2, "+1 taken"
	}
	if op.PagePenalty {
		return op.Cycles, "+1 page"
	}
	return op.Cycles, ""
}

// Listing produces a listing with addresses, bytes, instructions and cycles.
func Listing(res *Result, sources map[string][]string) string {
	var b strings.Builder
	lastKey := ""
	labelOf := map[int][]string{}
	for _, it := range res.Items {
		if it.Kind == KLabel {
			labelOf[it.Addr] = append(labelOf[it.Addr], it.Name)
		}
	}
	shown := map[int]bool{}
	lineSum := 0
	flushSum := func() {
		if lineSum > 0 {
			fmt.Fprintf(&b, "%38s; line total %d cycles (without extras)\n", "", lineSum)
		}
		lineSum = 0
	}
	for _, it := range res.Items {
		if it.Kind == KLabel {
			continue
		}
		key := fmt.Sprintf("%s:%d", it.File, it.Line)
		if it.Line > 0 && key != lastKey {
			flushSum()
			lastKey = key
			src := ""
			if lines := sources[it.File]; it.Line-1 < len(lines) {
				src = strings.TrimRight(lines[it.Line-1], " \t")
			}
			fmt.Fprintf(&b, "\n; %s:%d  %s\n", it.File, it.Line, src)
		}
		if !shown[it.Addr] {
			for _, l := range labelOf[it.Addr] {
				fmt.Fprintf(&b, "%s:\n", l)
			}
			shown[it.Addr] = true
		}
		switch it.Kind {
		case KInstr:
			off := it.Addr - res.Org
			n := it.size()
			var hex []string
			for i := 0; i < n && off+i < len(res.Code); i++ {
				hex = append(hex, fmt.Sprintf("%02X", res.Code[off+i]))
			}
			c, extra := it.Cycles()
			lineSum += c
			txt := it.Text()
			if it.Long {
				txt += "   ; long branch"
			}
			fmt.Fprintf(&b, "%04X  %-9s  %-24s ; %d%s\n", it.Addr, strings.Join(hex, " "), txt, c, suffix(extra))
		case KBytes:
			off := it.Addr - res.Org
			for i := 0; i < len(it.Data); i += 8 {
				var hex []string
				for j := i; j < i+8 && j < len(it.Data); j++ {
					hex = append(hex, fmt.Sprintf("%02X", res.Code[off+j]))
				}
				fmt.Fprintf(&b, "%04X  .byte %s\n", it.Addr+i, strings.Join(hex, " "))
			}
		case KReserve:
			fmt.Fprintf(&b, "%04X  .res %d     ; %s\n", it.Addr, it.Size, it.Name)
		case KAlign:
			if it.Size > 0 {
				fmt.Fprintf(&b, "%04X  .fill %d     ; keeps the next routine within one page\n", it.Addr, it.Size)
			}
		}
	}
	flushSum()
	return b.String()
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

// equValue evaluates a symbol using only Equ and Alias (no code labels).
func (p *Program) equValue(sym string, depth int) (int, bool) {
	if depth > 20 {
		return 0, false
	}
	if v, ok := p.Equ[sym]; ok {
		return v, true
	}
	if a, ok := p.Alias[sym]; ok {
		v, ok := p.equValue(a.Sym, depth+1)
		return v + a.Off, ok
	}
	return 0, false
}
