# Tantal

Tantal is a small compiled language for the MOS 6502, in the spirit of Action! for the Atari. It is built for writing short programs quickly, with constructs that map closely to 6502 instructions and direct access to memory-mapped I/O.

This repository holds the first version (0.1) of the compiler. It is written in Go with no dependencies and builds into a single binary for macOS, Linux and Windows. It has its own assembler and a 6502 emulator with a model of the PAL-2 computer (a modernized KIM-1), so programs can be tried on a PC before they go to the hardware.

## The language at a glance

```
; Count changes on port A, give up after 2 s without a change
ring byte[256] log           ; ring buffer of the last 256 samples
byte cur
card changes
bit  LED = PBD.0             ; a named bit of an I/O register

PBDD = $FF                   ; port B as output
cur = PAD
do
  cur = wait change PAD from cur timeout 2 s then exit fi
  log <- cur
  changes += 1
  LED ^= 1
od
```

Design points:

- **No stack for parameters or locals.** The hardware stack only holds return addresses. Parameters and locals live at fixed addresses, and procedures that can never be active at the same time share memory (an overlay computed from the call graph). The first three parameter bytes are passed in A, X and Y. Recursion is a compile error.
- **No boilerplate.** Top-level code is the program; there is no `main` and no module header.
- **I/O as variables.** Registers get names with `@`, single bits and bit fields get names with `bit` and `bits`, and qualifiers `readonly`, `writeonly` and `shadow` keep read-modify-write on hardware registers safe.
- **Predictable code.** Each construct has a known translation. The listing shows the generated instructions and the cycle count for every source line.
- **Machine described by a file.** Memory map, I/O registers and monitor routines come from a target file; `pal2` is built in.

## Installing

With Go 1.22 or newer:

```
go install github.com/jrydval/tantal-6502-compiled-language/cmd/tantal@latest
```

Or build from a clone:

```
git clone https://github.com/jrydval/tantal-6502-compiled-language.git
cd tantal-6502-compiled-language
go build -o tantal ./cmd/tantal
go test ./...
```

Cross-compiling, e.g. for Windows: `GOOS=windows GOARCH=amd64 go build -o tantal.exe ./cmd/tantal`.

## Usage

```
tantal build program.tantal
tantal run program.tantal --input PAD=00,01,03 --max 5000000
tantal run program.tantal --input PAD=@0:00,@500:01,@800:00
```

`build` writes next to the source:

| File | Contents |
| --- | --- |
| `.bin` | binary image starting at `.org` |
| `.hex` | Intel HEX |
| `.pap` | KIM-1 paper tape format, loaded with the monitor's `L` command |
| `.lst` | listing: source line, instructions, bytes, cycles, cycle total per line |
| `.map` | memory map: variables, procedure frames (overlay), zero page usage |
| `.sym` | symbol table |

`run` compiles the program and runs it in the emulator. It prints the variables, I/O writes, calls of monitor routines and the text sent to the TTY.

- `--input NAME=h1,h2,...` gives hex values for successive reads of a register (`PAD`) or return values of a monitor routine (`GETKEY`). After the last value, the last one repeats.
- `--input NAME=@cycle:value,...` changes the value at the given cycle, regardless of how often it is read.
- `--max N` sets the cycle limit (default 1,000,000, i.e. 1 s at 1 MHz).
- `--trace` writes the executed instructions to a `.trace` file.

Ports without an `--input` return memory contents, usually `$00`; the `run` output points such reads out.

The emulator contains no monitor ROM. Every `JSR` into `$1800-$1FFF` is logged with the registers and returns immediately, and a jump to `$1C4F` (START) ends the run. The user RIOT timer (`$1704-$170F`) is emulated including its interrupt; IRQs go through the vector at `$17FE` as on the KIM-1.

## Examples

In the `examples` folder:

| File | Shows |
| --- | --- |
| `hello.tantal` | for loop, procedure with a pointer, TTY output |
| `arithmetic.tantal` | byte and card, multiplication, division, shifts, comparisons |
| `bits.tantal` | bit aliases, bit fields, a shadow register |
| `procedures.tantal` | parameters in registers and slots, return values, overlay |
| `strings.tantal` | strings, pointers, `mem[]`, const tables |
| `analyzer.tantal` | logic analyzer: `wait change ... from ... timeout`, ring, display |
| `interrupts.tantal` | timer IRQ handler, ring shared with an interrupt, `atomic` |
| `display.tantal` | refreshing the six-digit LED display: 0-9, space and A-Z from `$10-$15` |

Examples that read a port need input in the emulator:

```
tantal run examples/analyzer.tantal --max 5000000 \
  --input PAD=@0:00,@500:01,@800:03,@1500:00,@1600:80
tantal run examples/interrupts.tantal --input PAD=@0:00,@3000:05,@6000:07
tantal run examples/bits.tantal --input PAD=C0
```

## What version 0.1 supports

- Types `byte`, `card` (16-bit unsigned), `byte pointer`, arrays `byte[n]`, `string` (length in the first byte), `ring byte[n]`, constants `const`.
- Variables at a fixed address with `@`; qualifiers `readonly`, `writeonly`, `shadow`, `noinit`.
- Bit aliases `bit`, bit fields `bits`, direct bit access `PAD.7`; bits 7 and 6 are tested with the `BIT` instruction.
- Directives `.org`, `.end`, `.zp exclude`, `.include`, `.target`, `.cpu 6502`, `.timeout timer|loop`.
- `if/elif/else`, `do ... od`, `do ... until ... od`, `while`, `for ... to/until ... step`, `exit`.
- Procedures: first three parameter bytes in A/X/Y, the rest in frame slots, return value in A or A/X. Frame overlay from the call graph, recursion check, `irqsafe` (a copy of the procedure for each context).
- `interrupt irq/nmi proc`, including passing a BRK instruction on to the monitor at `$1C00`; `atomic`; inline `asm ... endasm` with access to Tantal variables.
- `extern proc` for monitor routines; `pal2.tantal` with both RIOTs and the monitor routines is loaded automatically.
- `wait <condition>`, `wait change X [from e]`, `wait rising/falling X.n`, each with an optional `timeout N us|ms|s [then ... fi]`; on timeout the assignment or push is skipped and the block runs instead. The timeout uses the RIOT timer or, with `.timeout loop`, counts loop passes (works without a timer).
- `delay N us|ms|s|cycles`: a busy wait, exact to the cycle for a constant; `delay t ms|s` with a variable count.
- Ring buffer: `buf <- x`, `pop`, `buf[i]`, `last`, `len`, `full`, `clear`.
- Multiplication, division and `mod` (powers of 2 as shifts and masks, otherwise a 16-bit runtime routine); division by zero gives all ones.
- Global variables are initialized on every start; unused variables and procedures are left out of the program.
- A warning when data is modified by both the main program and an interrupt handler outside `atomic`.

## Not yet implemented

| Feature | Status |
| --- | --- |
| `.cycles` check | the directive is parsed but not checked; the listing shows cycles per line |
| `test ... endtest` blocks and timed inputs in tests | only `tantal run` with `--input` so far |
| `tantal upload`, `tantal watch` | send the `.pap` file by hand through a terminal (2400 bps 8N2, `L` command) |
| `int`, `sbyte` and signed comparisons | not supported |
| `card[n]` arrays, `card pointer` | not supported |
| `card` return value from `extern proc` | not supported |
| Zero page priority by access count | variables go to the zero page in declaration order |
| Packing `bit READY` flags eight to a byte | each flag takes its own byte |
| `.vars` | not supported; variables go to the zero page and after the code |
| 65C02 instructions | not supported (`.cpu 6502`) |

## Implementation choices

These were made where the language design left the question open; all of them can change.

- **The bound of a for loop** is evaluated once, at the start of the loop.
- **A pointer parameter** lives in the procedure frame and is copied to the zero page helper pointer `__mp` before `p[i]`. Pointers declared as variables always live in the zero page.
- **Helper variables** `__mp` (pointer for `mem[]` and large arrays) and `__rt` (multiplication, division) are shared. When both the main program and an interrupt handler use them, the compiler warns.
- **An IRQ handler** enables interrupts with `CLI` at the end of the startup code.
- **A shadow register with an initial value** (`byte OUT @ $1702 shadow = $81`) is written to both the copy and the register at startup; without an initial value the copy starts at 0 and the register is not written.
- **A `noinit` variable with an initial value** is stored in the binary after the code (not in the zero page), so that loading the program sets the value.
- **A `"..."z` literal** has type `byte pointer`; a plain literal in an expression gives the address of a length-prefixed string.
- **Emulator inputs** are given in hexadecimal.

## To verify on real PAL-2 hardware

- **PAP format.** Record `;LLAAAA<data>CCCC`, where the checksum is the 16-bit sum of the count, address and data bytes; the last record is `;00NNNNCCCC` with the number of records. This follows the KIM-1 description but has not been tried against the PAL-2 monitor.
- **`timeout` timing** assumes a 1 MHz clock and a timer at `$1704-$1707` that counts clock cycles like the 6530 in the KIM-1. In the emulator the timeout is accurate to one timer tick. With `.timeout loop` the compiler computes the number of passes after layout from the shortest pass, so the timeout is never early; interrupts make it longer. A target without `timeout.timer`, or with `timeout = loop`, uses loop counting by default.
- **The range `$1750-$177F`** is verified: it repeats the system RIOT registers `$1740-$174F` every 16 bytes. It stays reserved in the target so the compiler places nothing there and warns about `@` addresses in it.

## Testing

- The CPU emulator passes Klaus Dormann's 6502 functional test (`6502_functional_test.bin`, including decimal mode). The test runs when the `DORMANN` variable points to the image: `DORMANN=.../6502_functional_test.bin go test ./cpu`.
- `go test ./emu` compiles all examples and further programs (loops at the 255/256 boundary, ring, wait with edges and timeouts, interrupts, irqsafe, asm, large arrays, compile errors, warnings) and checks the results in the emulator.

## Layout

| Package | Contents |
| --- | --- |
| `cpu` | NMOS 6502 instruction table and CPU emulator |
| `asm` | assembler with branch relaxation, inline assembler, listing |
| `lang` | lexer, AST, parser |
| `compiler` | semantics, code generation, memory allocation, output formats, targets |
| `emu` | PAL-2 model for `tantal run`, program tests |
| `cmd/tantal` | command line |
