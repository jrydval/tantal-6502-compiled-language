// Příkaz tantal: kompilátor jazyka Tantal pro 6502.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tantal/compiler"
	"tantal/emu"
)

const usage = `Tantal %s – kompilátor jazyka Tantal pro 6502

Použití:
  tantal build [volby] program.tantal    přeloží program
  tantal run   [volby] program.tantal    přeloží a spustí v emulátoru
  tantal version

Volby:
  -t, --target JMÉNO     target (výchozí pal2) nebo soubor .target
  -o, --out ZÁKLAD       základ jmen výstupních souborů (výchozí podle zdrojáku)
  --input JMÉNO=h1,h2    (run) hodnoty pro postupná čtení registru nebo rutiny ROM, hexa;
                         časovaně: JMÉNO=@cyklus:hodnota,@cyklus:hodnota
  --max N                (run) limit cyklů (výchozí 1000000)
  --trace                (run) vypíše provedené instrukce do ZÁKLAD.trace
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, compiler.Version)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	target := "pal2"
	out := ""
	var inputs []string
	max := uint64(1000000)
	trace := false
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() string {
			if i+1 >= len(args) {
				fail(fmt.Errorf("volba %s potřebuje hodnotu", a))
			}
			i++
			return args[i]
		}
		switch a {
		case "-t", "--target":
			target = val()
		case "-o", "--out":
			out = val()
		case "--input":
			inputs = append(inputs, val())
		case "--max":
			n, err := strconv.ParseUint(val(), 10, 64)
			if err != nil {
				fail(fmt.Errorf("--max: %v", err))
			}
			max = n
		case "--trace":
			trace = true
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("neznámá volba %s", a))
			}
			files = append(files, a)
		}
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("tantal", compiler.Version)
		return
	case "build", "run":
	case "upload":
		fail(fmt.Errorf("upload zatím není hotový; soubor .pap z příkazu build pošlete do monitoru příkazem L přes terminál (2400 bps 8N2)"))
	default:
		fmt.Fprintf(os.Stderr, usage, compiler.Version)
		os.Exit(2)
	}
	if len(files) != 1 {
		fail(fmt.Errorf("zadejte právě jeden zdrojový soubor"))
	}
	src := files[0]
	res, err := compiler.CompileFile(src, target)
	if err != nil {
		fail(err)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	if out == "" {
		out = strings.TrimSuffix(src, filepath.Ext(src))
	}
	if cmd == "build" {
		files := map[string][]byte{
			".bin": res.Bin, ".hex": []byte(res.Hex), ".pap": []byte(res.Pap),
			".lst": []byte(res.Listing), ".map": []byte(res.Map), ".sym": []byte(res.Sym),
		}
		for ext, data := range files {
			if err := os.WriteFile(out+ext, data, 0o644); err != nil {
				fail(err)
			}
		}
		fmt.Printf("%s: $%04X–$%04X, kód a data %d B, proměnné do $%04X\n", src, res.Org, res.Asm.End-1, len(res.Bin), res.VarTop-1)
		fmt.Printf("výstup: %s.{bin,hex,pap,lst,map,sym}\n", out)
		return
	}
	m := emu.New(res)
	m.Trace = trace
	for _, in := range inputs {
		if err := m.SetInput(in); err != nil {
			fail(err)
		}
	}
	m.Run(max)
	fmt.Print(m.Report())
	if trace {
		if err := os.WriteFile(out+".trace", []byte(strings.Join(m.TraceOut, "\n")+"\n"), 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("\nstopa: %s.trace\n", out)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "chyba:", err)
	os.Exit(1)
}
