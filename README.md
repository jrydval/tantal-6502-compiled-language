# Tantal 0.1

Kompilátor jazyka Tantal pro 6502, první verze. Napsaný v Go, bez závislostí, jeden binár pro macOS, Linux i Windows. Obsahuje vlastní assembler a emulátor 6502 s modelem počítače PAL-2.

Specifikace jazyka je v dokumentu *Tantal – jazyk pro 6502: specifikace v0.1*. Tento soubor popisuje, co z ní verze 0.1 umí, co ne a jaká rozhodnutí padla při implementaci.

## Sestavení

```
go build -o tantal ./cmd/tantal
go test ./...
```

Křížový překlad, např. pro Windows: `GOOS=windows GOARCH=amd64 go build -o tantal.exe ./cmd/tantal`.

Přiložené binárky pro macOS nejsou podepsané. Pokud je macOS odmítne spustit, sestavte kompilátor ze zdrojáků příkazem výše, nebo odstraňte karanténu: `xattr -d com.apple.quarantine tantal-darwin-arm64`.

## Použití

```
tantal build program.tantal
tantal run program.tantal --input PAD=00,01,03 --max 5000000
tantal run program.tantal --input PAD=@0:00,@500:01,@800:00
```

`build` vytvoří vedle zdrojáku:

| Soubor | Obsah |
| --- | --- |
| `.bin` | binární obraz od adresy `.org` |
| `.hex` | Intel HEX |
| `.pap` | papírová páska KIM-1 pro nahrání příkazem `L` v monitoru |
| `.lst` | listing: řádek zdrojáku, instrukce, bajty, cykly, součet cyklů za řádek |
| `.map` | mapa paměti: proměnné, rámce procedur (overlay), obsazení zero page |
| `.sym` | tabulka symbolů |

`run` přeloží program a spustí ho v emulátoru. Vypíše hodnoty proměnných, zápisy na I/O, volání rutin monitoru a text poslaný na TTY.

- `--input JMÉNO=h1,h2,…` – hodnoty (hexa) pro postupná čtení registru (`PAD`) nebo návratové hodnoty rutiny monitoru (`GETKEY`). Po vyčerpání se opakuje poslední hodnota.
- `--input JMÉNO=@cyklus:hodnota,…` – hodnota se mění v daném cyklu nezávisle na počtu čtení.
- `--max N` – limit cyklů (výchozí 1 000 000, tedy 1 s při 1 MHz).
- `--trace` – stopa provedených instrukcí do souboru `.trace`.

Porty, pro které nezadáte `--input`, vracejí obsah paměti, tedy obvykle `$00`. Výstup `run` na takové čtení upozorní.

Emulátor neobsahuje ROM monitoru. Každé `JSR` do `$1800–$1FFF` jen zaznamená i s registry a hned se vrátí, skok na `$1C4F` (START) ukončí běh. Časovač uživatelského RIOT (`$1704–$170F`) je emulovaný včetně přerušení; IRQ jde přes vektor `$17FE` jako u KIM-1.

## Příklady

Ve složce `examples`:

| Soubor | Co ukazuje |
| --- | --- |
| `hello.tantal` | smyčka for, procedura s ukazatelem, výstup na TTY |
| `aritmetika.tantal` | byte i card, násobení, dělení, posuny, porovnání |
| `bity.tantal` | bitové aliasy, bitová pole, stínový registr |
| `procedury.tantal` | parametry v registrech i slotech, návratové hodnoty, overlay |
| `retezce.tantal` | řetězce, ukazatele, `mem[]`, const tabulky |
| `analyzator.tantal` | logický analyzátor: `wait change … from … timeout`, ring, displej |
| `preruseni.tantal` | obsluha IRQ od časovače, ring sdílený s přerušením, `atomic` |

Příklady, které čtou port, potřebují v emulátoru zadaný vstup:

```
tantal run examples/analyzator.tantal --max 5000000 \
  --input PAD=@0:00,@500:01,@800:03,@1500:00,@1600:80
tantal run examples/preruseni.tantal --input PAD=@0:00,@3000:05,@6000:07
tantal run examples/bity.tantal --input PAD=C0
```

## Co verze 0.1 umí

- Typy `byte`, `card`, `byte pointer`, pole `byte[n]`, `string`, `ring byte[n]`, konstanty `const`.
- Proměnné na pevné adrese `@`, kvalifikátory `readonly`, `writeonly`, `shadow`, `noinit`.
- Bitové aliasy `bit`, bitová pole `bits`, přímý zápis `PAD.7`, test bitů 7 a 6 instrukcí `BIT`.
- Direktivy `.org`, `.end`, `.zp exclude`, `.include`, `.target`, `.cpu 6502`.
- `if/elif/else`, `do … od`, `do … until … od`, `while`, `for … to/until … step`, `exit`.
- Procedury: první tři bajty parametrů v A/X/Y, další ve slotech rámce, návrat v A nebo A/X. Overlay rámců podle grafu volání, kontrola rekurze, `irqsafe` (kopie procedury pro každý kontext).
- `interrupt irq/nmi proc` včetně předání instrukce BRK monitoru na `$1C00`, `atomic`, inline `asm … endasm` s přístupem k proměnným Tantalu.
- `extern proc` pro rutiny monitoru; `pal2.tantal` s registry obou RIOT a rutinami monitoru se načte automaticky.
- `wait <podmínka>`, `wait change X [from Y]`, `wait rising/falling X.n`, vše i s `timeout N us|ms|s` a proměnnou `timedout`.
- Kruhový buffer: `buf <- x`, `pop`, `buf[i]`, `last`, `len`, `full`, `clear`.
- Násobení, dělení a `mod` (mocniny 2 jako posuny, jinak 16bitová běhová rutina), dělení nulou podle specifikace.
- Inicializace globálních proměnných při každém spuštění; nepoužité proměnné a procedury se do programu nedostanou.
- Varování, když data mění hlavní program i obsluha přerušení mimo `atomic`.

## Co zatím chybí

| Funkce | Stav |
| --- | --- |
| Kontrola `.cycles` | direktiva se přečte, ale nekontroluje; cykly po řádcích jsou v listingu |
| Testovací bloky `test … endtest` (fáze 2) a časované vstupy v testech | zatím jen `tantal run` s `--input` |
| `tantal upload`, `tantal watch` | `.pap` se zatím posílá ručně terminálem (2400 bps 8N2, příkaz `L`) |
| Typy `int`, `sbyte` a porovnání se znaménkem | nepodporováno |
| Pole `card[n]`, `card pointer` | nepodporováno |
| Návratová hodnota `card` z `extern proc` | nepodporováno |
| Přednost proměnných v zero page podle počtu přístupů | proměnné se do zero page dávají v pořadí deklarace |
| Balení příznaků `bit READY` po osmi do bajtu | každý příznak má vlastní bajt |
| `.vars` | nepodporováno; proměnné jdou do zero page a za kód |
| Instrukce 65C02 | nepodporováno (`.cpu 6502`) |

## Rozhodnutí, která specifikace neřešila

Udělal jsem je sám, aby šlo pokračovat; všechna lze změnit.

- **Mez smyčky for** se spočítá jednou na začátku smyčky.
- **Ukazatel jako parametr** leží v rámci procedury; před přístupem `p[i]` se zkopíruje do pomocného ukazatele `__mp` v zero page. Ukazatele deklarované jako proměnné leží vždy v zero page.
- **Pomocné proměnné** `__mp` (ukazatel pro `mem[]` a velká pole) a `__rt` (násobení, dělení) jsou společné. Když je používá hlavní program i obsluha přerušení, kompilátor vypíše varování.
- **Obsluha IRQ** zapne přerušení instrukcí `CLI` na konci startovacího kódu.
- **Shadow registr s počáteční hodnotou** (`byte OUT @ $1702 shadow = $81`) se při startu zapíše do kopie i do registru; bez počáteční hodnoty začíná kopie na 0 a registr se nezapisuje.
- **Proměnná `noinit` s počáteční hodnotou** leží v binárce za kódem (ne v zero page), aby hodnotu nastavilo nahrání programu.
- **Literál `"…"z`** je typu `byte pointer`; běžný literál ve výrazu dává adresu řetězce s délkou.
- **Vstupy emulátoru** se zadávají v šestnáctkové soustavě.

## K ověření na skutečném PAL-2

- **Formát PAP.** Záznam `;LLAAAA<data>CCCC`, kontrolní součet je 16bitový součet bajtů počtu, adresy a dat; poslední záznam `;00NNNNCCCC` s počtem záznamů. Takto je formát KIM-1 popsaný, ale proti monitoru PAL-2 jsem ho nezkoušel.
- **Časování `timeout`** počítá s taktem 1 MHz a s tím, že časovač na `$1704–$1707` počítá takty jako 6530 v KIM-1. V emulátoru limit sedí s přesností na jeden tik.
- **Rozsah `$1750–$177F`** je v targetu rezervovaný, dokud se neověří.

## Ověření

- Emulátor procesoru prošel funkčním testem Klause Dormanna (`6502_functional_test.bin`, včetně dekadického režimu). Test se spustí, když je nastavená proměnná `DORMANN` s cestou k obrazu: `DORMANN=…/6502_functional_test.bin go test ./cpu`.
- `go test ./emu` přeloží všechny příklady a další programy (smyčky na hranici 255/256, ring, wait s hranami a limitem, přerušení, irqsafe, asm, velká pole, chyby překladu, varování) a ověří výsledky v emulátoru.

## Struktura

| Balíček | Obsah |
| --- | --- |
| `cpu` | tabulka instrukcí NMOS 6502 a emulátor procesoru |
| `asm` | assembler s relaxací skoků, inline assembler, listing |
| `lang` | lexer, AST, parser |
| `compiler` | sémantika, generátor kódu, alokace paměti, výstupní formáty, targety |
| `emu` | model PAL-2 pro `tantal run`, testy programů |
| `cmd/tantal` | příkazová řádka |
