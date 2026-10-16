// Command tantal: compiler for the Tantal language for the 6502.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jrydval/tantal/compiler"
	"github.com/jrydval/tantal/emu"
)

const usage = `Tantal %s - compiler for the Tantal language for the 6502

Usage:
  tantal build [options] program.tantal    compile the program
  tantal run   [options] program.tantal    compile and run it in the emulator
  tantal version

Options:
  -t, --target NAME      target (default pal2) or a .target file
  -o, --out BASE         base name of the output files (default: from the source)
  --input NAME=h1,h2     (run) values for successive reads of a register or ROM routine, hex;
                         timed: NAME=@cycle:value,@cycle:value
  --max N                (run) cycle limit (default 1000000)
  --trace                (run) write executed instructions to BASE.trace
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
				fail(fmt.Errorf("option %s needs a value", a))
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
				fail(fmt.Errorf("unknown option %s", a))
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
		fail(fmt.Errorf("upload is not implemented yet; send the .pap file from build to the monitor with the L command via a terminal (2400 bps 8N2)"))
	default:
		fmt.Fprintf(os.Stderr, usage, compiler.Version)
		os.Exit(2)
	}
	if len(files) != 1 {
		fail(fmt.Errorf("give exactly one source file"))
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
		fmt.Printf("%s: $%04X-$%04X, code and data %d B, variables up to $%04X\n", src, res.Org, res.Asm.End-1, len(res.Bin), res.VarTop-1)
		fmt.Printf("output: %s.{bin,hex,pap,lst,map,sym}\n", out)
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
		fmt.Printf("\ntrace: %s.trace\n", out)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
