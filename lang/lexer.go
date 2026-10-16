// Package lang obsahuje lexer, AST a parser jazyka Tantal.
package lang

import (
	"fmt"
	"strings"
)

// TokKind je druh tokenu.
type TokKind int

const (
	EOF TokKind = iota
	NEWLINE
	IDENT
	KEYWORD
	NUMBER
	STRING
	DIRECTIVE
	OP
	ILLEGAL
)

// Pos je místo ve zdrojáku.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string { return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col) }

// Token je jeden token.
type Token struct {
	Kind  TokKind
	Text  string
	Num   int
	ZTerm bool // řetězec s příponou z
	Pos   Pos
}

var keywords = map[string]bool{
	"byte": true, "card": true, "const": true, "bit": true, "bits": true, "pointer": true,
	"string": true, "ring": true, "proc": true, "endproc": true, "return": true,
	"if": true, "then": true, "elif": true, "else": true, "fi": true,
	"do": true, "od": true, "while": true, "for": true, "to": true, "until": true, "step": true,
	"exit": true, "wait": true, "asm": true, "endasm": true, "atomic": true, "endatomic": true,
	"interrupt": true, "extern": true, "and": true, "or": true, "not": true, "mod": true,
	"noinit": true, "readonly": true, "writeonly": true, "shadow": true, "irqsafe": true,
}

// Lex rozloží zdroják na tokeny.
func Lex(file, src string) ([]Token, error) {
	var toks []Token
	line, col := 1, 1
	i := 0
	lineStart := true
	pos := func() Pos { return Pos{file, line, col} }
	adv := func(n int) {
		for k := 0; k < n; k++ {
			if src[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
			i++
		}
	}
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			toks = append(toks, Token{Kind: NEWLINE, Pos: pos()})
			adv(1)
			lineStart = true
			continue
		case c == ' ' || c == '\t' || c == '\r':
			adv(1)
			continue
		case c == ';':
			for i < len(src) && src[i] != '\n' {
				adv(1)
			}
			continue
		case c == '\\':
			// pokračování řádku: zahodí konec řádku
			p := pos()
			adv(1)
			for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r') {
				adv(1)
			}
			if i < len(src) && src[i] == ';' {
				for i < len(src) && src[i] != '\n' {
					adv(1)
				}
			}
			if i < len(src) && src[i] == '\n' {
				adv(1)
				continue
			}
			return nil, fmt.Errorf("%s: za \\ smí být jen konec řádku", p)
		}
		p := pos()
		start := i
		if c == '.' && lineStart && i+1 < len(src) && isLetter(src[i+1]) {
			adv(1)
			for i < len(src) && (isLetter(src[i]) || isDigit(src[i])) {
				adv(1)
			}
			toks = append(toks, Token{Kind: DIRECTIVE, Text: strings.ToLower(src[start+1 : i]), Pos: p})
			lineStart = false
			continue
		}
		lineStart = false
		switch {
		case isLetter(c):
			for i < len(src) && (isLetter(src[i]) || isDigit(src[i])) {
				adv(1)
			}
			w := src[start:i]
			k := IDENT
			if keywords[w] {
				k = KEYWORD
			}
			toks = append(toks, Token{Kind: k, Text: w, Pos: p})
		case isDigit(c) || c == '$' || c == '%':
			adv(1)
			for i < len(src) && (isHex(src[i]) || src[i] == '_') {
				adv(1)
			}
			txt := src[start:i]
			v, ok := parseNum(txt)
			if !ok {
				if txt == "%" {
					toks = append(toks, Token{Kind: ILLEGAL, Text: txt, Pos: p})
					continue
				}
				return nil, fmt.Errorf("%s: chybné číslo %q", p, txt)
			}
			toks = append(toks, Token{Kind: NUMBER, Text: txt, Num: v, Pos: p})
		case c == '\'':
			if i+2 < len(src) && src[i+2] == '\'' {
				toks = append(toks, Token{Kind: NUMBER, Text: src[i : i+3], Num: int(src[i+1]), Pos: p})
				adv(3)
			} else {
				toks = append(toks, Token{Kind: ILLEGAL, Text: "'", Pos: p})
				adv(1)
			}
		case c == '"':
			adv(1)
			var sb strings.Builder
			for i < len(src) && src[i] != '"' && src[i] != '\n' {
				sb.WriteByte(src[i])
				adv(1)
			}
			if i >= len(src) || src[i] != '"' {
				return nil, fmt.Errorf("%s: neukončený řetězec", p)
			}
			adv(1)
			t := Token{Kind: STRING, Text: sb.String(), Pos: p}
			if i < len(src) && src[i] == 'z' && (i+1 >= len(src) || !isLetter(src[i+1]) && !isDigit(src[i+1])) {
				t.ZTerm = true
				adv(1)
			}
			toks = append(toks, t)
		default:
			ops := []string{"<<=", ">>=", "..", "<-", "<>", "<=", ">=", "<<", ">>", "+=", "-=", "&=", "|=", "^=",
				"+", "-", "*", "/", "&", "|", "^", "~", "=", "<", ">", "(", ")", "[", "]", ",", ":", "@", "."}
			matched := false
			for _, o := range ops {
				if strings.HasPrefix(src[i:], o) {
					toks = append(toks, Token{Kind: OP, Text: o, Pos: p})
					adv(len(o))
					matched = true
					break
				}
			}
			if !matched {
				toks = append(toks, Token{Kind: ILLEGAL, Text: string(c), Pos: p})
				adv(1)
			}
		}
	}
	toks = append(toks, Token{Kind: NEWLINE, Pos: Pos{file, line, col}}, Token{Kind: EOF, Pos: Pos{file, line, col}})
	return toks, nil
}

func isLetter(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isHex(c byte) bool    { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }

func parseNum(s string) (int, bool) {
	t := strings.ReplaceAll(s, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(t, "$"):
		base, t = 16, t[1:]
	case strings.HasPrefix(t, "%"):
		base, t = 2, t[1:]
	}
	if t == "" {
		return 0, false
	}
	v := 0
	for _, r := range t {
		var d int
		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= 'a' && r <= 'f':
			d = int(r-'a') + 10
		case r >= 'A' && r <= 'F':
			d = int(r-'A') + 10
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		v = v*base + d
		if v > 0xFFFFFF {
			return 0, false
		}
	}
	return v, true
}
