# Tantal – context for AI assistants and contributors

This file carries the design decisions and project state for anyone (human or AI assistant) continuing work on Tantal. Read it together with `README.md`, which lists what version 0.1 supports and what is missing.

## Background

- Tantal is a small compiled language for the MOS 6502, inspired by Action! for the Atari: fast prototyping, no boilerplate, constructs that map closely to 6502 instructions.
- The primary target is the **PAL-2**, a modernized KIM-1 clone built by the maintainer. The first intended application is a simple **logic analyzer**: watch a sequence of signals on the user port and show information on the onboard display.
- The compiler is written in Go (1.22+), has no dependencies and must build for macOS, Linux and Windows.
- The full language specification (v0.1) is in Czech: [docs/spec-cs.md](docs/spec-cs.md), a Markdown export of the Claude Docs document "Tantal – jazyk pro 6502: specifikace v0.1" (https://claude.ai/code/artifact/ee52ff92-2650-454d-bede-b9b6af2be70c). This file summarizes the decisions that matter for the code. The English tutorial is [docs/tutorial.md](docs/tutorial.md).

## PAL-2 hardware facts

- CPU: NMOS 6502 at 1 MHz. No 65C02 instructions for now.
- RAM: 128 KB chip with A16 grounded, so the whole 64 KB address space is RAM except the holes below. Usable RAM: `$0000-$16FF` and `$2000-$FFF7`.
- Memory map as on the KIM-1:
  - `$1700-$173F` user RIOT (6530/6532-style): `PAD $1700`, `PADD $1701`, `PBD $1702`, `PBDD $1703`, timer `$1704-$170F`.
  - `$1740-$177F` system RIOT: display, keypad, used by the monitor.
  - `$1750-$177F` contents unknown (probably a gap or RIOT mirror); reserved in the target until verified.
  - `$1780-$17FF` RIOT RAM; `$17FA` NMI vector, `$17FE` IRQ vector.
  - `$1800-$1FFF` monitor ROM. `START $1C4F` returns to the monitor, `$1C00` handles BRK.
  - Zero page `$EF-$FF` is used by the monitor (`$F9-$FB` display buffer).
- The display must be refreshed by the program (e.g. repeated `SCANDS` calls); the language leaves timing of the refresh to the programmer. The built-in monitor routines show only hex digits, so custom display routines through the system RIOT will be needed. The segment/digit wiring of the system RIOT is not known yet.
- Programs are loaded as KIM-1 paper tape (PAP) over serial at 2400 bps (8N2) with the monitor's `L` command.
- `$0200-$16FF` is often occupied by a loader, so the compiler places code at `$2000` and variables in the zero page first, then after the code.

## Language decisions (agreed)

- **Name**: Tantal, file extension `.tantal`.
- **Case-sensitive**. Keywords and built-ins are lowercase only. Convention: variables and procedures lowercase, I/O registers and constants uppercase. The compiler warns when two names differ only in case (planned).
- **No stack for parameters or locals.** Fixed addresses, overlay by call graph, first three parameter bytes in A/X/Y, return in A or A/X. Recursion is a compile error. A procedure called from both main and an interrupt is an error unless marked `irqsafe` (one copy per context).
- **Types**: `byte`, `card` (16-bit unsigned); `int`/`sbyte` planned. Result of a binary operator has the type of the wider operand, `byte * byte` is `byte` and wraps; use `card(a) * b` for a 16-bit product. Mixed signed/unsigned comparisons will be errors. Division by zero: `x / 0` is all ones, `x mod 0` is `x`.
- **I/O**: `byte PAD @ $1700`; qualifiers `readonly`, `writeonly`, `shadow` (writes go through a RAM copy), `noinit`. Bit aliases `bit LED = PBD.0`, bit fields `bits MODE = PBD.4..6`. `.` after a variable is reserved for bits.
- **Initialization**: globals are initialized on every program start (re-run from the monitor without reloading); `noinit` on a variable opts out. Locals cannot be `noinit`.
- **Pointers**: `byte pointer p`, accessed only by index `p[i]` (maps to `(zp),Y`). No dereference operator; `^` is XOR. `mem[addr]` is a built-in array over the whole memory.
- **Strings**: length in the first byte (as in Action!), max 255, characters indexed from 1, `len(s)`. `"..."z` is a zero-terminated literal of type `byte pointer`.
- **Tables**: `byte[] T = 1, 2, 3` (initialized array), `const byte[] T = ...` (read-only, stored once, not copied at start), `const byte[] F = file "font.bin"`. A line ending with a comma continues.
- **Ring buffer**: `ring byte[N] buf`, N a power of 2 up to 256. `buf <- x` push (statement only; inside an expression `<-` reads as `< -` and warns), `pop(buf)` (expression, returns 0 when empty, no waiting), `buf[i]` from the oldest, `last(buf, k)` from the newest, `len`, `full`, `clear`. No method-call syntax (`buf.push`) on purpose.
- **wait** is an expression returning the whole port byte: `wait <cond>`, `wait change PAD` (compared with the value at the start of the wait), `wait change PAD from PAD_OLD` (compared with a user variable that wait updates itself), `wait rising/falling PAD.7`. Optional `timeout N us|ms|s`, measured with the user RIOT timer, sets `timedout`. No hidden variables.
- **for**: `to` is inclusive, `until` exclusive (use it for arrays and buffers: zero passes when the length is 0). The end is tested before incrementing, so a bound of 255 with a byte counter works.
- **Directives**: `.org`, `.end`, `.zp exclude $F0-$FF`, `.include`, `.target`, `.cpu`, `.cycles <= N` (a pragma applying to the next statement; no closing directive on purpose).
- **atomic ... endatomic** wraps code in `PHP/SEI ... PLP`. The compiler warns when data is modified by main and an interrupt outside `atomic`.
- **Testing** in phases: (1) `tantal run` with `--input`, ROM calls only logged, no stubs; (2) `test ... endtest` blocks in a separate `*_test.tantal` file; (3) timed inputs. Phase 1 is done.

## Open items / next steps

- Check `.cycles` limits (worst-case path, page crossings after layout).
- `test ... endtest` blocks (phase 2).
- `int`/`sbyte`, `card` arrays and `card pointer`.
- Zero page priority by weighted access count; packing `bit` flags.
- `tantal upload` over serial; `tantal watch`.
- Verify on real hardware: PAP format against the PAL-2 monitor, RIOT timer behavior, `$1750-$177F`, system RIOT display wiring.
- Choose a license before publishing.

## Working on the code

- Build: `go build -o tantal ./cmd/tantal`; test: `go test ./...`. Every language change should come with a test in `emu/programs_test.go` that compiles a program and checks results in the emulator.
- The CPU emulator passes Klaus Dormann's functional test (`DORMANN=path/to/6502_functional_test.bin go test ./cpu`).
- Code, comments, messages and docs are in English.
