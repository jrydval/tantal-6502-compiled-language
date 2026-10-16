// Package compiler překládá Tantal do strojového kódu 6502.
package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tantal/asm"
	"tantal/lang"
)

// Version je verze kompilátoru.
const Version = "0.1.0"

type constVal struct{ V int }

// Compiler drží stav jednoho překladu.
type Compiler struct {
	T         *Target
	Sources   map[string][]string
	Warnings  []string
	globals   map[string]any
	gvars     []*Var
	procs     []*Proc
	mainBody  []lang.Stmt
	org       int
	end       int
	zpExclude []Range
	included  map[string]bool

	p        *asm.Program
	insts    []*Inst
	work     []*Inst
	mainInst *Inst
	strLits  map[string]string
	data     []func()
	lbl      int

	mp, rt, timedOut *Var
	needMul, needDiv bool
	images           []func()
	shared           map[string][]access
	helperCtx        map[string]map[string]bool
	cyclesWarned     bool
}

type bailout struct{ err error }

func (c *Compiler) fail(pos lang.Pos, f string, a ...any) {
	panic(bailout{&lang.Error{Pos: pos, Msg: fmt.Sprintf(f, a...)}})
}

func (c *Compiler) warn(pos lang.Pos, f string, a ...any) {
	c.Warnings = append(c.Warnings, fmt.Sprintf("%s: varování: %s", pos, fmt.Sprintf(f, a...)))
}

// Result je výsledek překladu.
type Result struct {
	Target   *Target
	Asm      *asm.Result
	Org      int
	Bin      []byte
	Listing  string
	Map      string
	Sym      string
	Hex      string
	Pap      string
	Vars     []*Var
	Procs    []*Proc
	Externs  map[int]*Proc
	Labels   map[string]int
	Warnings []string
	VarTop   int
}

// CompileFile přeloží soubor.
func CompileFile(path, target string) (*Result, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Compile(path, string(src), target)
}

// Compile přeloží zdroják.
func Compile(path, src, target string) (res *Result, err error) {
	t, err := LoadTarget(target)
	if err != nil {
		return nil, err
	}
	c := &Compiler{T: t, Sources: map[string][]string{}, globals: map[string]any{}, org: t.Org, end: -1,
		strLits: map[string]string{}, included: map[string]bool{}, shared: map[string][]access{},
		helperCtx: map[string]map[string]bool{}}
	defer func() {
		if r := recover(); r != nil {
			if b, ok := r.(bailout); ok {
				err = b.err
				res = nil
				return
			}
			panic(r)
		}
	}()
	if t.IncludeSrc != "" {
		c.parseFile(t.Include, t.IncludeSrc, true)
	}
	c.parseFile(path, src, false)
	c.collectLocals()
	c.generate()
	return c.finish()
}

func (c *Compiler) parseFile(path, src string, fromTarget bool) {
	c.Sources[path] = strings.Split(src, "\n")
	stmts, warns, err := lang.Parse(path, src)
	if err != nil {
		panic(bailout{err})
	}
	c.Warnings = append(c.Warnings, warns...)
	for _, s := range stmts {
		switch d := s.(type) {
		case *lang.VarDecl:
			c.declareGlobal(d)
		case *lang.ConstDecl:
			c.define(d.Name, d.Pos, &constVal{c.mustConst(d.X)})
		case *lang.BitDecl:
			c.declareBit(d)
		case *lang.ProcDecl:
			c.declareProc(d)
		case *lang.Directive:
			c.directive(d, path)
		default:
			if fromTarget {
				c.fail(s.P(), "soubor targetu smí obsahovat jen deklarace")
			}
			c.mainBody = append(c.mainBody, s)
		}
	}
}

func (c *Compiler) directive(d *lang.Directive, path string) {
	switch d.Name {
	case "org":
		c.org = c.mustConst(d.Args[0])
	case "end":
		c.end = c.mustConst(d.Args[0])
	case "zp":
		lo, hi := c.mustConst(d.Args[0]), c.mustConst(d.Args[1])
		if lo < 0 || hi > 255 || hi < lo {
			c.fail(d.Pos, "rozsah .zp exclude musí ležet v $00–$FF")
		}
		c.zpExclude = append(c.zpExclude, Range{lo, hi})
	case "include":
		file := filepath.Join(filepath.Dir(path), d.Text)
		if c.included[file] {
			return
		}
		c.included[file] = true
		src, err := os.ReadFile(file)
		if err != nil {
			c.fail(d.Pos, "nelze načíst %s", d.Text)
		}
		c.parseFile(file, string(src), false)
	case "target":
		if d.Text != c.T.Name {
			c.fail(d.Pos, "program je pro target %q, překládá se pro %q", d.Text, c.T.Name)
		}
	case "cpu":
		if d.Text != "6502" {
			c.fail(d.Pos, "v0.1 podporuje jen .cpu 6502")
		}
	case "vars":
		c.fail(d.Pos, ".vars zatím není podporováno; proměnné jsou v zero page a za kódem")
	case "cycles":
		c.mainBody = append(c.mainBody, d)
	}
}

func (c *Compiler) define(name string, pos lang.Pos, v any) {
	if _, dup := c.globals[name]; dup {
		c.fail(pos, "jméno %q je už definováno", name)
	}
	if builtinNames[name] {
		c.fail(pos, "jméno %q je vyhrazené", name)
	}
	c.globals[name] = v
}

var builtinNames = map[string]bool{"pop": true, "last": true, "len": true, "full": true, "clear": true,
	"lo": true, "hi": true, "sizeof": true, "mem": true, "timedout": true}

func (c *Compiler) newVar(d *lang.VarDecl, proc *Proc) *Var {
	v := &Var{Name: d.Name, Pos: d.Pos, Elem: TByte, Pointer: d.Type.Pointer, Array: d.Type.Array,
		String: d.Type.String, Ring: d.Type.Ring, Const: d.Const, ReadOnly: d.ReadOnly || d.Const,
		WriteOnly: d.WriteOnly, NoInit: d.NoInit, Proc: proc}
	if d.Type.Base == "card" {
		v.Elem = TCard
	}
	if v.Pointer && v.Elem == TCard {
		c.fail(d.Pos, "v0.1 podporuje jen byte pointer")
	}
	if v.Array && v.Elem == TCard {
		c.fail(d.Pos, "v0.1 podporuje jen pole typu byte")
	}
	if d.Type.LenExpr != nil {
		v.Len = c.mustConst(d.Type.LenExpr)
		if v.Len <= 0 {
			c.fail(d.Pos, "velikost pole musí být kladná")
		}
		if v.String {
			v.Len++
		}
	}
	if d.Addr != nil {
		v.Fixed = true
		v.Addr = c.mustConst(d.Addr)
		if v.Addr < 0 || v.Addr > 0xFFFF {
			c.fail(d.Pos, "adresa mimo rozsah")
		}
		if proc != nil {
			c.fail(d.Pos, "proměnná na pevné adrese musí být globální")
		}
		if v.Addr < 256 {
			for _, r := range c.zpExclude {
				if v.Addr >= r.Lo && v.Addr <= r.Hi {
					c.warn(d.Pos, "%s leží ve vyloučené části zero page", v.Name)
				}
			}
		}
		for _, r := range c.T.Reserve {
			if v.Addr >= r.Lo && v.Addr <= r.Hi && (r.Lo >= 0x1700 && r.Hi < 0x1800) {
				c.warn(d.Pos, "%s leží v rezervovaném rozsahu $%04X–$%04X", v.Name, r.Lo, r.Hi)
			}
		}
	}
	if d.Shadow {
		if !v.Fixed || !v.Scalar() || v.Elem != TByte {
			c.fail(d.Pos, "shadow lze použít jen u byte registru s adresou @")
		}
		v.Shadow = &Var{Name: v.Name + "'shadow", Elem: TByte, Hidden: true, Pos: d.Pos}
	}
	if v.Const && !v.Array {
		c.fail(d.Pos, "const se u proměnné používá pro tabulky; pro konstantu napište const JMÉNO = hodnota")
	}
	// počáteční hodnoty
	switch {
	case d.File != "":
		data, err := os.ReadFile(filepath.Join(filepath.Dir(d.Pos.File), d.File))
		if err != nil {
			c.fail(d.Pos, "nelze načíst %s", d.File)
		}
		for _, b := range data {
			v.Init = append(v.Init, asm.N(int(b)))
		}
		v.HasInit = true
	case v.String && len(d.Init) > 0:
		s, ok := d.Init[0].(*lang.Str)
		if !ok || len(d.Init) > 1 {
			c.fail(d.Pos, "řetězec se inicializuje literálem v uvozovkách")
		}
		if len(s.S) > 255 {
			c.fail(d.Pos, "řetězec je delší než 255 znaků")
		}
		v.Init = append(v.Init, asm.N(len(s.S)))
		for i := 0; i < len(s.S); i++ {
			v.Init = append(v.Init, asm.N(int(s.S[i])))
		}
		v.HasInit = true
	case v.Array && len(d.Init) > 0:
		for _, e := range d.Init {
			x := c.mustConst(e)
			if x < -128 || x > 255 {
				c.fail(e.P(), "hodnota %d se nevejde do bajtu", x)
			}
			v.Init = append(v.Init, asm.N(x&0xFF))
		}
		v.HasInit = true
	case len(d.Init) > 0 && proc == nil:
		op, ok := c.constOp(d.Init[0])
		if !ok {
			c.fail(d.Init[0].P(), "počáteční hodnota globální proměnné musí být konstanta")
		}
		if op.Sym == "" {
			if v.Type() == TByte && (op.Off < -128 || op.Off > 255) {
				c.fail(d.Init[0].P(), "hodnota %d se nevejde do bajtu", op.Off)
			}
			v.Init = append(v.Init, asm.N(op.Off&0xFF))
			if v.Type() == TCard {
				v.Init = append(v.Init, asm.N((op.Off>>8)&0xFF))
			}
		} else {
			if v.Type() != TCard {
				c.fail(d.Init[0].P(), "adresa se vejde jen do card nebo pointer")
			}
			lo, hi := op, op
			lo.Part, hi.Part = asm.Lo, asm.Hi
			v.Init = append(v.Init, lo, hi)
		}
		v.HasInit = true
	}
	if v.Array && !v.Fixed {
		if v.Len == 0 {
			v.Len = len(v.Init)
		}
		if v.Len == 0 {
			c.fail(d.Pos, "pole bez velikosti a bez počátečních hodnot")
		}
		if len(v.Init) > v.Len {
			c.fail(d.Pos, "víc počátečních hodnot (%d) než velikost pole (%d)", len(v.Init), v.Len)
		}
		for len(v.Init) < v.Len && v.HasInit {
			v.Init = append(v.Init, asm.N(0))
		}
	}
	if v.Const && !v.HasInit && !v.Fixed {
		c.fail(d.Pos, "const tabulka potřebuje počáteční hodnoty")
	}
	if v.Shadow != nil && v.HasInit {
		v.Shadow.Init = v.Init
		v.Shadow.HasInit = true
		v.ShadowInit = true
		v.Init, v.HasInit = nil, false
	}
	if v.Fixed && v.HasInit && v.Shadow == nil {
		c.fail(d.Pos, "proměnná na pevné adrese nemůže mít počáteční hodnotu (kromě shadow registru)")
	}
	if v.Ring {
		if proc != nil {
			c.fail(d.Pos, "ring musí být globální")
		}
		if v.Len&(v.Len-1) != 0 || v.Len < 2 || v.Len > 256 {
			c.fail(d.Pos, "velikost ring musí být mocnina 2 (2 až 256)")
		}
		v.Head = &Var{Name: v.Name + "'head", Elem: TByte, Hidden: true, Pos: d.Pos, NoInit: v.NoInit}
		cnt := TByte
		if v.Len == 256 {
			cnt = TCard
		}
		v.Count = &Var{Name: v.Name + "'count", Elem: cnt, Hidden: true, Pos: d.Pos, NoInit: v.NoInit}
	}
	if v.Pointer {
		v.Static = true
	}
	if proc != nil && v.NoInit {
		c.fail(d.Pos, "lokální proměnná nemůže být noinit (sdílí paměť přes overlay)")
	}
	return v
}

func (c *Compiler) addGlobalVar(v *Var) {
	v.Order = len(c.gvars)
	v.Label = "v_" + strings.ReplaceAll(v.Name, "'", "_")
	c.gvars = append(c.gvars, v)
	if v.Shadow != nil {
		c.addGlobalVar(v.Shadow)
	}
	if v.Head != nil {
		c.addGlobalVar(v.Head)
		c.addGlobalVar(v.Count)
	}
}

func (c *Compiler) declareGlobal(d *lang.VarDecl) {
	v := c.newVar(d, nil)
	c.define(v.Name, d.Pos, v)
	if !v.Fixed {
		c.addGlobalVar(v)
	} else if v.Shadow != nil {
		c.addGlobalVar(v.Shadow)
	}
}

func (c *Compiler) declareBit(d *lang.BitDecl) {
	if d.Reg == "" {
		// příznak v RAM: v0.1 jeden bajt na příznak, bit 0
		v := &Var{Name: d.Name + "'flag", Elem: TByte, Pos: d.Pos, Hidden: true}
		if d.Init != nil {
			x := c.mustConst(d.Init)
			v.Init = []asm.Operand{asm.N(x & 1)}
			v.HasInit = true
		}
		c.addGlobalVar(v)
		c.define(d.Name, d.Pos, &Bit{Name: d.Name, Reg: v, Lo: 0, Hi: 0})
		return
	}
	r, ok := c.globals[d.Reg].(*Var)
	if !ok || !r.Scalar() || r.Elem != TByte || r.Pointer {
		c.fail(d.Pos, "%s není byte proměnná nebo registr", d.Reg)
	}
	c.define(d.Name, d.Pos, &Bit{Name: d.Name, Reg: r, Lo: d.Lo, Hi: d.Hi})
}

func (c *Compiler) declareProc(d *lang.ProcDecl) {
	p := &Proc{Name: d.Name, Decl: d, Locals: map[string]*Var{}, Interrupt: d.Interrupt, IrqSafe: d.IrqSafe,
		Insts: map[string]*Inst{}}
	switch d.Ret {
	case "byte":
		p.Ret = TByte
	case "card":
		p.Ret = TCard
	}
	if d.Interrupt != "" && (len(d.Params) > 0 || d.Ret != "") {
		c.fail(d.Pos, "obsluha přerušení nemá parametry ani návratovou hodnotu")
	}
	if d.Extern != nil {
		p.Extern = true
		p.Addr = c.mustConst(d.Extern)
		used := map[string]bool{}
		for _, prm := range d.Params {
			if prm.Reg == "" || !strings.Contains("AXY", prm.Reg) || len(prm.Reg) != 1 {
				c.fail(d.Pos, "parametr %s musí mít registr @A, @X nebo @Y", prm.Name)
			}
			if prm.Type.Base != "byte" || prm.Type.Pointer || prm.Type.Array {
				c.fail(d.Pos, "parametr extern procedury musí být byte")
			}
			if used[prm.Reg] {
				c.fail(d.Pos, "registr %s je použit dvakrát", prm.Reg)
			}
			used[prm.Reg] = true
			p.Params = append(p.Params, &Var{Name: prm.Name, Elem: TByte, Label: prm.Reg})
		}
		if d.RetReg != "" && d.RetReg != "A" {
			c.fail(d.Pos, "návratová hodnota extern procedury musí být v @A")
		}
		if p.Ret == TCard {
			c.fail(d.Pos, "extern procedura zatím vrací jen byte")
		}
	}
	c.define(d.Name, d.Pos, p)
	c.procs = append(c.procs, p)
}

// collectLocals projde těla procedur a přidělí parametrům a lokálům posuny v rámci.
func (c *Compiler) collectLocals() {
	for _, p := range c.procs {
		if p.Extern {
			continue
		}
		off := 0
		add := func(v *Var) {
			if _, dup := p.Locals[v.Name]; dup {
				c.fail(v.Pos, "lokální jméno %q je už definováno", v.Name)
			}
			if builtinNames[v.Name] {
				c.fail(v.Pos, "jméno %q je vyhrazené", v.Name)
			}
			p.Locals[v.Name] = v
			p.LocalList = append(p.LocalList, v)
			if v.Static {
				v.Label = "v_" + p.Name + "_" + v.Name
				c.gvars = append(c.gvars, v)
				return
			}
			v.Off = off
			off += v.Size()
		}
		for _, prm := range p.Decl.Params {
			d := &lang.VarDecl{Pos: p.Decl.Pos, Name: prm.Name, Type: prm.Type}
			if prm.Type.Array || prm.Type.String || prm.Type.Ring {
				c.fail(p.Decl.Pos, "parametr %s: pole se předávají jako byte pointer", prm.Name)
			}
			v := c.newVar(d, p)
			v.Static = false // ukazatel jako parametr leží v rámci, před použitím se kopíruje
			if v.Pointer {
				v.Static = false
			}
			add(v)
			p.Params = append(p.Params, v)
		}
		var walk func(ss []lang.Stmt)
		walk = func(ss []lang.Stmt) {
			for _, s := range ss {
				switch d := s.(type) {
				case *lang.VarDecl:
					v := c.newVar(d, p)
					if v.HasInit && (v.Array || v.String) {
						c.fail(d.Pos, "lokální pole nemůže mít počáteční hodnoty; použijte globální const tabulku")
					}
					add(v)
				case *lang.ConstDecl, *lang.BitDecl, *lang.ProcDecl:
					c.fail(s.P(), "uvnitř procedury jsou povolené jen deklarace proměnných")
				case *lang.If:
					for _, b := range d.Blocks {
						walk(b)
					}
					walk(d.Else)
				case *lang.Loop:
					walk(d.Body)
				case *lang.While:
					walk(d.Body)
				case *lang.For:
					walk(d.Body)
				case *lang.Atomic:
					walk(d.Body)
				}
			}
		}
		walk(p.Decl.Body)
		p.LocalSize = off
	}
}

func (c *Compiler) newLabel(prefix string) string {
	c.lbl++
	return fmt.Sprintf("_%s%d", prefix, c.lbl)
}

// ---- alokace a výstup ----

type zpPool struct{ free [256]bool }

func (z *zpPool) take(size int) (int, bool) {
	for a := 0; a+size <= 256; a++ {
		ok := true
		for i := 0; i < size; i++ {
			if !z.free[a+i] {
				ok = false
				break
			}
		}
		if ok {
			for i := 0; i < size; i++ {
				z.free[a+i] = false
			}
			return a, true
		}
	}
	return 0, false
}

func (c *Compiler) layoutFrames() map[string]int {
	// overlay: báze instance = max(báze volajícího + velikost volajícího) v rámci jednoho kořene
	roots := []*Inst{}
	for _, in := range c.insts {
		if in.Main || in.Proc != nil && in.Proc.Interrupt != "" {
			roots = append(roots, in)
		}
		in.Size = in.Temps
		if in.Proc != nil {
			in.Size += in.Proc.LocalSize
		}
	}
	// topologické pořadí (rekurze už je vyloučená)
	order := []*Inst{}
	seen := map[*Inst]bool{}
	var visit func(in *Inst)
	visit = func(in *Inst) {
		if seen[in] {
			return
		}
		seen[in] = true
		for _, cal := range sortedInsts(in.Calls) {
			visit(cal)
		}
		order = append(order, in)
	}
	for _, r := range roots {
		visit(r)
	}
	regionBase := map[*Inst]int{}
	total := 0
	for _, r := range roots {
		// kořen a jeho strom
		tree := map[*Inst]bool{}
		var mark func(in *Inst)
		mark = func(in *Inst) {
			if tree[in] {
				return
			}
			tree[in] = true
			for cal := range in.Calls {
				mark(cal)
			}
		}
		mark(r)
		ext := 0
		base := map[*Inst]int{}
		// procházet v obráceném topologickém pořadí (volající před volanými)
		for i := len(order) - 1; i >= 0; i-- {
			in := order[i]
			if !tree[in] {
				continue
			}
			b := base[in]
			for cal := range in.Calls {
				if b+in.Size > base[cal] {
					base[cal] = b + in.Size
				}
			}
			if b+in.Size > ext {
				ext = b + in.Size
			}
		}
		for in := range tree {
			in.Base = total + base[in]
			regionBase[in] = total
		}
		total += ext
	}
	m := map[string]int{"total": total}
	return m
}

func sortedInsts(m map[*Inst]bool) []*Inst {
	var out []*Inst
	for in := range m {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (c *Compiler) checkRecursion() {
	state := map[*Inst]int{}
	var stack []*Inst
	var dfs func(in *Inst)
	dfs = func(in *Inst) {
		state[in] = 1
		stack = append(stack, in)
		for _, cal := range sortedInsts(in.Calls) {
			if state[cal] == 1 {
				var names []string
				start := 0
				for i, s := range stack {
					if s == cal {
						start = i
					}
				}
				for _, s := range stack[start:] {
					names = append(names, s.Proc.Name)
				}
				names = append(names, cal.Proc.Name)
				c.fail(cal.Proc.Decl.Pos, "rekurze není povolená: %s", strings.Join(names, " → "))
			}
			if state[cal] == 0 {
				dfs(cal)
			}
		}
		stack = stack[:len(stack)-1]
		state[in] = 2
	}
	for _, in := range c.insts {
		if state[in] == 0 {
			dfs(in)
		}
	}
}

type access struct {
	ctx    string
	atomic bool
	pos    lang.Pos
}

// sharedWarnings upozorní na neatomické úpravy dat sdílených s obsluhou přerušení.
func (c *Compiler) sharedWarnings() {
	keys := make([]string, 0, len(c.shared))
	for k := range c.shared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		acc := c.shared[k]
		ctxs := map[string]bool{}
		for _, a := range acc {
			ctxs[a.ctx] = true
		}
		if len(ctxs) < 2 {
			continue
		}
		for _, a := range acc {
			if !a.atomic {
				c.warn(a.pos, "%s mění hlavní program i obsluha přerušení; obalte úpravu blokem atomic", k)
			}
		}
	}
	for _, name := range []string{"__mp", "__rt"} {
		if len(c.helperCtx[name]) > 1 {
			what := map[string]string{"__mp": "mem[], ukazatele z parametrů a velká pole", "__rt": "násobení a dělení"}[name]
			c.Warnings = append(c.Warnings, fmt.Sprintf("varování: %s se používá v hlavním programu i v obsluze přerušení; pomocné proměnné nejsou chráněné (v0.1)", what))
		}
	}
}

func (c *Compiler) finish() (*Result, error) {
	c.checkRecursion()
	c.sharedWarnings()
	frames := c.layoutFrames()
	total := frames["total"]

	pool := &zpPool{}
	for a := c.T.ZPFree.Lo; a <= c.T.ZPFree.Hi && a < 256; a++ {
		pool.free[a] = true
	}
	for _, r := range c.zpExclude {
		for a := r.Lo; a <= r.Hi; a++ {
			pool.free[a] = false
		}
	}
	// pevné proměnné v zero page
	for _, g := range c.globals {
		if v, ok := g.(*Var); ok && v.Fixed && v.Addr < 256 {
			for i := 0; i < v.Size() && v.Addr+i < 256; i++ {
				pool.free[v.Addr+i] = false
			}
		}
	}
	p := c.p
	var after []*Var
	place := func(v *Var, mustZP bool) {
		if v.NoInit && v.HasInit {
			after = append(after, v) // musí být v binárce
			return
		}
		if a, ok := pool.take(v.Size()); ok {
			v.InZP = true
			v.Final = a
			p.Equ[v.Label] = a
			return
		}
		if mustZP {
			c.fail(v.Pos, "%s musí ležet v zero page, ale zero page je plná", v.Name)
		}
		after = append(after, v)
	}
	used := c.usedVars()
	// 1. ukazatele a pomocné ukazatele
	for _, v := range c.gvars {
		if v.Pointer && used[v] && v != c.mp {
			place(v, true)
		}
	}
	if c.mp != nil {
		place(c.mp, true)
	}
	if c.rt != nil {
		place(c.rt, false)
	}
	// 2. rámce procedur
	framesInZP := false
	if total > 0 {
		if a, ok := pool.take(total); ok {
			p.Equ["__frames"] = a
			framesInZP = true
		}
	}
	// 3. skaláry
	for _, v := range c.gvars {
		if !v.Pointer && v.Scalar() && used[v] && v != c.mp && v != c.rt {
			place(v, false)
		}
	}
	// 4. zbytek
	for _, v := range c.gvars {
		if !v.Scalar() && used[v] && !v.InZP && !v.Const && v != c.rt && v != c.mp {
			after = append(after, v)
		}
	}
	for _, in := range c.insts {
		p.Alias[in.FrameSym] = asm.S("__frames", in.Base)
	}
	// datová sekce: data proměnných noinit s počáteční hodnotou
	for _, v := range after {
		if v.NoInit && v.HasInit {
			p.Label(v.Label)
			p.Bytes(v.Init...)
		}
	}
	// vyhrazené místo za kódem
	for _, v := range after {
		if !(v.NoInit && v.HasInit) {
			p.Reserve(v.Label, v.Size())
		}
	}
	if total > 0 && !framesInZP {
		p.Reserve("__frames", total)
	}
	res, err := p.Assemble()
	if err != nil {
		return nil, err
	}
	for _, v := range c.gvars {
		if !v.InZP {
			if a, ok := res.Labels[v.Label]; ok {
				v.Final = a
			}
		}
	}
	// kontroly paměti
	limit := c.end
	if limit < 0 {
		limit = 0xFFF7
		for _, r := range c.T.RAM {
			if c.org >= r.Lo && c.org <= r.Hi {
				limit = r.Hi
			}
		}
	}
	if res.Top-1 > limit {
		return nil, c.overflowError(res, res.Top-1-limit, limit)
	}
	if !c.T.inRAM(c.org, res.Top-1) && len(c.T.RAM) > 0 && res.Top > c.org {
		return nil, fmt.Errorf("program ($%04X–$%04X) nepadne celý do RAM targetu %s", c.org, res.Top-1, c.T.Name)
	}
	for _, r := range c.T.Reserve {
		if c.org <= r.Hi && res.Top-1 >= r.Lo {
			return nil, fmt.Errorf("program ($%04X–$%04X) zasahuje do rezervovaného rozsahu $%04X–$%04X", c.org, res.Top-1, r.Lo, r.Hi)
		}
	}
	out := &Result{Target: c.T, Asm: res, Org: c.org, Bin: res.Code, Warnings: c.Warnings, Labels: res.Labels,
		Procs: c.procs, Externs: map[int]*Proc{}, VarTop: res.Top}
	for _, v := range c.gvars {
		if used[v] {
			out.Vars = append(out.Vars, v)
		}
	}
	for _, g := range c.globals {
		if v, ok := g.(*Var); ok && v.Fixed {
			out.Vars = append(out.Vars, v)
		}
	}
	sort.SliceStable(out.Vars, func(i, j int) bool { return out.Vars[i].Order < out.Vars[j].Order })
	for _, pr := range c.procs {
		if pr.Extern {
			out.Externs[pr.Addr] = pr
		}
	}
	out.Listing = asm.Listing(res, c.Sources)
	out.Hex = IntelHex(c.org, res.Code)
	out.Pap = PaperTape(c.org, res.Code)
	out.Map = c.memMap(out, frames, framesInZP)
	out.Sym = c.symFile(out)
	return out, nil
}

func (c *Compiler) overflowError(res *asm.Result, over, limit int) error {
	type sz struct {
		name string
		n    int
	}
	var procs []sz
	starts := map[string]int{}
	for _, in := range c.insts {
		starts[in.Label] = res.Labels[in.Label]
	}
	var labels []string
	for l := range starts {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool { return starts[labels[i]] < starts[labels[j]] })
	for i, l := range labels {
		endA := res.End
		if i+1 < len(labels) {
			endA = starts[labels[i+1]]
		}
		procs = append(procs, sz{l, endA - starts[l]})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].n > procs[j].n })
	msg := fmt.Sprintf("program přesahuje limit $%04X o %d B", limit, over)
	for i := 0; i < len(procs) && i < 3; i++ {
		msg += fmt.Sprintf("; %s %d B", procs[i].name, procs[i].n)
	}
	return fmt.Errorf("%s", msg)
}

// usedVars vrací proměnné, na které kód odkazuje (ostatní se nealokují).
func (c *Compiler) usedVars() map[*Var]bool {
	refs := map[string]bool{}
	for _, it := range c.p.Items {
		if it.Arg.Sym != "" {
			refs[it.Arg.Sym] = true
		}
		for _, d := range it.Data {
			if d.Sym != "" {
				refs[d.Sym] = true
			}
		}
	}
	m := map[*Var]bool{}
	for _, v := range c.gvars {
		if refs[v.Label] {
			m[v] = true
		}
	}
	return m
}

func (c *Compiler) memMap(r *Result, frames map[string]int, framesInZP bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tantal %s, target %s\n\n", Version, c.T.Name)
	fmt.Fprintf(&b, "Kód a data:   $%04X–$%04X (%d B)\n", c.org, r.Asm.End-1, r.Asm.End-c.org)
	if r.VarTop > r.Asm.End {
		fmt.Fprintf(&b, "Proměnné:     $%04X–$%04X (%d B za kódem)\n\n", r.Asm.End, r.VarTop-1, r.VarTop-r.Asm.End)
	} else {
		fmt.Fprintf(&b, "Proměnné:     jen zero page\n\n")
	}
	fmt.Fprintf(&b, "Proměnné:\n")
	for _, v := range r.Vars {
		if v.Fixed {
			continue
		}
		where := "RAM"
		if v.InZP {
			where = "ZP"
		}
		fmt.Fprintf(&b, "  $%04X  %-5s %4d B  %s\n", v.Final, where, v.Size(), v.Name)
	}
	total := frames["total"]
	base := r.Labels["__frames"]
	if framesInZP {
		base = c.p.Equ["__frames"]
	}
	if total > 0 {
		where := "RAM"
		if framesInZP {
			where = "ZP"
		}
		fmt.Fprintf(&b, "\nRámce procedur (overlay, %s, $%04X, celkem %d B):\n", where, base, total)
		insts := append([]*Inst(nil), c.insts...)
		sort.Slice(insts, func(i, j int) bool { return insts[i].Base < insts[j].Base })
		for _, in := range insts {
			name := "hlavní program"
			if in.Proc != nil {
				name = in.Proc.Name
				if in.Ctx != "main" {
					name += " [" + in.Ctx + "]"
				}
			}
			fmt.Fprintf(&b, "  $%04X  %3d B  %s", base+in.Base, in.Size, name)
			if in.Proc != nil {
				var parts []string
				for _, v := range in.Proc.LocalList {
					if !v.Static {
						parts = append(parts, fmt.Sprintf("%s=+%d", v.Name, v.Off))
					}
				}
				if len(parts) > 0 {
					fmt.Fprintf(&b, "  (%s)", strings.Join(parts, ", "))
				}
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\nZero page (kompilátor):\n")
	var zp []string
	for _, v := range r.Vars {
		if v.InZP {
			zp = append(zp, fmt.Sprintf("$%02X %s", v.Final, v.Name))
		}
	}
	if framesInZP && total > 0 {
		zp = append(zp, fmt.Sprintf("$%02X–$%02X rámce", base, base+total-1))
	}
	sort.Strings(zp)
	for _, s := range zp {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	return b.String()
}

func (c *Compiler) symFile(r *Result) string {
	var lines []string
	for name, a := range r.Labels {
		if strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "__") {
			continue
		}
		lines = append(lines, fmt.Sprintf("%04X %s", a, name))
	}
	for _, v := range r.Vars {
		if v.InZP {
			lines = append(lines, fmt.Sprintf("%04X %s", v.Final, v.Label))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}
