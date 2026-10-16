// Package cpu contains the NMOS 6502 instruction table and an emulator.
package cpu

// Mode is an instruction addressing mode.
type Mode int

const (
	Imp  Mode = iota // implied
	Acc              // accumulator (ASL A)
	Imm              // #$nn
	ZP               // $nn
	ZPX              // $nn,X
	ZPY              // $nn,Y
	Abs              // $nnnn
	AbsX             // $nnnn,X
	AbsY             // $nnnn,Y
	Ind              // ($nnnn)
	IndX             // ($nn,X)
	IndY             // ($nn),Y
	Rel              // relative branch
)

// Size returns the instruction length in bytes for the mode.
func (m Mode) Size() int {
	switch m {
	case Imp, Acc:
		return 1
	case Imm, ZP, ZPX, ZPY, IndX, IndY, Rel:
		return 2
	default:
		return 3
	}
}

// Op describes one opcode.
type Op struct {
	Mnem   string
	Mode   Mode
	Cycles int
	// PagePenalty: +1 cycle on page crossing (indexed read modes).
	PagePenalty bool
	Valid       bool
}

// Table holds the 256 opcodes; invalid ones have Valid=false.
var Table [256]Op

// Lookup maps (mnemonic, mode) to an opcode.
var Lookup = map[string]map[Mode]byte{}

func def(code byte, mn string, mode Mode, cyc int, pen bool) {
	Table[code] = Op{Mnem: mn, Mode: mode, Cycles: cyc, PagePenalty: pen, Valid: true}
	if Lookup[mn] == nil {
		Lookup[mn] = map[Mode]byte{}
	}
	Lookup[mn][mode] = code
}

func init() {
	// ALU group: ORA AND EOR ADC STA LDA CMP SBC
	alu := []struct {
		mn   string
		base byte
	}{{"ORA", 0x00}, {"AND", 0x20}, {"EOR", 0x40}, {"ADC", 0x60}, {"STA", 0x80}, {"LDA", 0xA0}, {"CMP", 0xC0}, {"SBC", 0xE0}}
	for _, a := range alu {
		st := a.mn == "STA"
		def(a.base+0x01, a.mn, IndX, 6, false)
		def(a.base+0x05, a.mn, ZP, 3, false)
		if !st {
			def(a.base+0x09, a.mn, Imm, 2, false)
		}
		def(a.base+0x0D, a.mn, Abs, 4, false)
		if st {
			def(a.base+0x11, a.mn, IndY, 6, false)
			def(a.base+0x19, a.mn, AbsY, 5, false)
			def(a.base+0x1D, a.mn, AbsX, 5, false)
		} else {
			def(a.base+0x11, a.mn, IndY, 5, true)
			def(a.base+0x19, a.mn, AbsY, 4, true)
			def(a.base+0x1D, a.mn, AbsX, 4, true)
		}
		def(a.base+0x15, a.mn, ZPX, 4, false)
	}
	// Shifts and INC/DEC
	shifts := []struct {
		mn   string
		base byte
	}{{"ASL", 0x00}, {"ROL", 0x20}, {"LSR", 0x40}, {"ROR", 0x60}}
	for _, s := range shifts {
		def(s.base+0x0A, s.mn, Acc, 2, false)
		def(s.base+0x06, s.mn, ZP, 5, false)
		def(s.base+0x16, s.mn, ZPX, 6, false)
		def(s.base+0x0E, s.mn, Abs, 6, false)
		def(s.base+0x1E, s.mn, AbsX, 7, false)
	}
	def(0xC6, "DEC", ZP, 5, false)
	def(0xD6, "DEC", ZPX, 6, false)
	def(0xCE, "DEC", Abs, 6, false)
	def(0xDE, "DEC", AbsX, 7, false)
	def(0xE6, "INC", ZP, 5, false)
	def(0xF6, "INC", ZPX, 6, false)
	def(0xEE, "INC", Abs, 6, false)
	def(0xFE, "INC", AbsX, 7, false)

	// Branches
	for mn, c := range map[string]byte{"BPL": 0x10, "BMI": 0x30, "BVC": 0x50, "BVS": 0x70, "BCC": 0x90, "BCS": 0xB0, "BNE": 0xD0, "BEQ": 0xF0} {
		def(c, mn, Rel, 2, false)
	}
	def(0x24, "BIT", ZP, 3, false)
	def(0x2C, "BIT", Abs, 4, false)

	def(0x00, "BRK", Imp, 7, false)
	def(0x20, "JSR", Abs, 6, false)
	def(0x40, "RTI", Imp, 6, false)
	def(0x60, "RTS", Imp, 6, false)
	def(0x4C, "JMP", Abs, 3, false)
	def(0x6C, "JMP", Ind, 5, false)

	def(0xE0, "CPX", Imm, 2, false)
	def(0xE4, "CPX", ZP, 3, false)
	def(0xEC, "CPX", Abs, 4, false)
	def(0xC0, "CPY", Imm, 2, false)
	def(0xC4, "CPY", ZP, 3, false)
	def(0xCC, "CPY", Abs, 4, false)

	def(0xA2, "LDX", Imm, 2, false)
	def(0xA6, "LDX", ZP, 3, false)
	def(0xB6, "LDX", ZPY, 4, false)
	def(0xAE, "LDX", Abs, 4, false)
	def(0xBE, "LDX", AbsY, 4, true)
	def(0xA0, "LDY", Imm, 2, false)
	def(0xA4, "LDY", ZP, 3, false)
	def(0xB4, "LDY", ZPX, 4, false)
	def(0xAC, "LDY", Abs, 4, false)
	def(0xBC, "LDY", AbsX, 4, true)

	def(0x86, "STX", ZP, 3, false)
	def(0x96, "STX", ZPY, 4, false)
	def(0x8E, "STX", Abs, 4, false)
	def(0x84, "STY", ZP, 3, false)
	def(0x94, "STY", ZPX, 4, false)
	def(0x8C, "STY", Abs, 4, false)

	imp := map[string]struct {
		c   byte
		cyc int
	}{
		"CLC": {0x18, 2}, "SEC": {0x38, 2}, "CLI": {0x58, 2}, "SEI": {0x78, 2},
		"CLV": {0xB8, 2}, "CLD": {0xD8, 2}, "SED": {0xF8, 2},
		"DEX": {0xCA, 2}, "DEY": {0x88, 2}, "INX": {0xE8, 2}, "INY": {0xC8, 2},
		"TAX": {0xAA, 2}, "TXA": {0x8A, 2}, "TAY": {0xA8, 2}, "TYA": {0x98, 2},
		"TSX": {0xBA, 2}, "TXS": {0x9A, 2}, "NOP": {0xEA, 2},
		"PHA": {0x48, 3}, "PHP": {0x08, 3}, "PLA": {0x68, 4}, "PLP": {0x28, 4},
	}
	for mn, v := range imp {
		def(v.c, mn, Imp, v.cyc, false)
	}
}

// IsBranch reports whether the mnemonic is a conditional branch.
func IsBranch(mn string) bool {
	switch mn {
	case "BPL", "BMI", "BVC", "BVS", "BCC", "BCS", "BNE", "BEQ":
		return true
	}
	return false
}

// InvertBranch returns the inverted branch condition.
func InvertBranch(mn string) string {
	return map[string]string{
		"BPL": "BMI", "BMI": "BPL", "BVC": "BVS", "BVS": "BVC",
		"BCC": "BCS", "BCS": "BCC", "BNE": "BEQ", "BEQ": "BNE",
	}[mn]
}
