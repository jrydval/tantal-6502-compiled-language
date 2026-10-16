package compiler

import (
	"fmt"
	"strings"
)

// IntelHex vytvoří záznamy Intel HEX po 16 bajtech.
func IntelHex(org int, code []byte) string {
	var b strings.Builder
	for i := 0; i < len(code); i += 16 {
		n := len(code) - i
		if n > 16 {
			n = 16
		}
		addr := org + i
		sum := n + (addr >> 8) + (addr & 0xFF)
		fmt.Fprintf(&b, ":%02X%04X00", n, addr)
		for _, x := range code[i : i+n] {
			fmt.Fprintf(&b, "%02X", x)
			sum += int(x)
		}
		fmt.Fprintf(&b, "%02X\r\n", (-sum)&0xFF)
	}
	b.WriteString(":00000001FF\r\n")
	return b.String()
}

// PaperTape vytvoří formát papírové pásky KIM-1 (PAP), který monitor načte příkazem L.
// Záznam: ;LLAAAA<data>CCCC, kde CCCC je 16bitový součet bajtů počtu, adresy a dat.
// Poslední záznam: ;00NNNNCCCC, kde NNNN je počet datových záznamů.
func PaperTape(org int, code []byte) string {
	var b strings.Builder
	records := 0
	for i := 0; i < len(code); i += 24 {
		n := len(code) - i
		if n > 24 {
			n = 24
		}
		addr := org + i
		sum := n + (addr >> 8) + (addr & 0xFF)
		fmt.Fprintf(&b, ";%02X%04X", n, addr)
		for _, x := range code[i : i+n] {
			fmt.Fprintf(&b, "%02X", x)
			sum += int(x)
		}
		fmt.Fprintf(&b, "%04X\r\n", sum&0xFFFF)
		records++
	}
	fmt.Fprintf(&b, ";00%04X%04X\r\n", records, ((records>>8)+(records&0xFF))&0xFFFF)
	return b.String()
}
