package cpu

import "fmt"

// Bus is the memory and I/O seen by the CPU.
type Bus interface {
	Read(addr uint16) byte
	Write(addr uint16, v byte)
}

// Status register flags.
const (
	FlagC byte = 1 << 0
	FlagZ byte = 1 << 1
	FlagI byte = 1 << 2
	FlagD byte = 1 << 3
	FlagB byte = 1 << 4
	FlagU byte = 1 << 5
	FlagV byte = 1 << 6
	FlagN byte = 1 << 7
)

// CPU is an NMOS 6502 emulator (documented instructions).
type CPU struct {
	A, X, Y, S, P byte
	PC            uint16
	Cycles        uint64
	Bus           Bus
	// IRQLine: when true and I=0, an IRQ is taken before the next instruction.
	IRQLine func() bool
	// IRQVector: address of the IRQ/BRK handler. The default reads the vector at $FFFE.
	IRQVector func() uint16
}

// New creates a CPU on top of a bus.
func New(b Bus) *CPU {
	return &CPU{Bus: b, S: 0xFF, P: FlagU | FlagI}
}

func (c *CPU) read(a uint16) byte     { return c.Bus.Read(a) }
func (c *CPU) write(a uint16, v byte) { c.Bus.Write(a, v) }
func (c *CPU) read16(a uint16) uint16 {
	return uint16(c.read(a)) | uint16(c.read(a+1))<<8
}

// read16bug emulates the NMOS bug of JMP ($xxFF).
func (c *CPU) read16bug(a uint16) uint16 {
	b := (a & 0xFF00) | uint16(byte(a)+1)
	return uint16(c.read(a)) | uint16(c.read(b))<<8
}

func (c *CPU) push(v byte) { c.write(0x0100|uint16(c.S), v); c.S-- }
func (c *CPU) pull() byte  { c.S++; return c.read(0x0100 | uint16(c.S)) }

func (c *CPU) setZN(v byte) {
	c.P &^= FlagZ | FlagN
	if v == 0 {
		c.P |= FlagZ
	}
	c.P |= v & FlagN
}

func (c *CPU) flag(f byte) bool { return c.P&f != 0 }
func (c *CPU) setFlag(f byte, on bool) {
	if on {
		c.P |= f
	} else {
		c.P &^= f
	}
}

func (c *CPU) irqVec() uint16 {
	if c.IRQVector != nil {
		return c.IRQVector()
	}
	return c.read16(0xFFFE)
}

// Reset loads PC from the RESET vector.
func (c *CPU) Reset() {
	c.PC = c.read16(0xFFFC)
	c.S = 0xFD
	c.P = FlagU | FlagI
}

// operand computes the effective address; returns the address and a page-cross flag.
func (c *CPU) operand(m Mode) (uint16, bool) {
	pc := c.PC
	switch m {
	case Imm:
		c.PC++
		return pc, false
	case ZP:
		c.PC++
		return uint16(c.read(pc)), false
	case ZPX:
		c.PC++
		return uint16(c.read(pc) + c.X), false
	case ZPY:
		c.PC++
		return uint16(c.read(pc) + c.Y), false
	case Abs:
		c.PC += 2
		return c.read16(pc), false
	case AbsX:
		c.PC += 2
		b := c.read16(pc)
		a := b + uint16(c.X)
		return a, a&0xFF00 != b&0xFF00
	case AbsY:
		c.PC += 2
		b := c.read16(pc)
		a := b + uint16(c.Y)
		return a, a&0xFF00 != b&0xFF00
	case Ind:
		c.PC += 2
		return c.read16bug(c.read16(pc)), false
	case IndX:
		c.PC++
		z := c.read(pc) + c.X
		return uint16(c.read(uint16(z))) | uint16(c.read(uint16(z+1)))<<8, false
	case IndY:
		c.PC++
		z := c.read(pc)
		b := uint16(c.read(uint16(z))) | uint16(c.read(uint16(z+1)))<<8
		a := b + uint16(c.Y)
		return a, a&0xFF00 != b&0xFF00
	case Rel:
		c.PC++
		off := int8(c.read(pc))
		return uint16(int(c.PC) + int(off)), false
	}
	return 0, false
}

func (c *CPU) adc(v byte) {
	a := c.A
	carry := byte(0)
	if c.flag(FlagC) {
		carry = 1
	}
	if c.flag(FlagD) {
		lo := int(a&0x0F) + int(v&0x0F) + int(carry)
		if lo > 9 {
			lo += 6
		}
		hi := int(a>>4) + int(v>>4)
		if lo > 0x0F {
			hi++
		}
		bin := a + v + carry
		c.setFlag(FlagZ, bin == 0)
		tmp := byte(hi<<4) | byte(lo&0x0F)
		c.setFlag(FlagN, tmp&0x80 != 0)
		c.setFlag(FlagV, (^(a^v))&(a^tmp)&0x80 != 0)
		if hi > 9 {
			hi += 6
		}
		c.setFlag(FlagC, hi > 0x0F)
		c.A = byte(hi<<4) | byte(lo&0x0F)
		return
	}
	sum := uint16(a) + uint16(v) + uint16(carry)
	r := byte(sum)
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(a^v))&(a^r)&0x80 != 0)
	c.A = r
	c.setZN(r)
}

func (c *CPU) sbc(v byte) {
	a := c.A
	borrow := byte(1)
	if c.flag(FlagC) {
		borrow = 0
	}
	diff := int(a) - int(v) - int(borrow)
	r := byte(diff)
	c.setFlag(FlagV, (a^v)&(a^r)&0x80 != 0)
	c.setFlag(FlagC, diff >= 0)
	c.setZN(r)
	if c.flag(FlagD) {
		lo := int(a&0x0F) - int(v&0x0F) - int(borrow)
		hi := int(a>>4) - int(v>>4)
		if lo&0x10 != 0 {
			lo -= 6
			hi--
		}
		if hi&0x10 != 0 {
			hi -= 6
		}
		c.A = byte(hi<<4) | byte(lo&0x0F)
		return
	}
	c.A = r
}

func (c *CPU) cmp(r, v byte) {
	d := r - v
	c.setFlag(FlagC, r >= v)
	c.setZN(d)
}

// Interrupt performs an interrupt (IRQ or NMI).
func (c *CPU) Interrupt(vector uint16, brk bool) {
	c.push(byte(c.PC >> 8))
	c.push(byte(c.PC))
	p := c.P | FlagU
	if brk {
		p |= FlagB
	} else {
		p &^= FlagB
	}
	c.push(p)
	c.P |= FlagI
	c.PC = vector
	c.Cycles += 7
}

// Step executes one instruction. Returns the cycle count, or an error on an invalid opcode.
func (c *CPU) Step() (int, error) {
	if c.IRQLine != nil && !c.flag(FlagI) && c.IRQLine() {
		c.Interrupt(c.irqVec(), false)
		return 7, nil
	}
	start := c.Cycles
	at := c.PC
	code := c.read(c.PC)
	op := Table[code]
	if !op.Valid {
		return 0, fmt.Errorf("invalid opcode $%02X at $%04X", code, at)
	}
	c.PC++
	cyc := op.Cycles
	addr, crossed := c.operand(op.Mode)
	if crossed && op.PagePenalty {
		cyc++
	}
	switch op.Mnem {
	case "LDA":
		c.A = c.read(addr)
		c.setZN(c.A)
	case "LDX":
		c.X = c.read(addr)
		c.setZN(c.X)
	case "LDY":
		c.Y = c.read(addr)
		c.setZN(c.Y)
	case "STA":
		c.write(addr, c.A)
	case "STX":
		c.write(addr, c.X)
	case "STY":
		c.write(addr, c.Y)
	case "ADC":
		c.adc(c.read(addr))
	case "SBC":
		c.sbc(c.read(addr))
	case "AND":
		c.A &= c.read(addr)
		c.setZN(c.A)
	case "ORA":
		c.A |= c.read(addr)
		c.setZN(c.A)
	case "EOR":
		c.A ^= c.read(addr)
		c.setZN(c.A)
	case "CMP":
		c.cmp(c.A, c.read(addr))
	case "CPX":
		c.cmp(c.X, c.read(addr))
	case "CPY":
		c.cmp(c.Y, c.read(addr))
	case "BIT":
		v := c.read(addr)
		c.setFlag(FlagZ, c.A&v == 0)
		c.setFlag(FlagN, v&0x80 != 0)
		c.setFlag(FlagV, v&0x40 != 0)
	case "ASL", "LSR", "ROL", "ROR":
		var v byte
		if op.Mode == Acc {
			v = c.A
		} else {
			v = c.read(addr)
		}
		oldC := c.flag(FlagC)
		switch op.Mnem {
		case "ASL":
			c.setFlag(FlagC, v&0x80 != 0)
			v <<= 1
		case "LSR":
			c.setFlag(FlagC, v&1 != 0)
			v >>= 1
		case "ROL":
			c.setFlag(FlagC, v&0x80 != 0)
			v <<= 1
			if oldC {
				v |= 1
			}
		case "ROR":
			c.setFlag(FlagC, v&1 != 0)
			v >>= 1
			if oldC {
				v |= 0x80
			}
		}
		c.setZN(v)
		if op.Mode == Acc {
			c.A = v
		} else {
			c.write(addr, v)
		}
	case "INC":
		v := c.read(addr) + 1
		c.write(addr, v)
		c.setZN(v)
	case "DEC":
		v := c.read(addr) - 1
		c.write(addr, v)
		c.setZN(v)
	case "INX":
		c.X++
		c.setZN(c.X)
	case "INY":
		c.Y++
		c.setZN(c.Y)
	case "DEX":
		c.X--
		c.setZN(c.X)
	case "DEY":
		c.Y--
		c.setZN(c.Y)
	case "TAX":
		c.X = c.A
		c.setZN(c.X)
	case "TAY":
		c.Y = c.A
		c.setZN(c.Y)
	case "TXA":
		c.A = c.X
		c.setZN(c.A)
	case "TYA":
		c.A = c.Y
		c.setZN(c.A)
	case "TSX":
		c.X = c.S
		c.setZN(c.X)
	case "TXS":
		c.S = c.X
	case "PHA":
		c.push(c.A)
	case "PHP":
		c.push(c.P | FlagB | FlagU)
	case "PLA":
		c.A = c.pull()
		c.setZN(c.A)
	case "PLP":
		c.P = c.pull()&^FlagB | FlagU
	case "CLC":
		c.P &^= FlagC
	case "SEC":
		c.P |= FlagC
	case "CLI":
		c.P &^= FlagI
	case "SEI":
		c.P |= FlagI
	case "CLV":
		c.P &^= FlagV
	case "CLD":
		c.P &^= FlagD
	case "SED":
		c.P |= FlagD
	case "NOP":
	case "JMP":
		c.PC = addr
	case "JSR":
		ret := c.PC - 1
		c.push(byte(ret >> 8))
		c.push(byte(ret))
		c.PC = addr
	case "RTS":
		lo := c.pull()
		hi := c.pull()
		c.PC = (uint16(hi)<<8 | uint16(lo)) + 1
	case "RTI":
		c.P = c.pull()&^FlagB | FlagU
		lo := c.pull()
		hi := c.pull()
		c.PC = uint16(hi)<<8 | uint16(lo)
	case "BRK":
		c.PC++ // BRK skips the byte after it
		c.Interrupt(c.irqVec(), true)
		c.Cycles -= 7
	default:
		if IsBranch(op.Mnem) {
			var take bool
			switch op.Mnem {
			case "BPL":
				take = !c.flag(FlagN)
			case "BMI":
				take = c.flag(FlagN)
			case "BVC":
				take = !c.flag(FlagV)
			case "BVS":
				take = c.flag(FlagV)
			case "BCC":
				take = !c.flag(FlagC)
			case "BCS":
				take = c.flag(FlagC)
			case "BNE":
				take = !c.flag(FlagZ)
			case "BEQ":
				take = c.flag(FlagZ)
			}
			if take {
				cyc++
				if addr&0xFF00 != c.PC&0xFF00 {
					cyc++
				}
				c.PC = addr
			}
		}
	}
	c.Cycles = start + uint64(cyc)
	return cyc, nil
}
