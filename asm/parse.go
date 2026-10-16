package asm

import (
	"fmt"
	"strconv"
	"strings"

	"tantal/cpu"
)

// Line is a parsed line of inline assembler.
type Line struct {
	Label string
	Mnem  string
	Mode  cpu.Mode
	Arg   Operand
	Data  []Operand // for .byte
}

// Resolver maps a name used in assembler to an operand (Tantal variable, local label...).
type Resolver func(name string) (Operand, bool)

// ParseLine parses one line of the form [label:] [MNEM operand] [; comment].
func ParseLine(text string, res Resolver) (*Line, error) {
	if i := strings.IndexByte(text, ';'); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	l := &Line{}
	if text == "" {
		return l, nil
	}
	if i := strings.IndexByte(text, ':'); i > 0 && isIdent(strings.TrimSpace(text[:i])) {
		l.Label = strings.TrimSpace(text[:i])
		text = strings.TrimSpace(text[i+1:])
		if text == "" {
			return l, nil
		}
	}
	fields := strings.SplitN(text, " ", 2)
	mn := strings.ToUpper(strings.TrimSpace(fields[0]))
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(fields[1])
	}
	if mn == ".BYTE" {
		for _, part := range strings.Split(arg, ",") {
			o, err := parseExpr(strings.TrimSpace(part), res)
			if err != nil {
				return nil, err
			}
			l.Data = append(l.Data, o)
		}
		l.Mnem = mn
		return l, nil
	}
	if _, ok := cpu.Lookup[mn]; !ok {
		return nil, fmt.Errorf("unknown instruction %q", fields[0])
	}
	l.Mnem = mn
	compact := strings.ReplaceAll(arg, " ", "")
	up := strings.ToUpper(compact)
	var err error
	switch {
	case compact == "" || up == "A":
		if _, ok := cpu.Lookup[mn][cpu.Acc]; ok {
			l.Mode = cpu.Acc
		} else {
			l.Mode = cpu.Imp
		}
		return l, nil
	case strings.HasPrefix(compact, "#"):
		l.Mode = cpu.Imm
		l.Arg, err = parseExpr(compact[1:], res)
	case strings.HasPrefix(compact, "(") && strings.HasSuffix(up, ",X)"):
		l.Mode = cpu.IndX
		l.Arg, err = parseExpr(compact[1:len(compact)-3], res)
	case strings.HasPrefix(compact, "(") && strings.HasSuffix(up, "),Y"):
		l.Mode = cpu.IndY
		l.Arg, err = parseExpr(compact[1:len(compact)-3], res)
	case strings.HasPrefix(compact, "(") && strings.HasSuffix(compact, ")") && mn == "JMP":
		l.Mode = cpu.Ind
		l.Arg, err = parseExpr(compact[1:len(compact)-1], res)
	case strings.HasSuffix(up, ",X"):
		l.Mode = AutoX
		l.Arg, err = parseExpr(compact[:len(compact)-2], res)
	case strings.HasSuffix(up, ",Y"):
		l.Mode = AutoY
		l.Arg, err = parseExpr(compact[:len(compact)-2], res)
	default:
		if cpu.IsBranch(mn) {
			l.Mode = cpu.Rel
		} else if mn == "JMP" || mn == "JSR" {
			l.Mode = cpu.Abs
		} else {
			l.Mode = Auto
		}
		l.Arg, err = parseExpr(compact, res)
	}
	return l, err
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// parseExpr: [<|>] term {(+|-) term}; at most one symbol.
func parseExpr(s string, res Resolver) (Operand, error) {
	var o Operand
	if s == "" {
		return o, fmt.Errorf("missing operand")
	}
	switch s[0] {
	case '<':
		o.Part = Lo
		s = s[1:]
	case '>':
		o.Part = Hi
		s = s[1:]
	}
	sign := 1
	for len(s) > 0 {
		j := 0
		for j < len(s) && s[j] != '+' && s[j] != '-' {
			if s[j] == '\'' && j+2 < len(s) {
				j += 3
				continue
			}
			j++
		}
		term := s[:j]
		if term == "" {
			return o, fmt.Errorf("invalid expression")
		}
		if v, ok := ParseNumber(term); ok {
			o.Off += sign * v
		} else if isIdent(term) {
			if o.Sym != "" {
				return o, fmt.Errorf("an expression may contain only one symbol")
			}
			r, ok := res(term)
			if !ok {
				return o, fmt.Errorf("unknown symbol %q", term)
			}
			if sign < 0 && r.Sym != "" {
				return o, fmt.Errorf("a symbol cannot be subtracted")
			}
			if r.Sym != "" {
				o.Sym = r.Sym
				o.Off += r.Off
			} else {
				o.Off += sign * r.Off
			}
		} else {
			return o, fmt.Errorf("invalid expression term %q", term)
		}
		s = s[j:]
		if len(s) > 0 {
			if s[0] == '-' {
				sign = -1
			} else {
				sign = 1
			}
			s = s[1:]
		}
	}
	return o, nil
}

// ParseNumber reads $hex, %bin, decimal and 'c'.
func ParseNumber(s string) (int, bool) {
	s = strings.ReplaceAll(s, "_", "")
	if s == "" {
		return 0, false
	}
	if len(s) == 3 && s[0] == '\'' && s[2] == '\'' {
		return int(s[1]), true
	}
	base := 10
	switch s[0] {
	case '$':
		base, s = 16, s[1:]
	case '%':
		base, s = 2, s[1:]
	}
	v, err := strconv.ParseInt(s, base, 32)
	if err != nil {
		return 0, false
	}
	return int(v), true
}
