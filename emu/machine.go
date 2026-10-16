// Package emu spouští přeložený program v emulátoru 6502 s modelem PAL-2 (KIM-1).
package emu

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"tantal/compiler"
	"tantal/cpu"
)

type timedVal struct {
	at  uint64
	val byte
}

// Input je posloupnost hodnot pro čtení adresy nebo návratové hodnoty rutiny ROM.
type Input struct {
	seq   []byte
	idx   int
	timed []timedVal
}

func (in *Input) next(cyc uint64) byte {
	if len(in.timed) > 0 {
		v := in.timed[0].val
		for _, t := range in.timed {
			if cyc >= t.at {
				v = t.val
			}
		}
		return v
	}
	if len(in.seq) == 0 {
		return 0
	}
	v := in.seq[in.idx]
	if in.idx < len(in.seq)-1 {
		in.idx++
	}
	return v
}

type timer struct {
	set   bool
	start uint64
	count int
	div   int
	irq   bool
}

// Machine je emulovaný počítač.
type Machine struct {
	CPU      *cpu.CPU
	Mem      [65536]byte
	Res      *compiler.Result
	inputs   map[uint16]*Input
	rom      map[string]*Input
	Writes   []string
	Calls    []string
	Stop     string
	tm       timer
	Trace    bool
	TraceOut []string
	// čtení vstupních registrů, pro které nebyl zadán --input
	unfed map[uint16]int
}

// New připraví stroj s nahraným programem.
func New(res *compiler.Result) *Machine {
	m := &Machine{Res: res, inputs: map[uint16]*Input{}, rom: map[string]*Input{}, unfed: map[uint16]int{}}
	copy(m.Mem[res.Org:], res.Bin)
	c := cpu.New(m)
	c.PC = uint16(res.Org)
	c.S = 0xFF
	c.P = cpu.FlagU | cpu.FlagI
	c.IRQLine = func() bool { return m.tm.irq && m.timerFlag() }
	c.IRQVector = func() uint16 {
		v := res.Target.VectorIRQ
		if v < 0 {
			return uint16(m.Mem[0xFFFE]) | uint16(m.Mem[0xFFFF])<<8
		}
		return uint16(m.Mem[v]) | uint16(m.Mem[v+1])<<8
	}
	m.CPU = c
	return m
}

// SetInput nastaví vstup: JMÉNO=hodnoty (hexa), nebo časované JMÉNO=@cyklus:hodnota,...
func (m *Machine) SetInput(spec string) error {
	kv := strings.SplitN(spec, "=", 2)
	if len(kv) != 2 {
		return fmt.Errorf("vstup %q: očekáváno JMÉNO=hodnoty", spec)
	}
	name := kv[0]
	in := &Input{}
	for _, s := range strings.Split(kv[1], ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "@") {
			p := strings.SplitN(s[1:], ":", 2)
			if len(p) != 2 {
				return fmt.Errorf("vstup %q: časovaná hodnota je @cyklus:hodnota", spec)
			}
			at, err := strconv.ParseUint(p[0], 10, 64)
			if err != nil {
				return fmt.Errorf("vstup %q: chybný cyklus %q", spec, p[0])
			}
			v, err := parseByte(p[1])
			if err != nil {
				return err
			}
			in.timed = append(in.timed, timedVal{at, v})
			continue
		}
		v, err := parseByte(s)
		if err != nil {
			return err
		}
		in.seq = append(in.seq, v)
	}
	sort.SliceStable(in.timed, func(i, j int) bool { return in.timed[i].at < in.timed[j].at })
	for _, v := range m.Res.Vars {
		if v.Fixed && v.Name == name {
			m.inputs[uint16(v.Addr)] = in
			return nil
		}
	}
	for _, p := range m.Res.Externs {
		if p.Name == name {
			m.rom[name] = in
			return nil
		}
	}
	if a, err := strconv.ParseUint(strings.TrimPrefix(name, "$"), 16, 16); err == nil && strings.HasPrefix(name, "$") {
		m.inputs[uint16(a)] = in
		return nil
	}
	return fmt.Errorf("vstup %q: neznámý registr nebo rutina %q", spec, name)
}

func parseByte(s string) (byte, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "$")
	v, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, fmt.Errorf("chybná hodnota %q (očekáván bajt hexa)", s)
	}
	return byte(v), nil
}

func (m *Machine) timerElapsed() (int, bool) {
	t := &m.tm
	if !t.set {
		return 0xFF, false
	}
	el := int(m.CPU.Cycles - t.start)
	end := (t.count + 1) * t.div
	if el < end {
		return t.count - el/t.div, false
	}
	return 0xFF - ((el - end) & 0xFF), true
}

func (m *Machine) timerFlag() bool {
	_, f := m.timerElapsed()
	return f
}

func (m *Machine) isTimer(a uint16) bool {
	t := m.Res.Target.TimeoutTimer
	if t < 0 || int(a) < t {
		return false
	}
	off := int(a) - t
	return off <= 3 || (off >= 8 && off <= 11)
}

// Read implementuje sběrnici.
func (m *Machine) Read(a uint16) byte {
	if in, ok := m.inputs[a]; ok {
		return in.next(m.CPU.Cycles)
	}
	if m.isInputPort(a) {
		m.unfed[a]++
	}
	if m.isTimer(a) {
		base := uint16(m.Res.Target.TimeoutTimer)
		off := (a - base) & 0x0B // $1704..$1707, $170C..$170F
		switch off & 3 {
		case 2: // stav čítače, $1706 vypne / $170E zapne přerušení
			v, _ := m.timerElapsed()
			m.tm.irq = a-base >= 8
			return byte(v)
		case 3:
			if m.timerFlag() {
				return 0x80
			}
			return 0
		}
	}
	return m.Mem[a]
}

// Write implementuje sběrnici.
func (m *Machine) Write(a uint16, v byte) {
	t := m.Res.Target
	if t.ROM.Lo >= 0 && int(a) >= t.ROM.Lo && int(a) <= t.ROM.Hi {
		return
	}
	if m.isTimer(a) {
		base := uint16(t.TimeoutTimer)
		off := a - base
		m.tm = timer{set: true, start: m.CPU.Cycles, count: int(v), div: []int{1, 8, 64, 1024}[off&3], irq: off >= 8}
	}
	if t.IO.Lo >= 0 && int(a) >= t.IO.Lo && int(a) <= t.IO.Hi && len(m.Writes) < 1000 {
		m.Writes = append(m.Writes, fmt.Sprintf("%8d  $%04X %-8s <- $%02X", m.CPU.Cycles, a, m.regName(a), v))
	}
	m.Mem[a] = v
}

// isInputPort: datové registry portů RIOT (ne směrové registry ani časovač).
func (m *Machine) isInputPort(a uint16) bool {
	t := m.Res.Target
	if t.IO.Lo < 0 || int(a) < t.IO.Lo || int(a) > t.IO.Hi || m.isTimer(a) {
		return false
	}
	off := int(a) - t.IO.Lo
	return off == 0 || off == 2 || off == 0x40 || off == 0x42
}

func (m *Machine) regName(a uint16) string {
	for _, v := range m.Res.Vars {
		if v.Fixed && uint16(v.Addr) == a && !v.ReadOnly {
			return v.Name
		}
	}
	return ""
}

// Run spouští program, dokud se nevrátí do monitoru, nenarazí na chybu nebo nepřekročí limit cyklů.
func (m *Machine) Run(maxCycles uint64) {
	t := m.Res.Target
	c := m.CPU
	for {
		if c.Cycles >= maxCycles {
			m.Stop = fmt.Sprintf("limit %d cyklů", maxCycles)
			return
		}
		pc := c.PC
		if t.Exit >= 0 && int(pc) == t.Exit {
			m.Stop = "návrat do monitoru"
			return
		}
		op := m.Mem[pc]
		target := uint16(m.Mem[pc+1]) | uint16(m.Mem[pc+2])<<8
		inROM := func(a uint16) bool { return t.ROM.Lo >= 0 && int(a) >= t.ROM.Lo && int(a) <= t.ROM.Hi }
		if op == 0x20 && inROM(target) { // JSR do ROM: zaznamenat a vrátit se
			name := fmt.Sprintf("$%04X", target)
			var pr *compiler.Proc
			if p, ok := m.Res.Externs[int(target)]; ok {
				name = p.Name
				pr = p
			}
			if pr != nil && pr.Decl.Ret != "" {
				if in, ok := m.rom[name]; ok {
					c.A = in.next(c.Cycles)
				} else {
					c.A = 0
				}
			}
			if len(m.Calls) < 1000 {
				m.Calls = append(m.Calls, fmt.Sprintf("%8d  JSR %-7s A=$%02X X=$%02X Y=$%02X%s", c.Cycles, name, c.A, c.X, c.Y, m.callNote(name)))
			}
			c.PC += 3
			c.Cycles += 12
			continue
		}
		if inROM(pc) {
			m.Stop = fmt.Sprintf("běh v ROM na $%04X (emulátor ROM neobsahuje)", pc)
			return
		}
		if op == 0x00 {
			m.Stop = fmt.Sprintf("instrukce BRK na $%04X", pc)
			return
		}
		if m.Trace && len(m.TraceOut) < 100000 {
			m.TraceOut = append(m.TraceOut, fmt.Sprintf("%8d %04X %-12s A=%02X X=%02X Y=%02X S=%02X P=%02X", c.Cycles, pc, disasm(m, pc), c.A, c.X, c.Y, c.S, c.P))
		}
		if _, err := c.Step(); err != nil {
			m.Stop = err.Error()
			return
		}
		if c.PC == pc && op == 0x4C {
			m.Stop = fmt.Sprintf("nekonečná smyčka na $%04X", pc)
			return
		}
	}
}

func (m *Machine) callNote(name string) string {
	switch name {
	case "OUTCH":
		ch := m.CPU.A
		if ch >= 32 && ch < 127 {
			return fmt.Sprintf("  '%c'", ch)
		}
	case "SCANDS":
		return fmt.Sprintf("  displej %02X %02X %02X", m.Mem[0xFB], m.Mem[0xFA], m.Mem[0xF9])
	}
	return ""
}

func disasm(m *Machine, pc uint16) string {
	op := cpu.Table[m.Mem[pc]]
	if !op.Valid {
		return "???"
	}
	b1 := m.Mem[pc+1]
	w := uint16(b1) | uint16(m.Mem[pc+2])<<8
	switch op.Mode {
	case cpu.Imp:
		return op.Mnem
	case cpu.Acc:
		return op.Mnem + " A"
	case cpu.Imm:
		return fmt.Sprintf("%s #$%02X", op.Mnem, b1)
	case cpu.ZP:
		return fmt.Sprintf("%s $%02X", op.Mnem, b1)
	case cpu.ZPX:
		return fmt.Sprintf("%s $%02X,X", op.Mnem, b1)
	case cpu.ZPY:
		return fmt.Sprintf("%s $%02X,Y", op.Mnem, b1)
	case cpu.Abs:
		return fmt.Sprintf("%s $%04X", op.Mnem, w)
	case cpu.AbsX:
		return fmt.Sprintf("%s $%04X,X", op.Mnem, w)
	case cpu.AbsY:
		return fmt.Sprintf("%s $%04X,Y", op.Mnem, w)
	case cpu.Ind:
		return fmt.Sprintf("%s ($%04X)", op.Mnem, w)
	case cpu.IndX:
		return fmt.Sprintf("%s ($%02X,X)", op.Mnem, b1)
	case cpu.IndY:
		return fmt.Sprintf("%s ($%02X),Y", op.Mnem, b1)
	case cpu.Rel:
		return fmt.Sprintf("%s $%04X", op.Mnem, uint16(int(pc)+2+int(int8(b1))))
	}
	return op.Mnem
}

// Addr vrací adresu proměnné podle jména.
func (m *Machine) Addr(name string) (int, *compiler.Var, bool) {
	for _, v := range m.Res.Vars {
		if v.Name == name {
			if v.Fixed {
				return v.Addr, v, true
			}
			return v.Final, v, true
		}
	}
	return 0, nil, false
}

// Byte vrací hodnotu bajtové proměnné (pro testy).
func (m *Machine) Byte(name string) int {
	a, _, ok := m.Addr(name)
	if !ok {
		return -1
	}
	return int(m.Mem[a])
}

// Word vrací 16bitovou hodnotu proměnné.
func (m *Machine) Word(name string) int {
	a, _, ok := m.Addr(name)
	if !ok {
		return -1
	}
	return int(m.Mem[a]) | int(m.Mem[a+1])<<8
}

// Report vypíše výsledek běhu.
func (m *Machine) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Konec: %s po %d cyklech (%.3f ms při %d Hz)\n", m.Stop, m.CPU.Cycles,
		float64(m.CPU.Cycles)*1000/float64(m.Res.Target.Clock), m.Res.Target.Clock)
	fmt.Fprintf(&b, "\nProměnné:\n")
	for _, v := range m.Res.Vars {
		if v.Fixed || (v.Hidden && v.Name != "timedout") || strings.HasPrefix(v.Name, "__") {
			continue
		}
		a := v.Final
		switch {
		case v.Ring:
			ha, _, _ := m.Addr(v.Name + "'head")
			ca, cv, _ := m.Addr(v.Name + "'count")
			n := int(m.Mem[ca])
			if cv != nil && cv.Elem == compiler.TCard {
				n |= int(m.Mem[ca+1]) << 8
			}
			head := int(m.Mem[ha])
			var vals []string
			for i := 0; i < n && i < 32; i++ {
				idx := (head - n + i) & (v.Len - 1)
				vals = append(vals, fmt.Sprintf("%02X", m.Mem[a+idx]))
			}
			more := ""
			if n > 32 {
				more = " …"
			}
			fmt.Fprintf(&b, "  %-12s ring %d/%d: %s%s\n", v.Name, n, v.Len, strings.Join(vals, " "), more)
		case v.Array || v.String:
			n := v.Size()
			if n > 16 {
				n = 16
			}
			var vals []string
			for i := 0; i < n; i++ {
				vals = append(vals, fmt.Sprintf("%02X", m.Mem[a+i]))
			}
			more := ""
			if v.Size() > 16 {
				more = " …"
			}
			fmt.Fprintf(&b, "  %-12s [%d] %s%s\n", v.Name, v.Size(), strings.Join(vals, " "), more)
		case v.Size() == 2:
			w := int(m.Mem[a]) | int(m.Mem[a+1])<<8
			fmt.Fprintf(&b, "  %-12s $%04X (%d)\n", v.Name, w, w)
		default:
			fmt.Fprintf(&b, "  %-12s $%02X (%d)\n", v.Name, m.Mem[a], m.Mem[a])
		}
	}
	if len(m.unfed) > 0 {
		var addrs []int
		for a := range m.unfed {
			addrs = append(addrs, int(a))
		}
		sort.Ints(addrs)
		fmt.Fprintf(&b, "\nUpozornění: čtení portu bez zadaného vstupu (vrací obsah paměti, obvykle $00):\n")
		for _, a := range addrs {
			name := m.portName(uint16(a))
			fmt.Fprintf(&b, "  $%04X %-8s %d×   zadejte např. --input %s=00,01,03\n", a, name, m.unfed[uint16(a)], name)
		}
	}
	if len(m.Writes) > 0 {
		fmt.Fprintf(&b, "\nZápisy na I/O (cyklus, adresa, hodnota):\n")
		for i, w := range m.Writes {
			if i >= 50 {
				fmt.Fprintf(&b, "  … a dalších %d\n", len(m.Writes)-50)
				break
			}
			fmt.Fprintf(&b, "  %s\n", w)
		}
	}
	if len(m.Calls) > 0 {
		fmt.Fprintf(&b, "\nVolání ROM:\n")
		for i, s := range m.Calls {
			if i >= 50 {
				fmt.Fprintf(&b, "  … a dalších %d\n", len(m.Calls)-50)
				break
			}
			fmt.Fprintf(&b, "  %s\n", s)
		}
		if tty := m.TTY(); tty != "" {
			fmt.Fprintf(&b, "\nVýstup na TTY:\n%s\n", tty)
		}
	}
	return b.String()
}

// TTY vrací text vypsaný přes OUTCH, PRTBYT, OUTSP a CRLF.
func (m *Machine) TTY() string {
	var b strings.Builder
	for _, c := range m.Calls {
		f := strings.Fields(c)
		if len(f) < 4 {
			continue
		}
		a, _ := strconv.ParseUint(strings.TrimPrefix(f[3], "A=$"), 16, 8)
		switch f[2] {
		case "OUTCH":
			b.WriteByte(byte(a))
		case "PRTBYT":
			fmt.Fprintf(&b, "%02X", a)
		case "OUTSP":
			b.WriteByte(' ')
		case "CRLF":
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *Machine) portName(a uint16) string {
	for _, v := range m.Res.Vars {
		if v.Fixed && uint16(v.Addr) == a {
			return v.Name
		}
	}
	return fmt.Sprintf("$%04X", a)
}
