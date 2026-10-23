package compiler

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrydval/tantal-6502-compiled-language/asm"
)

//go:embed targets/*
var targetFS embed.FS

// Range is a closed address range.
type Range struct{ Lo, Hi int }

// Target describes the target machine.
type Target struct {
	Name         string
	CPU          string
	Clock        int
	RAM          []Range
	Org          int
	Reserve      []Range
	ZPFree       Range
	VectorIRQ    int
	VectorNMI    int
	Exit         int
	Brk          int
	TimeoutTimer int
	TimeoutMode  string // "timer", "loop" or "" (timer when there is one)
	ROM          Range
	IO           Range
	Include      string // file name
	IncludeSrc   string
	Dir          string
}

// LoadTarget loads a built-in target (e.g. "pal2") or a .target file.
func LoadTarget(name string) (*Target, error) {
	var data []byte
	var err error
	dir := ""
	builtin := false
	if strings.HasSuffix(name, ".target") {
		data, err = os.ReadFile(name)
		dir = filepath.Dir(name)
	} else {
		data, err = targetFS.ReadFile("targets/" + name + ".target")
		builtin = true
	}
	if err != nil {
		return nil, fmt.Errorf("target %q not found", name)
	}
	t := &Target{Name: strings.TrimSuffix(filepath.Base(name), ".target"), CPU: "6502", Clock: 1000000, Org: 0x2000,
		ZPFree: Range{0, 0xFF}, Exit: -1, Brk: -1, VectorIRQ: -1, VectorNMI: -1, TimeoutTimer: -1, ROM: Range{-1, -1}, IO: Range{-1, -1}, Dir: dir}
	for n, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexAny(line, ";#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("%s.target:%d: expected key = value", t.Name, n+1)
		}
		k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		num := func(s string) int {
			x, ok := asm.ParseNumber(strings.TrimSpace(s))
			if !ok {
				err = fmt.Errorf("%s.target:%d: invalid number %q", t.Name, n+1, s)
			}
			return x
		}
		rng := func(s string) Range {
			p := strings.SplitN(s, "-", 2)
			if len(p) == 1 {
				x := num(p[0])
				return Range{x, x}
			}
			return Range{num(p[0]), num(p[1])}
		}
		switch k {
		case "cpu":
			t.CPU = v
		case "clock":
			t.Clock = num(v)
		case "ram":
			for _, part := range strings.Split(v, ",") {
				t.RAM = append(t.RAM, rng(part))
			}
		case "org":
			t.Org = num(v)
		case "vars":
			// v0.1: variables always go after the code
		case "reserve":
			t.Reserve = append(t.Reserve, rng(v))
		case "zp.free":
			t.ZPFree = rng(v)
		case "vector.irq":
			t.VectorIRQ = num(v)
		case "vector.nmi":
			t.VectorNMI = num(v)
		case "exit":
			t.Exit = num(v)
		case "brk":
			t.Brk = num(v)
		case "timeout.timer":
			t.TimeoutTimer = num(v)
		case "timeout":
			if v != "timer" && v != "loop" {
				return nil, fmt.Errorf("%s.target:%d: timeout must be timer or loop", t.Name, n+1)
			}
			t.TimeoutMode = v
		case "rom":
			t.ROM = rng(v)
		case "io":
			t.IO = rng(v)
		case "include":
			t.Include = v
		default:
			return nil, fmt.Errorf("%s.target:%d: unknown key %q", t.Name, n+1, k)
		}
		if err != nil {
			return nil, err
		}
	}
	if t.Include != "" {
		var src []byte
		if builtin {
			src, err = targetFS.ReadFile("targets/" + t.Include)
		} else {
			src, err = os.ReadFile(filepath.Join(dir, t.Include))
		}
		if err != nil {
			return nil, fmt.Errorf("file %s referenced by the target not found", t.Include)
		}
		t.IncludeSrc = string(src)
	}
	return t, nil
}

func (t *Target) inRAM(lo, hi int) bool {
	for _, r := range t.RAM {
		if lo >= r.Lo && hi <= r.Hi {
			return true
		}
	}
	return false
}
