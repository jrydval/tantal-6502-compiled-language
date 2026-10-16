package emu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrydval/tantal/compiler"
)

// run compiles the source and runs it in the emulator.
func run(t *testing.T, src string, inputs ...string) *Machine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.tantal")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := compiler.CompileFile(path, "pal2")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	m := New(res)
	for _, in := range inputs {
		if err := m.SetInput(in); err != nil {
			t.Fatal(err)
		}
	}
	m.Run(20_000_000)
	if m.Stop != "return to monitor" {
		t.Fatalf("run stopped: %s", m.Stop)
	}
	return m
}

func expect(t *testing.T, m *Machine, want map[string]int) {
	t.Helper()
	for name, w := range want {
		_, v, ok := m.Addr(name)
		if !ok {
			t.Errorf("variable %s does not exist", name)
			continue
		}
		got := m.Byte(name)
		if v.Size() == 2 {
			got = m.Word(name)
		}
		if got != w {
			t.Errorf("%s = %d ($%X), want %d ($%X)", name, got, got, w, w)
		}
	}
}

func compileErr(t *testing.T, src, want string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "err.tantal")
	os.WriteFile(path, []byte(src), 0o644)
	_, err := compiler.CompileFile(path, "pal2")
	if err == nil {
		t.Fatalf("expected an error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func example(t *testing.T, name string, inputs ...string) *Machine {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return run(t, string(src), inputs...)
}

func TestExampleHello(t *testing.T) {
	m := example(t, "hello.tantal")
	expect(t, m, map[string]int{"i": 10, "sum": 55})
	if got := m.TTY(); got != "HELLO\n37" {
		t.Errorf("TTY = %q", got)
	}
}

func TestExampleArithmetic(t *testing.T) {
	m := example(t, "arithmetic.tantal")
	expect(t, m, map[string]int{
		"r_add": 207, "r_sub": 63, "r_mul": 63, "r_div": 28, "r_mod": 4, "r_shl": 56, "r_shr": 50, "r_neg": 249,
		"w_mul": 1400, "w_div": 33, "w_mod": 10, "w_add": 1200, "w_sub": 65535, "w_shl": 16000, "y": 8003, "d0": 255,
		"c_lt": 1, "c_gt": 1, "c_eq": 1, "c_le": 1, "c_ge": 0, "c_ne": 1, "cw_lt": 1, "cw_gt": 1,
	})
}

func TestExampleBits(t *testing.T) {
	m := example(t, "bits.tantal", "PAD=C0")
	expect(t, m, map[string]int{"n_ready": 1, "n_busy": 1, "mode_back": 5, "led_val": 0, "flags": 3})
	if m.Mem[0x1702] != 0x08 {
		t.Errorf("PBD = $%02X", m.Mem[0x1702])
	}
}

func TestExampleProcedures(t *testing.T) {
	m := example(t, "procedures.tantal")
	expect(t, m, map[string]int{"r1": 6, "r2": 33, "r3": 9, "r4": 1006, "r5": 2500})
}

func TestExampleStrings(t *testing.T) {
	m := example(t, "strings.tantal")
	expect(t, m, map[string]int{"n": 6, "total": 45, "peek": 0x56})
	if got := m.TTY(); got != "TANTAL MANTAL\nA5" {
		t.Errorf("TTY = %q", got)
	}
}

func TestExampleAnalyzer(t *testing.T) {
	m := example(t, "analyzer.tantal", "PAD=@0:00,@500:01,@800:03,@1500:00,@1600:80")
	expect(t, m, map[string]int{"changes": 4, "timedout": 1})
	if got := m.TTY(); got != "01 03 00 80 \n" {
		t.Errorf("TTY = %q", got)
	}
	// timeout 2 s after the last change (cycle 1600)
	if c := m.CPU.Cycles; c < 2_000_000 || c > 2_020_000 {
		t.Errorf("stopped after %d cycles, want about 2,001,600", c)
	}
}

func TestExampleInterrupt(t *testing.T) {
	m := example(t, "interrupts.tantal", "PAD=@0:00,@3000:05")
	expect(t, m, map[string]int{"ticks": 10, "n": 10, "got": 0})
}

func TestForLoops(t *testing.T) {
	m := run(t, `
byte i, n1, n2, n3, n4, n5, n6
card j, n7
for i = 0 to 255 do n1 += 1 od          ; 256 passes: n1 wraps to 0
for i = 250 to 255 do n2 += 1 od        ; 6
for i = 5 to 4 do n3 += 1 od            ; 0
for i = 0 until 0 do n4 += 1 od         ; 0
for i = 0 until 256 do n5 += 1 od       ; 256 -> 0
for i = 0 to 100 step 10 do n6 += 1 od  ; 11
for j = 0 to 999 do n7 += 1 od          ; 1000
`)
	expect(t, m, map[string]int{"n1": 0, "n2": 6, "n3": 0, "n4": 0, "n5": 0, "n6": 11, "n7": 1000, "i": 100})
}

func TestForUntilRing(t *testing.T) {
	m := run(t, `
ring byte[256] r
byte i, k, cnt
card total
for k = 0 to 255 do r <- k od
for i = 0 until len(r) do
  cnt += 1
  total += r[i]
od
`)
	expect(t, m, map[string]int{"cnt": 0, "total": 255 * 256 / 2})
}

func TestEmptyRingLoop(t *testing.T) {
	m := run(t, `
ring byte[256] r
byte i, cnt
for i = 0 until len(r) do cnt += 1 od
`)
	expect(t, m, map[string]int{"cnt": 0})
}

func TestRing(t *testing.T) {
	m := run(t, `
ring byte[4] r
byte a, b, c, d, e, n1, n2, f1, f2
r <- 1
r <- 2
r <- 3
f1 = full(r)
r <- 4
r <- 5           ; overwrites 1
f2 = full(r)
a = r[0]         ; 2
b = last(r, 0)   ; 5
c = last(r, 1)   ; 4
n1 = len(r)
d = pop(r)       ; 2
e = pop(r)       ; 3
n2 = len(r)      ; 2
clear(r)
`)
	expect(t, m, map[string]int{"a": 2, "b": 5, "c": 4, "d": 2, "e": 3, "n1": 4, "n2": 2, "f1": 0, "f2": 1})
}

func TestPopEmpty(t *testing.T) {
	m := run(t, `
ring byte[8] r
byte x = 9
x = pop(r)
`)
	expect(t, m, map[string]int{"x": 0})
}

func TestLoops(t *testing.T) {
	m := run(t, `
byte a, b, c, d
do
  a += 1
  if a = 5 then exit fi
od
while b < 7 do b += 1 od
do
  c += 2
until c >= 9 od
do
  do
    d += 1
    if d & 3 = 0 then exit fi
  od
  if d > 10 then exit fi
od
`)
	expect(t, m, map[string]int{"a": 5, "b": 7, "c": 10, "d": 12})
}

func TestWaitEdges(t *testing.T) {
	m := run(t, `
byte r, f, g, h
r = wait rising PAD.7
f = wait falling PAD.7
g = wait rising PAD.2
h = wait change PAD.0
`, "PAD=80,80,00,00,81,85,05,01,01,00,00,04,05,05,04")
	expect(t, m, map[string]int{"r": 0x81, "f": 0x05, "g": 0x04, "h": 0x04})
}

func TestWaitTimeoutBit6(t *testing.T) {
	m := run(t, `
byte a, t1, b, t2
a = wait rising PAD.6 timeout 1 ms
t1 = timedout
b = wait change PAD timeout 5 ms
t2 = timedout
`, "PAD=@0:00,@300:40")
	expect(t, m, map[string]int{"a": 0x40, "t1": 0, "t2": 1})
}

func TestWaitCond(t *testing.T) {
	m := run(t, `
byte x
wait PAD.3 = 1
x = PAD
`, "PAD=00,00,00,08")
	expect(t, m, map[string]int{"x": 8})
}

func TestAsmBlock(t *testing.T) {
	m := run(t, `
byte x = 3, y
const K = 10
asm
  LDA x
  CLC
  ADC #K
  STA y        ; y = 13
loop:
  DEC x
  BNE loop
endasm
`)
	expect(t, m, map[string]int{"x": 0, "y": 13})
}

func TestBigArrayAndCardIndex(t *testing.T) {
	m := run(t, `
byte[300] big
card k
byte a, b
for k = 0 until 300 do big[k] = lo(k) od
a = big[299]      ; 299 & 255 = 43
k = 256
b = big[k]        ; 0
`)
	expect(t, m, map[string]int{"a": 43, "b": 0})
}

func TestNoInit(t *testing.T) {
	m := run(t, `
byte runs = 5 noinit
byte other = 7
runs += other
`)
	expect(t, m, map[string]int{"runs": 12, "other": 7})
}

func TestIrqSafe(t *testing.T) {
	m := run(t, `
card ticks
byte a, b
irqsafe proc dbl(byte v): byte
  return v + v
endproc
interrupt irq proc tick()
  ticks += dbl(1)
  TIM8I = 10
endproc
TIM8I = 10
while ticks < 6 do od
a = dbl(21)
`)
	expect(t, m, map[string]int{"a": 42})
}

func TestBoolValues(t *testing.T) {
	m := run(t, `
byte a = 5, b, c, d
b = a > 3
c = not (a = 5)
d = a > 3 and a < 10 or a = 0
`)
	expect(t, m, map[string]int{"b": 1, "c": 0, "d": 1})
}

func TestLongBranch(t *testing.T) {
	var b strings.Builder
	b.WriteString("byte x, y\nif x = 0 then\n")
	for i := 0; i < 60; i++ {
		b.WriteString("  y += 2\n")
	}
	b.WriteString("fi\n")
	m := run(t, b.String())
	expect(t, m, map[string]int{"y": 120})
}

func TestShadowInit(t *testing.T) {
	m := run(t, `
byte OUT @ $1702 shadow = $81
bit LAMP = OUT.1
LAMP = 1
`)
	if m.Mem[0x1702] != 0x83 {
		t.Errorf("OUT = $%02X, want $83", m.Mem[0x1702])
	}
}

func TestDivide(t *testing.T) {
	m := run(t, `
card a = 60000, b = 40000, q, r
byte x = 200, z
q = a / b
r = a mod b
z = x / 3
card big = 65535
card q2
q2 = big / 1
`)
	expect(t, m, map[string]int{"q": 1, "r": 20000, "z": 66, "q2": 65535})
}

func TestErrors(t *testing.T) {
	compileErr(t, "proc a()\n b()\nendproc\nproc b()\n a()\nendproc\na()\n", "recursion")
	compileErr(t, "TIMSTAT = 1\n", "read-only")
	compileErr(t, "byte x\nx = TIM1\n", "write-only")
	compileErr(t, "byte x\nx = y\n", "unknown name")
	compileErr(t, "proc f()\nendproc\ninterrupt irq proc h()\n f()\nendproc\nf()\n", "irqsafe")
	compileErr(t, "byte[4] a\na[4] = 1\n", "outside array")
	compileErr(t, "ring byte[100] r\n", "power of 2")
	compileErr(t, ".end $2005\nbyte x\nx = 1\nx = 2\nx = 3\n", "exceeds")
	compileErr(t, "exit\n", "outside a loop")
	compileErr(t, "do\n atomic\n  exit\n endatomic\nod\n", "atomic")
	compileErr(t, "const byte[] T = 1, 2\nT[0] = 3\n", "read-only")
	compileErr(t, "byte x\nif x then\n", "end of file")
}

func TestZPExclude(t *testing.T) {
	m := run(t, `
.zp exclude $00-$EE
byte a = 1, b
b = a
`)
	a, v, _ := m.Addr("a")
	if v.InZP || a < 0x2000 {
		t.Errorf("a is at $%04X, want after the code", a)
	}
}

func compileWarn(t *testing.T, src string) []string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "w.tantal")
	os.WriteFile(path, []byte(src), 0o644)
	res, err := compiler.CompileFile(path, "pal2")
	if err != nil {
		t.Fatal(err)
	}
	return res.Warnings
}

func hasWarn(ws []string, s string) bool {
	for _, w := range ws {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

func TestSharedWarnings(t *testing.T) {
	ws := compileWarn(t, `
ring byte[8] r
byte x
interrupt irq proc h()
  r <- PAD
endproc
x = pop(r)
`)
	if !hasWarn(ws, "atomic") {
		t.Errorf("expected a warning about atomic, got %v", ws)
	}
	ws = compileWarn(t, `
ring byte[8] r
byte x
interrupt irq proc h()
  r <- PAD
endproc
atomic
  x = pop(r)
endatomic
`)
	if hasWarn(ws, "atomic") {
		t.Errorf("unexpected warning: %v", ws)
	}
	ws = compileWarn(t, "byte a\nif a <-1 then a = 2 fi\n")
	if !hasWarn(ws, "add a space") {
		t.Errorf("expected a warning about <-: %v", ws)
	}
}

func TestRerunNoInit(t *testing.T) {
	m := run(t, `
byte runs = 5 noinit
byte other
other += 1
runs += other
`)
	m.CPU.PC = uint16(m.Res.Org)
	m.Stop = ""
	m.Run(m.CPU.Cycles + 100000)
	// other is cleared on the second run, runs is not
	expect(t, m, map[string]int{"runs": 7, "other": 1})
}

func TestIncludeAndFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lib.tantal"), []byte("proc twice(byte v): byte\n  return v << 1\nendproc\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "font.bin"), []byte{9, 8, 7}, 0o644)
	src := ".include \"lib.tantal\"\nconst byte[] F = file \"font.bin\"\nbyte a, b\na = twice(F[1])\nb = sizeof(F)\n"
	path := filepath.Join(dir, "main.tantal")
	os.WriteFile(path, []byte(src), 0o644)
	res, err := compiler.CompileFile(path, "pal2")
	if err != nil {
		t.Fatal(err)
	}
	m := New(res)
	m.Run(100000)
	expect(t, m, map[string]int{"a": 16, "b": 3})
}

func TestCardStepAndNegation(t *testing.T) {
	m := run(t, `
card i, n, neg
byte s
for i = 1000 to 2000 step 250 do n += 1 od   ; 1000,1250,1500,1750,2000
neg = -i
s = 3
s -= 5
`)
	expect(t, m, map[string]int{"n": 5, "i": 2000, "neg": 65536 - 2000, "s": 254})
}

func TestFramesInRAM(t *testing.T) {
	m := run(t, `
.zp exclude $00-$EC        ; 2 bytes remain: __mp
byte[4] data = 1, 2, 3, 4
byte r
card w
proc sum(byte pointer p, byte n): byte
  byte k, acc
  for k = 0 until n do acc += p[k] od
  return acc
endproc
proc wide(card a, card b, byte c): card
  return a + b + c
endproc
r = sum(@data, 4)
w = wide(1000, 2000, 5)
`)
	expect(t, m, map[string]int{"r": 10, "w": 3005})
	_, v, _ := m.Addr("r")
	if v.InZP {
		t.Error("r must not be in the zero page")
	}
}

func TestMisc(t *testing.T) {
	m := run(t, `
byte[8] buf
byte a, b, c, d, e, f, g2, h, sh
card addr, ca = 500, cb = 700
byte r1, r2, r3, r4, r5, r6
bits MODE = PBD.2..4
proc fill(byte pointer p, byte n)
  byte k
  for k = 0 until n do p[k + 1] = k * 2 od
endproc
fill(@buf, 5)               ; buf = 0,0,2,4,6,8
a = buf[3]                  ; 4
addr = $0310
mem[addr + 1] = 77
b = mem[$0311]              ; 77
c = 3
if c = 1 then d = 10
elif c = 3 then d = 30
else d = 99 fi              ; 30
e = 5
MODE = e                    ; PBD = %0001_0100
f = MODE                    ; 5
sh = 1 << c                 ; 8
g2 = $80 >> c               ; 16
if ca + 1 < cb then r1 = 1 fi
if ca * 2 > cb then r2 = 1 fi
if ca <= cb - 200 then r3 = 1 fi
if ca >= cb then r4 = 1 fi
if ca = 500 then r5 = 1 fi
if ca <> cb - 200 then r6 = 1 fi
h = lo(@fill) - lo(@fill) + hi(card(513))   ; 2
`)
	expect(t, m, map[string]int{"a": 4, "b": 77, "d": 30, "f": 5, "sh": 8, "g2": 16,
		"r1": 1, "r2": 1, "r3": 1, "r4": 0, "r5": 1, "r6": 0, "h": 2})
	if m.Mem[0x1702]&0x1C != 0x14 {
		t.Errorf("PBD = $%02X", m.Mem[0x1702])
	}
}
