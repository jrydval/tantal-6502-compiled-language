# Tantal – jazyk pro 6502: specifikace v0.1

Oct 15, 2026 · @Jan Rydval

## Cíle a principy

Tantal je malý kompilovaný jazyk pro rychlé psaní krátkých programů pro 6502, primárně pro počítač PAL-2. Inspirací je ACTION! pro Atari. Kompilátor běží na PC (macOS, Linux, Windows) a je napsaný v Go.

Principy:

- **Stack jen na návratové adresy.** Parametry a lokální proměnné mají pevné adresy. Paměť se šetří sdílením (overlay) podle grafu volání.
- **Žádná omáčka.** Kód na nejvyšší úrovni souboru je rovnou program. Bez `main`, bez deklarace modulu.
- **I/O jako proměnné.** Registry obvodů i jejich jednotlivé bity mají jména a čtou se a zapisují jako proměnné.
- **Předvídatelný překlad.** Každý konstrukt má známý překlad do 6502. Listing ukazuje kód a počet cyklů ke každému řádku.
- **Jeden binár bez závislostí.** Kompilátor obsahuje vlastní assembler. Hardware cílového stroje popisuje konfigurační soubor, ne kód kompilátoru.

Co jazyk záměrně neřeší: rekurzi, čísla s plovoucí čárkou, dynamickou alokaci paměti, objekty.

## Lexikální struktura

Zdroják je řádkově orientovaný: příkaz končí koncem řádku a bloky uzavírají klíčová slova (`fi`, `od`, `endproc`), jako v ACTION!.

| Prvek | Zápis | Poznámka |
| --- | --- | --- |
| Komentář | `; text` | do konce řádku |
| Desítkové číslo | `123`, `65_535` | `_` je oddělovač pro čitelnost |
| Šestnáctkové číslo | `$FF`, `$17_00` |  |
| Dvojkové číslo | `%1010_0001` |  |
| Znak | `'A'` | hodnota ASCII |
| Řetězec | `"HELLO"` | délka v prvním bajtu; "..."z zakončený nulou |
| Direktiva | `.org $0200` | začíná tečkou, vždy na začátku řádku |
| Pokračování řádku | `\` na konci řádku | pro dlouhé výrazy; řádek končící čárkou pokračuje automaticky |

Jazyk rozlišuje velká a malá písmena. Klíčová slova a zabudované funkce se píší jen malými písmeny (`proc`, `wait`, `len`, `mem`); `PROC` je obyčejný identifikátor. `pad`, `Pad` a `PAD` jsou tři různá jména.

Konvence (nevynucuje se): proměnné a procedury malými písmeny, I/O registry a konstanty velkými (`PAD`, `TIMSTAT`, `MAX`), takže je hned vidět, co sahá na hardware. Kompilátor varuje, když se dvě jména v programu liší jen velikostí písmen.

## Typy a proměnné

Základní typy jsou 8 a 16bitová celá čísla. Výchozí volba je vždy `byte`, protože je pro 6502 nejlevnější.

| Typ | Velikost | Rozsah | Použití |
| --- | --- | --- | --- |
| `byte` | 1 B | 0 až 255 | výchozí typ, I/O registry |
| `sbyte` | 1 B | -128 až 127 |  |
| `card` | 2 B | 0 až 65 535 | čítače, adresy |
| `int` | 2 B | -32 768 až 32 767 |  |
| `byte pointer, card pointer` | 2 B | adresa | ukazatel, vždy v zero page |
| `byte[n]` | n B |  | pole, index 0 až n-1 |

Deklarace:

```
byte count                ; kompilátor zvolí adresu
byte limit = 10           ; s počáteční hodnotou
const MAX = 16            ; konstanta, nezabírá paměť
byte[16] buf              ; pole
byte pointer p = @buf     ; ukazatel do zero page

byte PAD  @ $1700         ; proměnná na pevné adrese (I/O)
card TIMER @ $1704
```

Proměnné s `@` jsou vždy **volatile**. Kompilátor každé jejich čtení i zápis provede skutečně, v pořadí ze zdrojáku, a nikdy je nedrží v registru. U I/O je to nutné: dvě čtení portu za sebou mohou vrátit různé hodnoty.

Volitelné kvalifikátory pro I/O registry (podrobněji v další sekci):

- `readonly`: zápis je chyba kompilace.
- `writeonly`: čtení je chyba kompilace, bitové operace musí jít přes `shadow`.
- `shadow`: zápisy jdou přes kopii v RAM, z registru se nikdy nečte zpět.

### Inicializace a noinit

Globální proměnné se inicializují při každém spuštění programu: s počáteční hodnotou na ni, bez ní na 0. Program spuštěný znovu z monitoru bez nového nahrání se tak chová stejně jako po nahrání. Kvalifikátor `noinit` u konkrétní proměnné to vypne.

```
byte limit = 10                ; při každém spuštění 10
card total                     ; při každém spuštění 0
ring byte[256] log noinit      ; přežije restart i s head a count
byte runs = 0 noinit           ; 0 jen po nahrání, pak počítá spuštění
```

| Zápis | Po nahrání | Při každém spuštění |
| --- | --- | --- |
| `byte x = 5` | 5 | 5 |
| `byte x` | 0 | 0 |
| `byte x = 5 noinit` | 5 | beze změny |
| `byte x noinit` | co je v paměti | beze změny |

- Lokální proměnné procedur `noinit` mít nemohou, protože sdílejí paměť přes overlay; je to chyba kompilace.
- `const` pole se neinicializují nikdy, leží v binárce jen jednou.
- Využití v analyzátoru: zachycená historie v `log noinit` přežije návrat do monitoru a jde si ji prohlédnout nebo vypsat jiným programem.

### Ukazatele a mem

Ukazatel nemá operátor dereference. Používá se jako pole, indexem. Překlad odpovídá přímo adresování `(zp),Y`: `p[i]` je `LDY i` / `LDA (p),Y`.

```
byte pointer p
p = @buf          ; adresa proměnné
p[0] = 5          ; zápis na adresu p
x = p[3]          ; čtení z p+3
p += 1            ; posun ukazatele
```

Vestavěné pole `mem` pokrývá celou paměť, jako PEEK a POKE v BASICu. Hodí se na rychlý přístup k adrese bez deklarace proměnné.

```
mem[$1700] = 1
x = mem[addr + 2]
```

`mem` s konstantní adresou se přeloží na absolutní `LDA`/`STA`, s proměnnou adresou přes dočasný ukazatel v zero page. Přístup přes `mem` je vždy volatile. `Jméno je mem, malými písmeny, jako všechna zabudovaná jména`.

### Pole a tabulky

Pole s počáteční hodnotou se zapisuje přímo výčtem hodnot. Tabulka jen pro čtení má navíc `const`; samostatné klíčové slovo `data` jazyk nemá.

```
byte[] buf = 0, 0, 0, 0                  ; proměnné pole, velikost 4
byte[8] hist = 1, 2                      ; zbytek doplněn nulami
const byte[] SEG7 = $3F, $06, $5B, $4F,  ; řádek končící čárkou pokračuje
                    $66, $6D, $7D, $07
const byte[] FONT = file "font.bin"      ; obsah binárního souboru
```

|  | Proměnné pole | `const` pole |
| --- | --- | --- |
| Zápis | povolen | chyba kompilace |
| Uložení v binárce | dvakrát: originál a pracovní kopie | jednou |
| Při startu programu | startovací kód zkopíruje počáteční hodnoty | nic |
| Typické použití | buffery, čítače | 7seg vzory, převodní tabulky, fonty |

- Velikost se odvodí z počtu hodnot, pokud není uvedena. Víc hodnot než uvedená velikost je chyba.
- Kompilátor si konstantnost neodvozuje sám: nepřímý zápis přes ukazatel (`p = @SEG7`) by jinak tiše změnil chování.
- Do `const` pole nejde vzít ukazatel pro zápis; čtení přes `byte pointer` je povolené.

### Řetězce

Řetězec je pole bajtů s délkou v prvním bajtu, jako v ACTION!. Délka je tak k dispozici jednou instrukcí, řetězec smí obsahovat `$00` a kompilátor snadno hlídá přetečení bufferu.

```
string msg = "HELLO"      ; v paměti: $05 'H' 'E' 'L' 'L' 'O'
string[16] line           ; buffer na 16 znaků (zabere 17 B)

n = len(msg)              ; délka (totéž co msg[0])
c = msg[1]                ; první znak
z = "HELLO"z              ; zakončený nulou, pro cizí rutiny
```

- Maximální délka je 255 znaků, což odpovídá dosahu adresování `(zp),Y`.
- Znaky se indexují od 1, index 0 je délka.
- `string[n]` vyhradí n + 1 bajtů. Zápis delšího řetězce do bufferu je chyba kompilace, pokud je délka známá; jinak se ořízne.
- Literál s příponou `z` se uloží bez délky a s `$00` na konci. Je to `byte pointer`, ne `string`.

### Kruhový buffer

`ring` uchovává posledních N záznamů; nový záznam při zaplnění přepíše nejstarší. Vkládání je příkaz se šipkou buf <- x, ostatní operace včetně výběru jsou zabudované funkce. Tečka za proměnnou zůstává vyhrazená pro bity registrů.

```
ring byte[256] buf

buf <- 10           ; vloží záznam; při zaplnění přepíše nejstarší
a = pop(buf)        ; vybere nejstarší záznam a odebere ho (výraz)
x = buf[0]          ; nejstarší záznam, zůstane v bufferu
x = buf[i]          ; i-tý od nejstaršího, i = 0 … len(buf) - 1
x = last(buf, 0)    ; nejnovější záznam
x = last(buf, 1)    ; předposlední
n = len(buf)        ; počet platných záznamů (0 až N)
if full(buf) then ... fi
clear(buf)

for i = 0 until len(buf) do      ; výpis historie
  PRTBYT(buf[i])
od

if len(buf) > 0 then             ; vybírání z fronty bez čekání
  a = pop(buf)
fi
```

Uložení:

| Část | Velikost | Význam |
| --- | --- | --- |
| `data` | N B | záznamy |
| `head` | 1 B, zero page | kam se zapíše příští záznam |
| `count` | 1 B, u N = 256 2 B | počet platných záznamů; nejstarší leží na `head − count` |

- N musí být mocnina 2 (2 až 256). U 256 se index přetáčí přetečením bajtu, u menších velikostí jedním `AND #maska`. Jiné velikosti by vyžadovaly porovnání a skok a časování by bylo hůř předvídatelné.
- `buf <- x` u N = 256 trvá zhruba 20 cyklů: `LDY head` / `LDA x` / `STA data,Y` / `INC head` a úprava `count`.
- `pop(buf)` nečeká. Z prázdného bufferu `vrátí 0 a emulátor to při tantal run nahlásí`; program si předem ověří `len(buf) > 0`, případně počká explicitně `wait len(buf) > 0`.
- `buf[i]` se přeloží na výpočet `head − count + i` do Y a `LDA data,Y`.
- Index mimo `0 … len(buf) − 1` vrátí nedefinovanou hodnotu; emulátor ho při `tantal run` nahlásí.
- `<-` je platné jen jako samostatný příkaz. Uvnitř výrazu (podmínky) se `<-` čte jako `<` a unární minus; kompilátor u `a <-1` v podmínce vypíše varování, ať autor doplní mezeru.
- pop, `last`, `len`, `full` a `clear` jsou vyhrazená jména; proměnná nebo procedura se stejným jménem je chyba kompilace. `len` funguje i pro řetězce.
- Ring sdílený s obsluhou přerušení: `<-` i `pop` mění `head` a `count`. Operace v hlavním programu musí být v `atomic`; kompilátor to hlídá stejně jako bitové zápisy do sdílených registrů.
- Výrazy se vyhodnocují zleva doprava, takže `pop(buf) - pop(buf)` má definovaný výsledek (první minus druhý nejstarší záznam).

## Bitové aliasy a bitová pole

Jednotlivé bity i skupiny bitů registru dostávají jméno a používají se jako proměnné.

```
byte PAD @ $1700
bit  INTENABLED = PAD.7
bit  LED        = PAD.0
bits MODE       = PAD.4..6     ; 3bitové pole, hodnoty 0 až 7

INTENABLED = 1
LED ^= 1                       ; přepnutí
MODE = 5                       ; posun + maskování
if INTENABLED then ... fi
```

Překlad (zápis jde přes čtení, úpravu a zápis registru):

| Zápis | Kód 6502 | Poznámka |
| --- | --- | --- |
| `LED = 1` | `LDA PAD` / `ORA #$01` / `STA PAD` |  |
| `LED = 0` | `LDA PAD` / `AND #$FE` / `STA PAD` |  |
| `LED ^= 1` | `LDA PAD` / `EOR #$01` / `STA PAD` |  |
| `if INTENABLED` (bit 7) | `BIT PAD` / `BPL dál` | bit 7 jde do příznaku N |
| test bitu 6 | `BIT PAD` / `BVC dál` | bit 6 jde do příznaku V |
| test bitů 0 až 5 | `LDA PAD` / `AND #maska` / `BEQ dál` |  |
| `MODE = 5` | `LDA PAD` / `AND #$8F` / `ORA #$50` / `STA PAD` | konstanta se posune při kompilaci |

Bity 7 a 6 se testují nejrychleji. Rychlé vstupy je proto vhodné zapojit na ně.

### Stínový registr

U registru, kde čtení nevrací zapsanou hodnotu (registr jen pro zápis, čtení stavu pinů místo výstupního latche), se zápisy vedou přes kopii v RAM:

```
byte PBD @ $1702 shadow
bit  MOTOR = PBD.3
MOTOR = 1        ; LDA shPBD / ORA #$08 / STA shPBD / STA PBD
```

Kopie se při startu programu inicializuje na hodnotu z target souboru nebo z `shadow = $00`.

### Bitové proměnné v RAM

`bit READY` bez `=` je příznak v RAM. Kompilátor sbalí až 8 příznaků do jednoho bajtu, nejlépe v zero page.

## Direktivy

Direktivy určují, kam program patří a čeho se nesmí dotknout. Výchozí hodnoty bere kompilátor z target souboru, direktiva je přepíše.

| Direktiva | Význam | Výchozí |
| --- | --- | --- |
| `.target pal2` | profil stroje (paměť, I/O, vektory) | z příkazové řádky |
| `.cpu 6502` / `.cpu 65c02` | povolená instrukční sada | z targetu |
| `.org $0200` | začátek kódu | z targetu |
| `.vars $0300` | začátek proměnných | hned za kódem |
| `.end $03FF` | horní limit; překročení je chyba | konec RAM z targetu |
| `.zp exclude $F0-$FF` | rozsah zero page, kam kompilátor nic nedá; může se opakovat | z targetu |
| `.include "lcd.tantal"` | vložení dalšího souboru |  |
| `.cycles <= 24` | hlídá nejhorší počet cyklů následujícího příkazu (smyčka = jedna iterace); bez limitu jen vypíše počet; viz Silné stránky, bod 2 | nic se nehlídá |

Pravidla pro zero page:

- Explicitní `@` adresa ve vyloučeném rozsahu je povolená, jen s varováním. Někdy je záměr číst proměnné monitoru.
- Proměnné jdou nejdřív do zero page, po jejím vyčerpání za kód. O místo v zero page mají přednost proměnné s nejvíce přístupy ve zdrojáku, ve smyčkách váženě víc.
- Ukazatele musí být v zero page kvůli adresování `(zp),Y`. Pokud se nevejdou, je to chyba.
- Stránka $0100 až $01FF je hardwarový stack. Kompilátor ji nikdy nepoužije pro kód ani proměnné.

Překročení `.end` hlásí kompilátor přesně: o kolik bajtů a které procedury jsou největší.

## Řídicí struktury a výrazy

Struktury vycházejí z ACTION!: smyčky jsou `do ... od`, podmínky `if ... fi`.

```
if x > 10 then
  ...
elif x = 0 then
  ...
else
  ...
fi

do                      ; nekonečná smyčka
  ...
  if done then exit fi  ; exit = break
od

while n > 0 do ... od
for i = 0 to 15 do ... od      ; volitelně step
do ... until ready od          ; podmínka na konci

wait PAD.7 = 1                 ; čekání v těsné smyčce
```

`=` je přiřazení v příkazu a porovnání ve výrazu. Přiřazení uvnitř výrazu neexistuje, takže to není dvojznačné.

### Smyčka for

```
for i = 0 to 255 do ... od           ; včetně horní meze
for i = 0 until len(buf) do ... od   ; bez horní meze: i = 0 … len - 1
for i = 0 to 100 step 10 do ... od
```

- `to` zahrnuje horní mez, `until` ji vylučuje. `until` je správná volba pro procházení pole nebo bufferu: při délce 0 neproběhne ani jednou, kdežto `to len(buf) - 1` by po podtečení proběhlo 256krát.
- Konec se testuje **před** zvýšením čítače (porovnání s mezí). Smyčka tak funguje i s horní mezí 255 a `byte` čítačem, který by po zvýšení přetekl na 0.
- Před prvním průchodem se kontroluje prázdný rozsah (`start > end` u `to`, `start >= end` u `until`); v tom případě tělo neproběhne.
- `until` porovnává mez jako 16bitovou hodnotu, takže zvládne i `until 256` s `byte` čítačem.
- `step` testuje, jestli zbývá na další krok (`end − i < step`), takže čítač nikdy nepřeteče.
- Kompilátor varuje, když mez může přesáhnout rozsah čítače (např. `card` mez a `byte` čítač u `to`).

Překlad pro obecnou mez:

```
loop: ...tělo...
      LDA i
      CMP end
      BEQ done       ; i = end, poslední průchod hotový
      INC i
      BNE loop       ; vždy skočí: i < end <= 255, takže i + 1 není 0
done:
```

S konstantní mezí 255 se to zkrátí na `INC i` / `BNE loop`: přetečení na 0 nastaví příznak Z zadarmo.

Operátory podle priority (od nejvyšší):

| Úroveň | Operátory | Poznámka |
| --- | --- | --- |
| 1 | `-x`, `~x`, `not x`, `@x` (adresa), `p`\[i\] (přístup přes ukazatel) | unární |
| 2 | `*`, `/`, `mod` | softwarová rutina, kromě násobení a dělení mocninou 2 |
| 3 | `+`, `-` |  |
| 4 | `<<`, `>>` |  |
| 5 | `&` | bitové AND |
| 6 | `^`, `\|` | bitové XOR, OR |
| 7 | `=`, `<>`, `<`, `<=`, `>`, `>=` |  |
| 8 | `and` | logické, zkrácené vyhodnocení |
| 9 | `or` | logické, zkrácené vyhodnocení |

Pravidla typů:

- `byte op byte` dává `byte` a přetéká (255 + 1 = 0).
- Kombinace s 16bitovým typem dává 16bitový výsledek.
- Porovnání `byte` a `card` je bez znaménka, `sbyte` a `int` se znaménkem. Míchání znaménkových a neznaménkových typů v porovnání je chyba; je potřeba explicitní převod `card(x)`, `int(x)`.
- `lo(x)`, `hi(x)` vrátí dolní a horní bajt 16bitové hodnoty.
- Násobení, dělení a `mod` se řídí stejným pravidlem: `byte * byte` dává `byte` a přetéká. Pro 16bitový součin je potřeba převést jeden operand: `card(a) * b`.
- Přiřazení `byte * byte` do 16bitové proměnné (`card c = a * b`) vyvolá varování, protože jde téměř jistě o chybu.
- Dělení nulou má definovaný výsledek: `x / 0` je samé jedničky (`$FF`, `$FFFF`), `x mod 0` je `x`. Dělení se znaménkem zaokrouhluje k nule.

## Procedury a volací konvence

Parametry ani lokální proměnné nejdou přes stack. Hardwarový stack nese jen návratové adresy (2 B na úroveň volání) a uložené registry v přerušení.

```
proc show(byte v, card n)
  byte tmp              ; lokální proměnná, pevná adresa
  ...
endproc

proc sum(byte a, byte b): byte
  return a + b
endproc

show(cur, count)
x = sum(3, 4)
```

Volací konvence:

| Co | Kde | Poznámka |
| --- | --- | --- |
| 1. až 3. bajt parametrů | A, X, Y | v pořadí bajtů; `card` zabere dva registry |
| další parametry | pevné sloty volané procedury | volající zapisuje rovnou `STA slot` |
| návratová hodnota `byte` | A |  |
| návratová hodnota `card` / `int` | A (dolní), X (horní) |  |
| lokální proměnné | pevné adresy | sdílené podle grafu volání |

Volaná procedura si A/X/Y na začátku uloží do svých slotů jen tehdy, když je později potřebuje.

### Overlay alokace

Kompilátor sestaví graf volání. Dvě procedury, které nemohou být aktivní současně (žádná nevolá druhou, ani nepřímo), sdílejí stejnou paměť pro lokály a parametry. Spotřeba RAM tak odpovídá nejhlubší větvi volání, ne součtu všech procedur.

Příklad: hlavní program volá `measure` a `show`, `show` volá `digit`. Lokály `measure` a `show` mohou ležet na stejných adresách; `digit` musí ležet za lokály `show`.

Omezení plynoucí z overlay:

- **Rekurze** (přímá i nepřímá) je chyba kompilace s výpisem cyklu v grafu.
- **Procedura volaná z hlavního programu i z přerušení** je chyba (není reentrantní), pokud není označená irqsafe, viz níže.
- **Volání přes ukazatel na proceduru** graf nezná. Procedury, jejichž adresa se kdekoli bere (`@proc`), dostanou vlastní nesdílenou paměť.

### irqsafe proc

Procedura, kterou je potřeba volat z hlavního programu i z obsluhy přerušení, se označí `irqsafe`:

```
irqsafe proc hex2seg(byte v): byte
  return SEG7_HEX[v & $0F]
endproc
```

- Kompilátor vytvoří kopii procedury pro každý kontext, ze kterého je volaná: hlavní program, IRQ, NMI. Každá kopie má vlastní parametry a lokální proměnné, takže si je kontexty nepřepíšou.
- Volání se automaticky směruje na kopii daného kontextu.
- Cenou je kód a lokály navíc za každý kontext. Mapa paměti kopie ukáže.
- Procedury volané z `irqsafe` procedury musí být také `irqsafe`, jinak je to chyba kompilace.
- Globální proměnné, které procedura mění, tím chráněné nejsou; na ty platí `atomic`.

## Přerušení, atomic a inline asm

### Přerušení

```
interrupt irq proc onTick()
  ticks += 1
endproc
```

Kompilátor na začátek vloží uložení A, X, Y na stack a na konec jejich obnovení a `RTI`. Adresu obsluhy zapíše do vektoru, jehož umístění určuje target soubor. U KIM-1 jsou vektory v RAM; přesné adresy pro PAL-2 je potřeba ověřit.

Lokály obsluhy přerušení mají vlastní oblast paměti, která se nikdy nesdílí s ničím volaným z hlavního programu.

### Atomic

```
atomic
  MOTOR = 1
  count = 0
endatomic
```

Překlad: `PHP` / `SEI` … `PLP`. Po bloku se obnoví původní stav příznaku I, takže `atomic` jde bezpečně vnořit.

### Inline asm

```
asm
  LDA PAD          ; jména proměnných Tantalu jsou symboly assembleru
  STA last
loop:
  CMP PAD
  BEQ loop
endasm
```

Návěští uvnitř `asm` jsou lokální pro blok. Kompilátor předpokládá, že blok změní A, X, Y i příznaky.

### Procedury z ROM monitoru

```
extern proc OUTCH(byte c @A) @ $1EA0   ; znak na TTY, další v pal2.tantal
OUTCH('A')
```

`@A`, `@X`, `@Y` u parametru určuje registr. Tak jde volat rutiny monitoru bez obalového kódu.

## Výstup kompilátoru a nástroje

Kompilátor `tantal` je jeden statický binár v Go, přeložený pro macOS, Linux a Windows. Nepotřebuje externí assembler.

| Soubor | Obsah |
| --- | --- |
| `prog.bin` | binární obraz od adresy `.org` |
| `prog.hex` | Intel HEX; navíc prog.pap ve formátu papírové pásky KIM-1 |
| `prog.lst` | listing: řádek zdrojáku, vygenerované instrukce, bajty, cykly |
| `prog.map` | mapa paměti: kód, proměnné, overlay oblasti, obsazení zero page |
| `prog.sym` | tabulka symbolů pro monitor nebo emulátor |

Příkazy:

- `tantal build prog.tantal`: překlad.
- `tantal run prog.tantal`: překlad a spuštění ve vestavěném emulátoru 6502.
- `tantal upload prog.tantal`: překlad a nahrání do PAL-2 přes sériovou linku 2400 bps (8N2) ve formátu papírové pásky (PAP), který monitor načte příkazem L.

### Target soubor

Hardware stroje popisuje textový soubor, ne kompilátor. PAL-2 je jen jeden z profilů.

### Paměťová mapa PAL-2

PAL-2 má mapu jako KIM-1, ale RAM (čip 128 KB, A16 na zemi, tedy celých 64 KB adresového prostoru) je osazená v rozsahu `$0200` až `$FFF7` mimo díry pro RIOT čipy a ROM monitoru. Zdroj: KIM-1 Quick Reference (Jeff Tranter) a údaje o PAL-2 od Jana.

| Rozsah | Obsah | Použití v Tantalu |
| --- | --- | --- |
| `$0000–$00EE` | RAM, zero page | proměnné, ukazatele |
| `$00EF–$00FF` | zero page monitoru (kontext ST/GO, buffer displeje `$F9–$FB`) | vyloučeno; přístup jen přes `@` |
| `$0100–$01FF` | stack | nepoužívá se |
| `$0200–$16FF` | RAM (5 376 B) | volné pro loader a uživatele; kompilátor sem nic nedává |
| `$1700–$174F` | RIOT: porty A/B, směrové registry, časovače | I/O registry |
| `$1750–$177F` | neznámé; pravděpodobně mezera nebo zrcadlo RAM či registrů RIOT | rezervováno; kompilátor sem nic nedá, @ adresa sem vyvolá varování |
| `$1780–$17E6` | RAM v RIOT (103 B) | volitelně proměnné |
| `$17E7–$17FF` | RAM monitoru: vektory NMI (`$17FA`) a IRQ (`$17FE`), parametry pásky | jen vektory |
| `$1800–$1FFF` | ROM monitoru (2 KB) | volání rutin |
| `$2000–$FFF7` | RAM (57 336 B) | kód, za ním proměnné |
| `$FFF8–$FFFF` | vektory RESET, NMI, IRQ | nepoužívá se |

Z toho plyne výchozí rozvržení: **proměnné nejdřív do zero page**, **kód od `$2000`** (souvislých 56 KB) a **zbylé proměnné hned za kódem**. Oblast `$0200–$16FF` zůstává volná, protože tam často bývá loader. Díra `$1700–$1FFF` nikdy neleží uprostřed kódu ani proměnných.

### Target soubor pro PAL-2

```
# pal2.target
cpu        = 6502              ; NMOS, zatím bez 65C02
clock      = 1000000           ; Hz, 1 MHz
timeout.timer = $1704          ; časovač pro wait ... timeout
ram        = $0000-$16FF, $2000-$FFF7
org        = $2000
vars       = after-code        ; po vyčerpání zero page
reserve    = $0200-$16FF       ; loader, uživatel
reserve    = $1750-$177F       ; neověřeno, mezera nebo zrcadlo RIOT
zp.free    = $00-$EE
vector.irq = $17FE
vector.nmi = $17FA
exit       = $1C4F             ; START, návrat do monitoru
include    = pal2.tantal
```

### Předdefinované registry (pal2.tantal)

Soubor obsahuje oba RIOT čipy: uživatelský na $1700 a systémový na $1740, přes který jde psát vlastní obsluhu displeje s jinými znaky než hexa. Časovače RIOT mají na stejné adrese jiný význam pro čtení a pro zápis a čtení má vedlejší účinek (např. čtení `$1706` vypne přerušení časovače). Proto má jedna adresa dvě jména: jedno `readonly`, druhé `writeonly`.

```
; uživatelský RIOT, porty na aplikačním konektoru
byte PAD   @ $1700
byte PADD  @ $1701
byte PBD   @ $1702
byte PBDD  @ $1703

; systémový RIOT: displej, klávesnice (používá ho i monitor)
byte SYS_PA   @ $1740
byte SYS_PADD @ $1741
byte SYS_PB   @ $1742
byte SYS_PBDD @ $1743
; časovač systémového RIOT ($1744–$174F) má stejnou strukturu jako u $1700;
; zda je volný pro program, je potřeba ověřit

byte[] SEG7_HEX @ $1FE7   ; tabulka 7seg vzorů číslic 0–F v ROM (TABLE)

; časovač: zápis spustí odpočet s daným dělitelem
byte TIM1     @ $1704 writeonly    ; 1 µs, bez přerušení
byte TIM8     @ $1705 writeonly
byte TIM64    @ $1706 writeonly
byte TIM1024  @ $1707 writeonly
byte TIM1I    @ $170C writeonly    ; 1 µs, s přerušením
byte TIM8I    @ $170D writeonly
byte TIM64I   @ $170E writeonly
byte TIM1024I @ $170F writeonly

; časovač: čtení
byte TIMCNT   @ $1706 readonly     ; stav čítače, vypne přerušení
byte TIMCNTI  @ $170E readonly     ; stav čítače, zapne přerušení
byte TIMSTAT  @ $1707 readonly
bit  TIMEOUT  = TIMSTAT.7          ; 1 = odpočet doběhl (test přes BIT)

; rutiny monitoru
extern proc OUTCH(byte c @A)   @ $1EA0   ; znak na TTY
extern proc PRTBYT(byte b @A)  @ $1E3B   ; bajt hexa na TTY
extern proc CRLF()             @ $1E2F
extern proc GETCH(): byte @A   @ $1E5A   ; znak z TTY
extern proc SCANDS()           @ $1F1F   ; displej z $F9–$FB
extern proc GETKEY(): byte @A  @ $1F6A   ; klávesa 0–$15

byte DISP0 @ $F9   ; buffer displeje monitoru
byte DISP1 @ $FA
byte DISP2 @ $FB
```

Vlastní obsluha displeje:

- Zapojení (které bity `SYS_PA` vedou na segmenty, jak `SYS_PB` vybírá pozici) je potřeba doplnit ze schématu PAL-2. Pak dostanou jména jako bitová pole, např. `bits DIGIT = SYS_PB.1..4`.
- Vlastní znaky budou datová tabulka vzorů; pro číslice jde použít `SEG7_HEX` z ROM.
- Displej je multiplexovaný: program musí pozice obnovovat sám dostatečně často, stejně jako při volání `SCANDS`.
- Před dalším použitím klávesnice přes monitor je potřeba zavolat `KEYIN`, protože vlastní rutina mění nastavení portů.

### Start a konec programu

- Monitor při spuštění obnovuje stavový registr z `$00F1`, včetně příznaku D. Startovací kód proto vždy začne `CLD`, jinak by aritmetika běžela v BCD.
- Následuje inicializace globálních proměnných (na počáteční hodnotu, bez ní na 0; kromě noinit) a stínových registrů.
- Konec programu skočí na `START` (`$1C4F`), tedy zpět do monitoru.
- `interrupt irq` přepíše vektor `$17FE`. Ten ale používá i instrukce `BRK` pro návrat do monitoru, takže obsluha musí rozlišit příznak B a `BRK` předat na `$1C00`.

Soubor `pal2.tantal` obsahuje deklarace registrů (`byte PAD @ ...`) a bitů, takže program je může rovnou používat.

## Slabiny současného návrhu

Nejvážnější jsou body 1 až 3: vedou k chybám, které se projeví až na hardwaru a těžko se hledají. Body 1 až 4 a 6 už mají v návrhu výše řešení.

| # | Slabina | Proč vadí | Návrh řešení |
| --- | --- | --- | --- |
| 1 | Zápis bitu je čtení, úprava a zápis registru | U registru jen pro zápis nebo s čtením stavu pinů zapíše nesmysl do ostatních bitů | Kvalifikátory `readonly`, `writeonly`, `shadow`; zápis bitu do `writeonly` bez `shadow` je chyba |
| 2 | Zápis bitu není atomický | Přerušení mezi `LDA` a `STA`, které mění stejný registr, se ztratí | Blok `atomic`; varování, když stejný registr mění hlavní program i obsluha přerušení |
| 3 | Overlay a přerušení | Procedura volaná z hlavního programu i z IRQ si přepíše lokály | `Vyřešeno: bez označení chyba kompilace; irqsafe proc dostane kopii pro každý kontext (viz Procedury`) |
| 4 | Pseudoregistry `A`, `X`, `Y` v běžném kódu | Kompilátor registry sám používá, hodnota „v A“ mezi dvěma příkazy nic neznamená | Registry jen v `asm` a jako `@A` u parametrů `extern proc` |
| 5 | Míchání typů ve výrazech | `byte + byte` přeteče potichu; porovnání se znaménkem a bez je klasický zdroj chyb | `Vyřešeno: výsledek má typ širšího operandu i u násobení; card c = a * b s byte operandy dá varování; porovnání smíšených typů je chyba` |
| 6 | Nerozlišování velkých a malých písmen | Kdyby existovaly pseudoregistry, nešla by proměnná `x`; `pad` a `PAD` je totéž | Vyřešeno: jazyk velikost písmen rozlišuje, klíčová slova jen malými |
| 7 | `^` znamená ukazatel, dereferenci i XOR | Parser to rozliší, čtenář hůř | `Vyřešeno: ukazatel se používá jen indexem p[i], ^ zůstává XOR` |
| 8 | Počáteční hodnoty při opakovaném spuštění | Na KIM-1 se program běžně spouští znovu z monitoru bez nahrání; proměnné mají staré hodnoty | `Vyřešeno: inicializace při každém startu, kvalifikátor noinit u proměnné` |
| 9 | `wait` bez časového limitu | Když signál nepřijde, program visí a displej zhasne | `Vyřešeno: wait ... timeout 100 ms, měřeno časovačem RIOT; výsledek v proměnné timedout (viz Silné stránky, bod 1)` |
| 10 | Časování není vidět ze zdrojáku | Pro logický analyzátor je doba smyčky klíčová; optimalizace ji mohou měnit mezi verzemi kompilátoru | Cykly v listingu; kontrola `.cycles <= 20` (viz Silné stránky) |
| 11 | Násobení, dělení, `mod` | Volají softwarovou rutinu, jsou pomalé a přidávají kód | Listing je označí; konstanty mocniny 2 se přeloží na posuny |
| 12 | Jen jeden blok kódu (`.org`) | RAM PAL-2 přerušuje díra $1700–$1FFF | Vyřešeno rozvržením: kód od $2000 (56 KB souvisle), proměnné v zero page a za kódem; segmenty až bude potřeba víc |
| 13 | Smyčka for s horní mezí 255 | for i = 0 to len(buf) - 1 s byte i se u plného bufferu zacyklí: i přeteče z 255 na 0 | Vyřešeno: konec se testuje před zvýšením čítače, kontrola prázdného rozsahu, tvar until pro procházení polí (viz Řídicí struktury, Smyčka for) |

## Silné stránky k přidání

Nejvíc hodnoty pro logický analyzátor mají první tři body: detekce hran, hlídání časování a testy v emulátoru. Tím se Tantal odliší od obecných jazyků pro 6502.

### 1. Detekce hran a změn

```
wait rising PAD.7                    ; čeká na přechod 0 → 1
wait falling PAD.7                   ; čeká na přechod 1 → 0
cur = wait change PAD                ; změna proti stavu na začátku čekání
cur = wait change PAD from PAD_OLD   ; změna proti PAD_OLD, wait ho aktualizuje
log <- wait rising PAD.7             ; wait je výraz
```

Kompilátor vygeneruje nejkratší možnou smyčku a její délku v cyklech uvede v listingu. Z ní plyne nejkratší zachytitelný puls.

`wait` je výraz: vrátí celý bajt portu přečtený v okamžiku, kdy podmínka nastala. U hran tak program dostane i stav ostatních linek. Použitý jako příkaz hodnotu zahodí. Jazyk nemá žádnou skrytou proměnnou; s čím se porovnává, určuje zápis:

| Zápis | Porovnává s | Změna během zpracování předchozího vzorku | Použití |
| --- | --- | --- | --- |
| `wait change PAD` | hodnotou přečtenou na začátku čekání (dočasná proměnná v overlay) | ztratí se | jednorázové čekání, rychlé pokusy |
| `wait change PAD from PAD_OLD` | proměnnou `PAD_OLD` | zachytí se hned při dalším `wait` | analyzátor, smyčky vzorkování |

S `from` wait po změně do proměnné sám zapíše novou hodnotu. Kdyby to musel dělat program a zapomněl, každé další `wait` by se vrátilo okamžitě. Výchozí hodnotu (např. klidový stav linek) nastaví program před smyčkou.

Překlad s `from` (bez `from` je stejný, jen se na začátku přečte port do dočasné proměnné):

```
loop: LDA PAD
      CMP PAD_OLD
      BEQ loop
      STA PAD_OLD
      STA cur
```

#### Časový limit

```
cur = wait change PAD from PAD_OLD timeout 100 ms
if timedout then
  ...                          ; signál nepřišel
fi

wait rising PAD.7 timeout 500 us
```

- Limit se zadává **časem** (`us`, `ms`, `s`), ne počtem průchodů. Doba průchodu čekací smyčky závisí na podmínce, takže počet průchodů by znamenal jiný čas po každé úpravě kódu.
- Čas měří časovač uživatelského RIOT (v targetu `timeout.timer`). Smyčka jen testuje jeho stavový bit, takže průchod se prodlouží o 6 cyklů (z 10 na 16). Bez `timeout` zůstane smyčka beze změny.
- Kompilátor zvolí dělitel časovače (1, 8, 64 nebo 1024 µs) podle délky limitu. Limity delší než 255 × 1024 µs (zhruba 261 ms) prodlouží softwarový čítač, který se obsluhuje jen při doběhnutí časovače, takže smyčku nezpomalí. Maximum je 60 s. Přesnost je ±1 tik zvoleného dělitele.
- `timedout` je vyhrazená proměnná: 1, když poslední `wait` s limitem vypršel, jinak 0.
- Při vypršení výraz vrátí poslední přečtenou hodnotu portu a proměnná z `from` zůstane beze změny.
- Časové jednotky počítají s taktem z targetu (`clock`). PAL-2 běží na 1 MHz, stejně jako KIM-1.
- Pokud program sám používá stejný časovač a zároveň `wait ... timeout`, je to chyba kompilace.

Překlad (limit do 261 ms):

```
      LDA #ticks
      STA TIM1024
loop: LDA PAD
      CMP PAD_OLD
      BNE done
      BIT TIMSTAT      ; bit 7 = časovač doběhl
      BPL loop
      ; vypršelo: timedout = 1
```

### 2. Hlídání časování při kompilaci

Pragma `.cycles` platí na **následující příkaz**: smyčku, `if`, blok nebo proceduru. Konec se neoznačuje, je to konec toho příkazu.

```
.cycles <= 24            ; jedna iterace smyčky
do
  cur = wait change PAD from PAD_OLD
  log <- cur
od

.cycles <= 120           ; celá procedura
proc show(byte v)
  ...
endproc

.cycles                  ; jen vypíše počet cyklů, nic nehlídá
if ready then ... fi
```

Když příkaz přesáhne limit, je to chyba kompilace s rozpisem cyklů po řádcích. Změna kompilátoru nebo kódu tak nemůže tiše zpomalit vzorkování.

Co se počítá:

- **Nejhorší cesta** tělem, u `if` dražší větev.
- U smyčky **jedna iterace** včetně skoku zpět.
- **Volané procedury** celé, inline `asm` také.
- **Přechod přes hranici stránky** u větvení (cyklus navíc). Kontrola proběhne až po rozmístění kódu, takže je přesná.
- **`wait` uvnitř** jen výstupní cestou, tedy od zachycení změny dál. Délku čekací smyčky (nejkratší zachytitelný puls) kompilátor vypíše zvlášť.
- **Smyčka s neznámým počtem průchodů** uvnitř hlídaného příkazu je chyba, protože ji nelze spočítat.

Párové `.cycles` … `.endcycles` jazyk záměrně nemá: začátek a konec by mohly ležet v různé úrovni zanoření. Libovolný úsek kódu stačí obalit blokem nebo procedurou.

### 3. Běh v emulátoru a testy

Program jde spustit na PC ve vestavěném emulátoru 6502. Je to pohodlí, ne nutnost: nahrání do PAL-2 přes 2400 bps trvá u 1 KB programu zhruba 10 s. Emulátor má smysl hlavně pro ladění logiky a časování. Zavádí se ve třech fázích.

**Fáze 1: `tantal run` bez testovacího jazyka**

```
tantal run prog.tantal --input PAD=00,00,01,03 --max 10000
```

- Emulátor načte stejný binár jako pro PAL-2, s paměťovou mapou z target souboru.
- Každé čtení portu vrátí další hodnotu ze seznamu `--input`.
- Běh skončí po `--max` cyklech nebo návratem do monitoru.
- Na konci vypíše hodnoty proměnných, zápisy na porty a záznam volání ROM.

Rutiny monitoru se nemodelují. Každé `JSR` do ROM (`$1800–$1FFF`) emulátor jen zaznamená i s registry a hned se vrátí:

```
JSR OUTCH  A=$48
JSR SCANDS            ; $F9..$FB = 12 34 56
```

Rutiny, které hodnotu vracejí (`GETCH`, `GETKEY`), ji berou ze seznamu stejně jako porty, např. `--input GETKEY=03,13`.

**Fáze 2: bloky `test ... endtest`**

Testy leží v samostatném souboru (`prog_test.tantal`), takže se nikdy nedostanou do binárky. Blok je vždy na nejvyšší úrovni souboru.

```
test "počítá změny na portu"
  input PAD = $00, $00, $01, $01, $03, $00
  run until count = 3 max 10000
  expect last = $00
  expect rom OUTCH A = 'K'      ; volání rutiny proběhlo s danou hodnotou
endtest
```

`tantal test prog.tantal` vypíše výsledek každého testu, např. `OK  počítá změny na portu  (412 cyklů)`.

**Fáze 3: časované vstupy**

Hodnota portu se mění v daném cyklu bez ohledu na počet čtení. Tak jde ověřit, jestli smyčka zachytí krátký puls.

```
input PAD at 0 = $00, at 150 = $01, at 160 = $00   ; puls 10 cyklů
```

### Další návrhy

| Návrh | Přínos | Priorita |
| --- | --- | --- |
| Kruhový buffer `ring byte[256] log` s `<-, indexem a funkcemi pop, last, len, full, clear;` `viz Typy, Kruhový buffer` | ukládání vzorků; při velikosti 256 se index přetáčí sám, bez maskování | vysoká |
| Datové tabulky `const byte[] SEG7 = $3F, $06, ...` a `file "font.bin" (viz Typy, Pole a tabulky)` | znaky pro displej, převodní tabulky | vysoká |
| `tantal watch`: po uložení souboru překlad a nahrání | nejkratší cyklus úprava → běh | střední |
| Automatické použití X/Y pro `byte` čítače smyček | rychlejší smyčky bez ručního asm | střední |
| Výrazy při kompilaci: `sizeof`, `lo`, `hi`, aritmetika konstant | žádný kód za výpočty známé předem | střední |
| Chybové hlášky s řádkem, sloupcem a ukázkou zdrojáku | rychlejší oprava | střední |
| Výpis zbývající RAM a zero page po každém překladu | přehled, kolik místa zbývá | nízká |

## Otevřené otázky

Hardware PAL-2 (potřeba pro target soubor):

- [x] Paměťová mapa (viz Paměťová mapa PAL-2).
- [x] RAM do `$FFF7`: čip 128 KB s A16 na zemi.
- [x] Zero page monitoru: `$EF–$FF`.
- [x] Vektory IRQ a NMI a rutiny monitoru.
- [x] Uživatelský RIOT je na `$1700`.
- [x] Displej obnovuje program sám voláním rutiny monitoru, kdy a jak často chce. Kompilátor nic nevkládá.
- [x] CPU: zatím jen NMOS 6502.
- [x] Nahrávání: formát PAP přes sériovou linku 2400 bps.
- [x] `Rozsah $1750–$177F: neznámý, pravděpodobně mezera nebo zrcadlo; v targetu rezervovaný, dokud se neověří.`

Jazyk:

- [x] Řetězce: vyřešeno, délka v prvním bajtu, volitelně literál zakončený nulou.
- [x] Velikost písmen: rozlišuje se; klíčová slova jen malými písmeny.
- [x] Ukazatele a XOR: `vyřešeno, ukazatel přes index p[i], ^ je XOR.`
- [x] Název: Tantal, přípona .tantal.
