# Tantal Tutorial

This tutorial introduces every element of the Tantal language, version 0.1, with short examples. It assumes you know what a byte, a register and a memory-mapped port are; knowing 6502 assembler helps but is not required.

Tantal compiles to MOS 6502 machine code. The examples target the PAL-2 (a modernized KIM-1), which is the default target.

Contents:

1. [First program](#1-first-program)
2. [Source text](#2-source-text)
3. [Variables and types](#3-variables-and-types)
4. [Constants](#4-constants)
5. [Expressions](#5-expressions)
6. [Assignment](#6-assignment)
7. [Control flow](#7-control-flow)
8. [Arrays, tables and memory](#8-arrays-tables-and-memory)
9. [Pointers](#9-pointers)
10. [Strings](#10-strings)
11. [I/O registers](#11-io-registers)
12. [Bits and bit fields](#12-bits-and-bit-fields)
13. [Procedures](#13-procedures)
14. [Monitor routines: `extern proc`](#14-monitor-routines-extern-proc)
15. [Ring buffers](#15-ring-buffers)
16. [Waiting for signals: `wait`](#16-waiting-for-signals-wait)
17. [Interrupts and `atomic`](#17-interrupts-and-atomic)
18. [Inline assembler](#18-inline-assembler)
19. [Directives](#19-directives)
20. [The PAL-2 target](#20-the-pal-2-target)
21. [Building and running](#21-building-and-running)
22. [Quick reference](#22-quick-reference)

---

## 1. First program

```
; hello.tantal
byte i
card sum

for i = 1 to 10 do
  sum += i
od

OUTCH('S')
PRTBYT(lo(sum))      ; prints 37 (hex of 55)
CRLF()
```

There is no `main`, no module header and no `import`. The statements at the top level of the file are the program, executed from top to bottom. When they finish, control returns to the monitor.

Compile and try it in the emulator:

```
tantal build hello.tantal      ; writes hello.bin, .hex, .pap, .lst, .map, .sym
tantal run hello.tantal        ; runs it and prints variables and TTY output
```

## 2. Source text

**Comments** start with `;` and run to the end of the line.

**One statement per line.** A line ends a statement. Two ways to continue a long line:

```
const byte[] DIGITS = '0', '1', '2', '3',     ; a line ending with a comma continues
                      '4', '5', '6', '7'
card total
total = 1000 + 2000 + \
        3000                                  ; backslash joins the next line
```

A short block may stay on one line: `if x = 0 then exit fi`, `while b < 7 do b += 1 od`.

**Names** consist of letters, digits and `_` and start with a letter or `_`. Tantal is **case-sensitive**. Keywords and built-ins are lowercase only. By convention, variables and procedures are lowercase and I/O registers and constants are uppercase (`PAD`, `MAX_LEN`).

**Numbers:**

| Form | Example | Value |
| --- | --- | --- |
| decimal | `200` | 200 |
| hexadecimal | `$C8` | 200 |
| binary | `%1100_1000` | 200 |
| character | `'A'` | 65 |

`_` may separate digits in any base: `%0101_0001`, `$12_34`.

**Strings** are in double quotes: `"HELLO"`. A `z` right after the closing quote makes a zero-terminated string: `"HELLO"z` (see [Strings](#10-strings)).

**Reserved names** of built-in functions and variables cannot be redefined: `pop`, `last`, `len`, `full`, `clear`, `lo`, `hi`, `sizeof`, `mem`.

## 3. Variables and types

| Type | Size | Range |
| --- | --- | --- |
| `byte` | 1 byte | 0 to 255 |
| `card` | 2 bytes, little endian | 0 to 65535 |
| `byte pointer` | 2 bytes (in the zero page) | an address |
| `byte[n]` | n bytes | array |
| `string`, `string[n]` | length byte + characters | text up to 255 characters |
| `ring byte[n]` | n bytes + head and count | ring buffer |

`int` and `sbyte` (signed types) are planned but not in version 0.1.

```
byte a, b = 7, c       ; several variables of one type
card x = 1000
card big = $FFFF
```

**Initialization.** Global variables get their initial value (or 0) **every time the program starts**, including a re-run from the monitor without reloading. The initial value of a global must be a constant.

**`noinit`** opts out: the variable keeps its value between runs. If it has an initial value, that value is set by loading the program.

```
byte runs = 0 noinit    ; counts runs since the program was loaded
runs += 1
```

**Placement.** You never choose where an ordinary variable lives. The compiler puts variables into the free part of the zero page first (in declaration order) and, when that is full, into RAM after the code. Unused variables are left out. The `.map` file shows the result.

Declarations of the main program belong at the top level of the file, not inside `if` or loops.

## 4. Constants

A named constant has no storage; it is replaced by its value:

```
const MAX = 100
const MASK = $0F
const LIMIT = MAX * 2 + 1
```

`const` before an array declares a read-only table; see [Arrays](#8-arrays-tables-and-memory).

## 5. Expressions

### Operators

From the highest precedence to the lowest:

| Level | Operators | Meaning |
| --- | --- | --- |
| 1 | `-x` `~x` `not x` `@name` | negation, bitwise NOT, logical NOT, address of |
| 2 | `*` `/` `mod` | multiplication, division, remainder |
| 3 | `+` `-` | addition, subtraction |
| 4 | `<<` `>>` | shift left, shift right |
| 5 | `&` | bitwise AND |
| 6 | `\|` `^` | bitwise OR, bitwise XOR |
| 7 | `=` `<>` `<` `<=` `>` `>=` | comparison |
| 8 | `and` | logical AND, short-circuit |
| 9 | `or` | logical OR, short-circuit |

Postfix forms (`a[i]`, `p[i]`, `X.n`, calls) bind tightest of all. `and` and `or` evaluate the right operand only when needed.

Note two things that differ from C:

- `=` is both assignment (as a statement) and comparison (in an expression). There is no `==`.
- Bitwise operators bind tighter than comparisons, so `d & 3 = 0` means `(d & 3) = 0`, as you would want. Shifts bind looser than `+`: `x << 1 + 1` is `x << 2`.

`^` is always XOR. There is no dereference operator; use an index (see [Pointers](#9-pointers)).

### Types in expressions

The result of a binary operator has the type of its **wider** operand. `byte op byte` is a `byte` and wraps around:

```
byte a = 200, b = 7, r
card w

r = a + b          ; 207
r = b - a          ; 63 (7 - 200 wraps around)
r = b * 40         ; 24 (280 wraps around)
w = card(b) * 40   ; 280: convert one operand to get a 16-bit result
w = a + 1000       ; 1200: card + byte is card
r = -b             ; 249
```

Storing a 16-bit value into a `byte` keeps the low byte and gives a warning; use `lo()` to say you mean it.

**Division** by a power of two compiles to shifts and masks; any other divisor uses a small 16-bit runtime routine. Division by zero is defined: `x / 0` gives all ones (`$FF` or `$FFFF`) and `x mod 0` gives `x`.

### Conversions and built-in functions

| Function | Result |
| --- | --- |
| `lo(x)` | low byte of a 16-bit value |
| `hi(x)` | high byte of a 16-bit value |
| `byte(x)` | conversion to byte (low byte) |
| `card(x)` | conversion to card |
| `sizeof(v)` | size of a global variable in bytes (a constant) |
| `len(v)` | length of a string, pointed-to string or ring |

```
card x = $1234
byte h, l
h = hi(x)      ; $12
l = lo(x)      ; $34
```

### Truth values

A comparison gives 1 (true) or 0 (false), and you can store it. In a condition, any non-zero value is true.

```
byte a = 5, b, c
b = a > 3                  ; 1
c = a > 3 and a < 10       ; 1
if not (a = 5) then c = 0 fi
```

## 6. Assignment

```
x = 5
x += 1      ; also -=  &=  |=  ^=  <<=  >>=
```

There is no `*=`, `/=`, `++` or `--`. An assignment is a statement, not an expression.

## 7. Control flow

### if

```
if c = 1 then
  d = 10
elif c = 2 then
  d = 20
else
  d = 0
fi
```

`elif` and `else` are optional; the block always ends with `fi`.

### Loops

```
do                        ; endless loop, leave it with exit
  a += 1
  if a = 5 then exit fi
od

while b < 7 do            ; test before each pass
  b += 1
od

do                        ; test after each pass
  c += 2
until c >= 9 od
```

`exit` leaves the innermost loop.

### for

```
for i = 1 to 10 do ... od          ; to: inclusive, 10 passes
for i = 0 until 10 do ... od       ; until: exclusive, 10 passes (0..9)
for i = 0 to 100 step 10 do ... od ; 0, 10, ..., 100
```

- Use `until` for arrays and buffers: `for i = 0 until len(buf) do` runs zero times when the length is 0.
- The end is tested **before** incrementing, so `for i = 0 to 255` with a `byte` counter works and stops.
- The bound is evaluated once, at the start of the loop.
- `step` must be a constant from 1 to 255. Loops only count up.
- The counter is a `byte` or `card` variable declared beforehand. A `card` counter is needed above 255: `for k = 0 until 300 do`.

## 8. Arrays, tables and memory

```
byte[8] buf              ; 8 bytes, zeroed at start
byte[8] init = 1, 2, 3   ; the rest is zero
byte[] sq = 0, 1, 4, 9   ; size from the initializer (4)
byte[300] big            ; more than 256 elements: index with a card
```

Indexes start at 0. An index can be any expression; a constant index outside the array is a compile error. Arrays hold `byte`s only in version 0.1 (no `card[n]`).

```
byte i, total
for i = 0 until 4 do
  total += sq[i]
od
```

You cannot assign a whole array or use it as a value. Its address is `@buf`.

**Tables.** `const` before an array makes a read-only table. It is stored once in the program and, unlike an initialized array, is not copied at every start:

```
const byte[] HEX = '0', '1', '2', '3', '4', '5', '6', '7',
                   '8', '9', 'A', 'B', 'C', 'D', 'E', 'F'
const byte[] FONT = file "font.bin"     ; contents of a binary file
```

A local array (inside a procedure) cannot have initial values; use a global `const` table.

**Memory.** `mem[addr]` is a built-in byte array over the whole 64 KB address space:

```
card addr = $0300
mem[$0300] = $55
mem[addr + 1] = mem[addr] + 1
```

## 9. Pointers

A `byte pointer` holds an address. It is accessed **only by index** (`p[i]`, which maps to the 6502 `(zp),Y` mode); there is no `*p`.

```
byte[8] buf = 1, 2, 3
byte pointer p
byte x

p = @buf        ; @ gives the address of a variable
p[3] = 40       ; buf[3] = 40
p += 1          ; pointer arithmetic is card arithmetic
x = p[0]        ; 2
```

Pointer variables always live in the zero page. `card pointer` is not supported in version 0.1. Arrays are passed to procedures as a `byte pointer` (see [Procedures](#13-procedures)).

## 10. Strings

A `string` stores its length in the first byte (as in Action!), followed by up to 255 characters. Characters are indexed **from 1**; index 0 is the length.

```
string msg = "TANTAL"      ; 7 bytes: 6, 'T', 'A', ...
string[16] line            ; room for 16 characters
byte n, c

n = len(msg)               ; 6
c = msg[1]                 ; 'T'
line[0] = 2
line[1] = 'O'
line[2] = 'K'
```

String literals:

- `"..."` in an expression gives the address of a length-prefixed copy of the text. Use it as a `byte pointer` argument: `puts("HELLO")`.
- `"..."z` is a zero-terminated literal of type `byte pointer`, for routines that expect a terminating 0.

```
proc puts(byte pointer s)
  byte k
  for k = 1 to len(s) do      ; len() of a pointer reads s[0]
    OUTCH(s[k])
  od
endproc

proc putz(byte pointer s)
  byte k
  while s[k] <> 0 do
    OUTCH(s[k])
    k += 1
  od
endproc

puts(@msg)
puts("HELLO")
putz("WORLD"z)
```

## 11. I/O registers

Any byte in memory, including an I/O register, can be given a name with `@`:

```
byte PORT @ $1700
byte LATCH @ $1702 writeonly
byte STATUS @ $1707 readonly
```

A variable at a fixed address must be global and cannot have an initial value (except a `shadow` register, below). The PAL-2 registers (`PAD`, `PBD`, ...) are predefined; see [The PAL-2 target](#20-the-pal-2-target).

Qualifiers:

| Qualifier | Effect |
| --- | --- |
| `readonly` | writing is a compile error |
| `writeonly` | reading is a compile error; so is `+=` and changing a single bit |
| `shadow` | the compiler keeps a RAM copy; every write goes to the copy and the register, reads come from the copy |
| `noinit` | not initialized at start (for ordinary variables) |

`shadow` makes read-modify-write safe on a register that cannot be read back, or that reads differently from what was written:

```
byte OUT @ $1702 shadow = $81   ; copy and register both set to $81 at start
OUT |= $04                      ; reads the copy, writes both
```

## 12. Bits and bit fields

`.` after a register or byte variable selects bits:

```
PBD.0 = 1                 ; one bit
if PAD.7 then ... fi      ; bits 7 and 6 are tested with the BIT instruction
x = PAD.4..6              ; bits 4 to 6, shifted down: 0..7
```

Named aliases are clearer:

```
bit  LED  = PBD.0         ; a single bit
bit  READY = PAD.7
bits MODE = PBD.4..6      ; a bit field

PBDD = $FF                ; port B as output
LED = 1
LED ^= 1                  ; toggle (^= on a bit needs a constant)
MODE = 5                  ; writes bits 4-6 only
if READY and not PAD.1 then LED = 0 fi
```

A value that does not fit in the field gives a warning. Writing a bit of a `writeonly` register needs `shadow`.

A `bit` without a register is a flag in RAM (in version 0.1 each flag takes a whole byte):

```
bit done          ; starts as 0
bit armed = 1
if armed then done = 1 fi
```

## 13. Procedures

```
proc max(byte a, byte b): byte
  if a > b then
    return a
  fi
  return b
endproc

proc square(byte v): card
  return card(v) * v
endproc

proc beep()
  OUTCH(7)
endproc

byte m
card s
m = max(5, 9)
s = square(m) + square(3)
beep()
```

- Parameters are `byte`, `card` or `byte pointer`. The return type, after `:`, is `byte` or `card`; without it the procedure returns nothing.
- `return` leaves the procedure; `return x` gives the result. A procedure with a return type must return a value; one that may fall off the end gives a warning.
- A call of a procedure without a result is a statement; a call with a result can be used in any expression.
- Local variables are declared inside the body. They are not initialized (and cannot be `noinit`). Only variable declarations are allowed inside a procedure: no constants, bits or nested procedures.
- Procedures are declared at the top level and can be used before or after their declaration. Unused procedures are left out of the program.

**No stack for parameters and locals.** This is the central design choice of Tantal. The 6502 stack only holds return addresses. Parameters and locals live at fixed addresses (the procedure's *frame*):

- The first three parameter bytes are passed in **A, X and Y**; the rest go to the frame. A `card` parameter takes two bytes.
- A `byte` result is returned in A, a `card` result in A (low) and X (high).
- Procedures that can never be active at the same time share frame memory. The compiler computes this *overlay* from the call graph; the `.map` file shows it.
- Consequently, **recursion is a compile error**, direct or indirect.

`@name` of a procedure gives its address.

## 14. Monitor routines: `extern proc`

An `extern proc` describes a routine that already exists in ROM. You state which register carries each parameter and the result, and the address:

```
extern proc PUTC(byte c @A) @ $1EA0          ; same routine as the predefined OUTCH
extern proc READC(): byte @A @ $1E5A
extern proc SETXY(byte x @X, byte y @Y) @ $2F00

byte ch
ch = READC()
PUTC(ch)
SETXY(3, 4)
```

Parameters must be `byte`, each in a different register; the result is a `byte` in A. The PAL-2 monitor routines are already declared under their usual names (see [section 20](#20-the-pal-2-target)), so declaring `OUTCH` again would be an error.

## 15. Ring buffers

A ring buffer keeps the last N values. N is a power of two from 2 to 256. Rings are global.

```
ring byte[8] r
byte a, b, n

r <- 3            ; push (a statement); when full, the oldest value is dropped
r <- 4
r <- 5
n = len(r)        ; 3
a = r[0]          ; 3: index from the oldest
b = last(r, 0)    ; 5: index from the newest
a = pop(r)        ; 3: remove and return the oldest; 0 when empty, never waits
if full(r) then clear(r) fi
```

| Operation | Meaning |
| --- | --- |
| `buf <- x` | push x (statement only) |
| `pop(buf)` | take the oldest value; 0 when empty |
| `buf[i]` | i-th value from the oldest |
| `last(buf, k)` | k-th value from the newest |
| `len(buf)` | number of stored values |
| `full(buf)` | true when the ring is full |
| `clear(buf)` | empty the ring |

Write `<-` only as a statement. Inside an expression, `a<-1` reads as `a < -1` and the compiler warns. There is deliberately no method-call syntax such as `buf.push(x)`.

## 16. Waiting for signals: `wait`

`wait` is an expression: it waits for a condition on a port and returns the **whole port byte** read at that moment. It can also stand alone as a statement.

```
byte v, old

wait PAD.3 = 1                 ; any condition
v = wait change PAD            ; until PAD differs from its value at the start
v = wait change PAD.0          ; until one bit changes
v = wait rising PAD.7          ; 0 -> 1 on bit 7
v = wait falling PAD.7         ; 1 -> 0
v = wait change PAD from old   ; until PAD differs from old
```

`change ... from` compares with a value of your choice instead of the value at the start of the wait. It is meant for loops that must not miss a change between two waits: keep the last recorded state in a variable and assign the result back to it. `wait` itself never writes to the variable; `from` is an ordinary byte expression (`from old`, `from old + 1`, `from $FF`), evaluated once before the wait. With a bit or a bit field (`wait change PAD.4..5 from old`), only those bits of the value are compared; the result is still the whole port byte.

```
cur = PAD
do
  cur = wait change PAD from cur   ; cur always holds the last state
  ...
od
```

**Timeout.** Any form can take `timeout N us|ms|s` (a constant up to 60 s), optionally followed by a block `then ... fi` that runs when the time runs out. On timeout the assignment or push the wait belongs to **does not happen**: the variable keeps its value and nothing goes into the ring. The block runs instead, and the program continues after the statement. There is no `timedout` flag to check afterwards; what should happen on a timeout belongs in the block.

```
v = wait rising PAD.6 timeout 500 ms then
  OUTCH('T')                   ; v keeps its previous value
fi
log <- wait change PAD timeout 1 s then exit fi
wait PAD.7 = 0 timeout 20 ms   ; wait at most 20 ms, no block
```

Because the assignment is skipped, a wait with a timeout must be the whole statement: `wait ...`, `x = wait ...` or `buf <- wait ...`. Inside a larger expression or an `if` condition the compiler reports an error. `exit` in the block leaves the enclosing loop, as in an `if`.

The timeout is measured in one of two ways, chosen with `.timeout` (see [Directives](#19-directives)):

- `timer` (the default on PAL-2): the user RIOT timer counts the time. Interrupts do not change the result, but the program must not use the timer for anything else at the same time.
- `loop`: the wait counts its own loop passes in a 24-bit counter. The compiler knows the cycles of every pass, including page crossings, so it works on any 6502 and leaves the timer free. The timeout never expires early; when the condition has paths of different length, it is computed from the shortest one. Time spent in interrupt handlers is not counted, so with interrupts the timeout gets longer (the compiler warns). A condition that calls a procedure or a runtime routine (multiplication, division) cannot be timed this way. The `.map` file shows the pass length and count of every such wait.

The timeout block is outside the wait loop, so it does not make a pass longer.

A complete example, recording changes until the port goes quiet for 2 seconds:

```
ring byte[256] log
byte cur
card changes

PADD = $00                     ; port A as input
cur = PAD
do
  cur = wait change PAD from cur timeout 2 s then exit fi
  log <- cur
  changes += 1
od
```

### Delays: `delay`

`delay` waits a given time, keeping the CPU busy (it does not sleep, hence the name):

```
delay 500 us
delay 20 ms
delay 2 s
delay 100 cycles       ; independent of the clock
```

**A constant delay is exact to the cycle**: from the end of the previous statement to the start of the next one it takes exactly the given time, computed from the target `clock` (`us` rounds to the nearest cycle). Up to 40 cycles it is a row of `NOP` (and one `BIT` for an odd count); longer delays load A, X and Y and call a small counting routine that the compiler keeps within one page, so the time does not depend on where the code lands. The shortest delay is 2 cycles; `delay 1 cycles` is an error. The longest is 60 s.

**A variable delay** takes a `byte` or `card` count in `ms` or `s`:

```
delay t ms             ; t = 0 .. 65535
delay pause s
```

Every millisecond takes exactly `clock/1000` cycles, plus 5 cycles once every 256 ms; the call itself adds about 30 cycles, and `s` about 70 more per second. `us` and `cycles` need a constant.

`delay` changes A, X, Y and the flags. It uses no memory (except a counter for a variable `s`) and no timer, so it works in an interrupt handler too. Time spent in interrupt handlers during a delay is not counted, so the delay gets longer; the compiler warns when the program has an interrupt handler.

## 17. Interrupts and `atomic`

An interrupt handler is a procedure marked `interrupt irq` or `interrupt nmi`. It has no parameters and no result, and it cannot be called. The compiler installs it in the vector (`$17FE` for IRQ, `$17FA` for NMI on the PAL-2), saves and restores registers and, for IRQ, enables interrupts with `CLI` at the end of startup. A `BRK` instruction is passed on to the monitor.

```
card ticks
ring byte[16] samples
byte n, got

interrupt irq proc onTimer()
  ticks += 1
  samples <- PAD
  TIM64I = 16                  ; next interrupt in 16 x 64 us
endproc

TIM64I = 16                    ; start the timer with interrupt
while ticks < 10 do od

atomic
  n = len(samples)
  got = pop(samples)
endatomic
```

**`atomic ... endatomic`** runs its body with interrupts disabled (`PHP SEI ... PLP`). `exit` and `return` must not leave an atomic block. The compiler **warns** when the main program and an interrupt handler both modify the same data outside `atomic`.

**`irqsafe`.** Because frames are at fixed addresses, a procedure called both from the main program and from an interrupt handler would overwrite its own frame. That is a compile error unless the procedure is marked `irqsafe`; the compiler then makes one copy per context:

```
irqsafe proc dbl(byte v): byte
  return v + v
endproc
```

## 18. Inline assembler

`asm ... endasm` inserts 6502 instructions. Tantal variables, constants and procedures can be used by name; labels are local to the block.

```
byte x = 3, y
const K = 10

asm
  LDA x
  CLC
  ADC #K
  STA y          ; y = 13
loop:
  DEC x
  BNE loop
  .byte $EA, $EA ; raw bytes (two NOPs)
endasm
```

The usual addressing modes are supported (`#imm`, `zp`, `abs`, `,X`, `,Y`, `(zp,X)`, `(zp),Y`, `(abs)`). In an operand, `<name` and `>name` give the low and high byte, and `name+n` an offset. Only NMOS 6502 instructions are accepted.

## 19. Directives

Directives start with `.` at the beginning of a line.

| Directive | Meaning |
| --- | --- |
| `.org $3000` | address of the code (default from the target, `$2000` on PAL-2) |
| `.end $3FFF` | the program must end below this address |
| `.zp exclude $F0-$FF` | keep the compiler's variables out of this part of the zero page |
| `.include "lib.tantal"` | include another file (relative to this one, at most once) |
| `.target pal2` | fail unless compiling for this target |
| `.cpu 6502` | the CPU; only `6502` in version 0.1 |
| `.timeout loop` | how `wait ... timeout` measures time: `timer` (RIOT timer) or `loop` (counted loop passes); the default comes from the target |
| `.cycles <= 40` | cycle limit for the next statement; parsed but not checked yet (the listing shows cycles) |

## 20. The PAL-2 target

The target file `pal2.tantal` is loaded automatically. It defines:

**User RIOT** (application connector): `PAD $1700`, `PADD $1701` (direction of port A, 1 = output), `PBD $1702`, `PBDD $1703`.

**System RIOT** (display, keypad): `SYS_PA`, `SYS_PADD`, `SYS_PB`, `SYS_PBDD` at `$1740-$1743`.

**Timer** (user RIOT). Writing starts a countdown with a divider of 1, 8, 64 or 1024 µs: `TIM1`, `TIM8`, `TIM64`, `TIM1024`, and the same with an interrupt: `TIM1I`, `TIM8I`, `TIM64I`, `TIM1024I`. Reading: `TIMCNT`, `TIMCNTI`, `TIMSTAT`, and the bit `TIMEOUT` (1 = finished). `wait ... timeout` uses this timer too, unless the program says `.timeout loop`.

**Monitor routines:**

| Routine | Does |
| --- | --- |
| `OUTCH(c)` | character to the TTY |
| `PRTBYT(b)` | byte as two hex digits to the TTY |
| `OUTSP()` | space to the TTY |
| `CRLF()` | new line on the TTY |
| `GETCH(): byte` | character from the TTY |
| `SCANDS()` | show the display buffer once (call it repeatedly) |
| `KEYIN()` | keypad check |
| `GETKEY(): byte` | key 0-$15 |

**Display:** `DISP0`, `DISP1`, `DISP2` (`$F9-$FB`) hold the six hex digits shown by `SCANDS`. `SEG7_HEX` is the 7-segment table in ROM. The display must be refreshed by the program.

```
card changes = $1234
byte cur = $AB
DISP2 = hi(changes)
DISP1 = lo(changes)
DISP0 = cur
do
  SCANDS()                ; shows 1234AB
od
```

**Memory:** code starts at `$2000`. Variables go to the zero page `$00-$EE` first, then after the code. `$EF-$FF` belongs to the monitor.

## 21. Building and running

```
tantal build program.tantal
tantal run program.tantal --input PAD=00,01,03
tantal run program.tantal --input PAD=@0:00,@500:01 --max 5000000 --trace
```

`build` writes `.bin`, `.hex`, `.pap` (KIM-1 paper tape for the monitor's `L` command), `.lst` (listing with instructions and cycles per line), `.map` (memory, frames, zero page) and `.sym`.

`run` compiles the program and runs it in the emulator, then prints variables, I/O writes, monitor calls and the TTY output.

- `--input NAME=h1,h2,...` gives hex values for successive reads of a register, or results of a monitor routine; the last value repeats.
- `--input NAME=@cycle:value,...` changes the value at the given cycle.
- `--max N` sets the cycle limit (default 1,000,000 = 1 s at 1 MHz).
- `--trace` writes executed instructions to a `.trace` file.
- `-t NAME` selects another target or a `.target` file; `-o BASE` sets the output name.

## 22. Quick reference

**Keywords:**
`and` `asm` `atomic` `bit` `bits` `byte` `card` `const` `do` `elif` `else` `endasm` `endatomic` `endproc` `exit` `extern` `fi` `for` `if` `interrupt` `irqsafe` `mod` `noinit` `not` `od` `or` `pointer` `proc` `readonly` `return` `ring` `shadow` `step` `string` `then` `to` `until` `wait` `delay` `while` `writeonly`

**Context words:** `file` (table from a file), `change` `from` `rising` `falling` `timeout` `us` `ms` `s` (in `wait`), `cycles` (in `delay`), `irq` `nmi` (in `interrupt`), `exclude` (in `.zp`).

**Built-ins:** `lo` `hi` `byte()` `card()` `sizeof` `len` `pop` `last` `full` `clear` `mem[]`

**Declarations:**

```
byte a, b = 1                      card c = 1000
byte pointer p                     byte[16] buf        byte[] t = 1, 2
string s = "TEXT"                  string[32] line
ring byte[64] r                    const N = 10
const byte[] T = 1, 2, 3           const byte[] F = file "font.bin"
byte R @ $1700 readonly            byte O @ $1702 shadow = 0
byte keep = 1 noinit
bit LED = PBD.0                    bits MODE = PBD.4..6       bit flag = 1
proc f(byte a, card b): card ... endproc
extern proc OUT(byte c @A): byte @A @ $1EA0
interrupt irq proc h() ... endproc
irqsafe proc g() ... endproc
```

**Statements:**

```
x = e      x += e (-= &= |= ^= <<= >>=)      r <- e      f(args)
if c then ... elif c then ... else ... fi
do ... od          do ... until c od          while c do ... od
for i = a to b [step k] do ... od             for i = a until b do ... od
exit       return [e]
wait cond | change X [from e] | rising X.n | falling X.n  [timeout N us|ms|s [then ... fi]]
delay N us|ms|s|cycles                        delay t ms|s
atomic ... endatomic                          asm ... endasm
```

**Not in version 0.1:** `int`/`sbyte` and signed comparisons, `card[n]` arrays, `card pointer`, `card` results from `extern proc`, `test ... endtest` blocks, `.vars`, 65C02 instructions, checking of `.cycles`.
