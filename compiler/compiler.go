// Package compiler translates Tantal into 6502 machine code.
package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrydval/tantal/asm"
	"github.com/jrydval/tantal/lang"
)

// Version is the compiler version.
const Version = "0.1.0"

type constVal struct{ V int }

// Compiler holds the state of one compilation.
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
	c.Warnings = append(c.Warnings, fmt.Sprintf("%s: warning: %s", pos, fmt.Sprintf(f, a...)))
}

// Result is the compilation result.
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

// CompileFile compiles a file.
func CompileFile(path, target string) (*Result, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Compile(path, string(src), target)
}

// Compile compiles source code.
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
				c.fail(s.P(), "a target file may contain only declarations")
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
			c.fail(d.Pos, "the .zp exclude range must lie within $00-$FF")
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
			c.fail(d.Pos, "cannot read %s", d.Text)
		}
		c.parseFile(file, string(src), false)
	case "target":
		if d.Text != c.T.Name {
			c.fail(d.Pos, "the program is for target %q, compiling for %q", d.Text, c.T.Name)
		}
	case "cpu":
		if d.Text != "6502" {
			c.fail(d.Pos, "v0.1 supports only .cpu 6502")
		}
	case "vars":
		c.fail(d.Pos, ".vars is not supported yet; variables go to the zero page and after the code")
	case "cycles":
		c.mainBody = append(c.mainBody, d)
	}
}

func (c *Compiler) define(name string, pos lang.Pos, v any) {
	if _, dup := c.globals[name]; dup {
		c.fail(pos, "name %q is already defined", name)
	}
	if builtinNames[name] {
		c.fail(pos, "name %q is reserved", name)
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
		c.fail(d.Pos, "v0.1 supports only byte pointer")
	}
	if v.Array && v.Elem == TCard {
		c.fail(d.Pos, "v0.1 supports only byte arrays")
	}
	if d.Type.LenExpr != nil {
		v.Len = c.mustConst(d.Type.LenExpr)
		if v.Len <= 0 {
			c.fail(d.Pos, "array size must be positive")
		}
		if v.String {
			v.Len++
		}
	}
	if d.Addr != nil {
		v.Fixed = true
		v.Addr = c.mustConst(d.Addr)
		if v.Addr < 0 || v.Addr > 0xFFFF {
			c.fail(d.Pos, "address out of range")
		}
		if proc != nil {
			c.fail(d.Pos, "a variable at a fixed address must be global")
		}
		if v.Addr < 256 {
			for _, r := range c.zpExclude {
				if v.Addr >= r.Lo && v.Addr <= r.Hi {
					c.warn(d.Pos, "%s lies in an excluded part of the zero page", v.Name)
				}
			}
		}
		for _, r := range c.T.Reserve {
			if v.Addr >= r.Lo && v.Addr <= r.Hi && (r.Lo >= 0x1700 && r.Hi < 0x1800) {
				c.warn(d.Pos, "%s lies in the reserved range $%04X-$%04X", v.Name, r.Lo, r.Hi)
			}
		}
	}
	if d.Shadow {
		if !v.Fixed || !v.Scalar() || v.Elem != TByte {
			c.fail(d.Pos, "shadow can only be used on a byte register with an @ address")
		}
		v.Shadow = &Var{Name: v.Name + "'shadow", Elem: TByte, Hidden: true, Pos: d.Pos}
	}
	if v.Const && !v.Array {
		c.fail(d.Pos, "const on a variable is for tables; for a constant write const NAME = value")
	}
	// initial values
	switch {
	case d.File != "":
		data, err := os.ReadFile(filepath.Join(filepath.Dir(d.Pos.File), d.File))
		if err != nil {
			c.fail(d.Pos, "cannot read %s", d.File)
		}
		for _, b := range data {
			v.Init = append(v.Init, asm.N(int(b)))
		}
		v.HasInit = true
	case v.String && len(d.Init) > 0:
		s, ok := d.Init[0].(*lang.Str)
		if !ok || len(d.Init) > 1 {
			c.fail(d.Pos, "a string is initialized with a quoted literal")
		}
		if len(s.S) > 255 {
			c.fail(d.Pos, "string is longer than 255 characters")
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
				c.fail(e.P(), "value %d does not fit in a byte", x)
			}
			v.Init = append(v.Init, asm.N(x&0xFF))
		}
		v.HasInit = true
	case len(d.Init) > 0 && proc == nil:
		op, ok := c.constOp(d.Init[0])
		if !ok {
			c.fail(d.Init[0].P(), "the initial value of a global variable must be a constant")
		}
		if op.Sym == "" {
			if v.Type() == TByte && (op.Off < -128 || op.Off > 255) {
				c.fail(d.Init[0].P(), "value %d does not fit in a byte", op.Off)
			}
			v.Init = append(v.Init, asm.N(op.Off&0xFF))
			if v.Type() == TCard {
				v.Init = append(v.Init, asm.N((op.Off>>8)&0xFF))
			}
		} else {
			if v.Type() != TCard {
				c.fail(d.Init[0].P(), "an address fits only into card or pointer")
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
			c.fail(d.Pos, "array without a size and without initial values")
		}
		if len(v.Init) > v.Len {
			c.fail(d.Pos, "more initial values (%d) than the array size (%d)", len(v.Init), v.Len)
		}
		for len(v.Init) < v.Len && v.HasInit {
			v.Init = append(v.Init, asm.N(0))
		}
	}
	if v.Const && !v.HasInit && !v.Fixed {
		c.fail(d.Pos, "a const table needs initial values")
	}
	if v.Shadow != nil && v.HasInit {
		v.Shadow.Init = v.Init
		v.Shadow.HasInit = true
		v.ShadowInit = true
		v.Init, v.HasInit = nil, false
	}
	if v.Fixed && v.HasInit && v.Shadow == nil {
		c.fail(d.Pos, "a variable at a fixed address cannot have an initial value (except a shadow register)")
	}
	if v.Ring {
		if proc != nil {
			c.fail(d.Pos, "ring must be global")
		}
		if v.Len&(v.Len-1) != 0 || v.Len < 2 || v.Len > 256 {
			c.fail(d.Pos, "ring size must be a power of 2 (2 to 256)")
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
		c.fail(d.Pos, "a local variable cannot be noinit (it shares memory through the overlay)")
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
		// flag in RAM: in v0.1 one byte per flag, bit 0
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
		c.fail(d.Pos, "%s is not a byte variable or register", d.Reg)
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
		c.fail(d.Pos, "an interrupt handler has no parameters and no return value")
	}
	if d.Extern != nil {
		p.Extern = true
		p.Addr = c.mustConst(d.Extern)
		used := map[string]bool{}
		for _, prm := range d.Params {
			if prm.Reg == "" || !strings.Contains("AXY", prm.Reg) || len(prm.Reg) != 1 {
				c.fail(d.Pos, "parameter %s must have a register @A, @X or @Y", prm.Name)
			}
			if prm.Type.Base != "byte" || prm.Type.Pointer || prm.Type.Array {
				c.fail(d.Pos, "a parameter of an extern procedure must be byte")
			}
			if used[prm.Reg] {
				c.fail(d.Pos, "register %s is used twice", prm.Reg)
			}
			used[prm.Reg] = true
			p.Params = append(p.Params, &Var{Name: prm.Name, Elem: TByte, Label: prm.Reg})
		}
		if d.RetReg != "" && d.RetReg != "A" {
			c.fail(d.Pos, "the return value of an extern procedure must be in @A")
		}
		if p.Ret == TCard {
			c.fail(d.Pos, "an extern procedure can only return byte for now")
		}
	}
	c.define(d.Name, d.Pos, p)
	c.procs = append(c.procs, p)
}

// collectLocals walks procedure bodies and assigns frame offsets to parameters and locals.
func (c *Compiler) collectLocals() {
	for _, p := range c.procs {
		if p.Extern {
			continue
		}
		off := 0
		add := func(v *Var) {
			if _, dup := p.Locals[v.Name]; dup {
				c.fail(v.Pos, "local name %q is already defined", v.Name)
			}
			if builtinNames[v.Name] {
				c.fail(v.Pos, "name %q is reserved", v.Name)
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
				c.fail(p.Decl.Pos, "parameter %s: arrays are passed as byte pointer", prm.Name)
			}
			v := c.newVar(d, p)
			v.Static = false // a pointer parameter lives in the frame and is copied before use
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
						c.fail(d.Pos, "a local array cannot have initial values; use a global const table")
					}
					add(v)
				case *lang.ConstDecl, *lang.BitDecl, *lang.ProcDecl:
					c.fail(s.P(), "only variable declarations are allowed inside a procedure")
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

// ---- allocation and output ----

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
	// overlay: instance base = max(caller base + caller size) within one root
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
	// topological order (recursion is already ruled out)
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
		// root and its call tree
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
		// walk in reverse topological order (callers before callees)
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
				c.fail(cal.Proc.Decl.Pos, "recursion is not allowed: %s", strings.Join(names, " -> "))
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

// sharedWarnings warns about non-atomic changes of data shared with an interrupt handler.
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
				c.warn(a.pos, "%s is modified by both the main program and an interrupt handler; wrap the change in an atomic block", k)
			}
		}
	}
	for _, name := range []string{"__mp", "__rt"} {
		if len(c.helperCtx[name]) > 1 {
			what := map[string]string{"__mp": "mem[], pointer parameters and large arrays", "__rt": "multiplication and division"}[name]
			c.Warnings = append(c.Warnings, fmt.Sprintf("warning: %s is used in the main program and in an interrupt handler; the helper variables are not protected (v0.1)", what))
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
	// fixed variables in the zero page
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
			after = append(after, v) // must be in the binary
			return
		}
		if a, ok := pool.take(v.Size()); ok {
			v.InZP = true
			v.Final = a
			p.Equ[v.Label] = a
			return
		}
		if mustZP {
			c.fail(v.Pos, "%s must be in the zero page, but the zero page is full", v.Name)
		}
		after = append(after, v)
	}
	used := c.usedVars()
	// 1. pointers and helper pointers
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
	// 2. procedure frames
	framesInZP := false
	if total > 0 {
		if a, ok := pool.take(total); ok {
			p.Equ["__frames"] = a
			framesInZP = true
		}
	}
	// 3. scalars
	for _, v := range c.gvars {
		if !v.Pointer && v.Scalar() && used[v] && v != c.mp && v != c.rt {
			place(v, false)
		}
	}
	// 4. the rest
	for _, v := range c.gvars {
		if !v.Scalar() && used[v] && !v.InZP && !v.Const && v != c.rt && v != c.mp {
			after = append(after, v)
		}
	}
	for _, in := range c.insts {
		p.Alias[in.FrameSym] = asm.S("__frames", in.Base)
	}
	// data section: data of noinit variables with an initial value
	for _, v := range after {
		if v.NoInit && v.HasInit {
			p.Label(v.Label)
			p.Bytes(v.Init...)
		}
	}
	// reserved space after the code
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
	// memory checks
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
		return nil, fmt.Errorf("the program ($%04X-$%04X) does not fit entirely into the RAM of target %s", c.org, res.Top-1, c.T.Name)
	}
	for _, r := range c.T.Reserve {
		if c.org <= r.Hi && res.Top-1 >= r.Lo {
			return nil, fmt.Errorf("the program ($%04X-$%04X) overlaps the reserved range $%04X-$%04X", c.org, res.Top-1, r.Lo, r.Hi)
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
	msg := fmt.Sprintf("the program exceeds the limit $%04X by %d B", limit, over)
	for i := 0; i < len(procs) && i < 3; i++ {
		msg += fmt.Sprintf("; %s %d B", procs[i].name, procs[i].n)
	}
	return fmt.Errorf("%s", msg)
}

// usedVars returns the variables referenced by code (others are not allocated).
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
	fmt.Fprintf(&b, "Code and data: $%04X-$%04X (%d B)\n", c.org, r.Asm.End-1, r.Asm.End-c.org)
	if r.VarTop > r.Asm.End {
		fmt.Fprintf(&b, "Variables:     $%04X-$%04X (%d B after the code)\n\n", r.Asm.End, r.VarTop-1, r.VarTop-r.Asm.End)
	} else {
		fmt.Fprintf(&b, "Variables:     zero page only\n\n")
	}
	fmt.Fprintf(&b, "Variables:\n")
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
		fmt.Fprintf(&b, "\nProcedure frames (overlay, %s, $%04X, total %d B):\n", where, base, total)
		insts := append([]*Inst(nil), c.insts...)
		sort.Slice(insts, func(i, j int) bool { return insts[i].Base < insts[j].Base })
		for _, in := range insts {
			name := "main program"
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
	fmt.Fprintf(&b, "\nZero page (compiler):\n")
	var zp []string
	for _, v := range r.Vars {
		if v.InZP {
			zp = append(zp, fmt.Sprintf("$%02X %s", v.Final, v.Name))
		}
	}
	if framesInZP && total > 0 {
		zp = append(zp, fmt.Sprintf("$%02X-$%02X frames", base, base+total-1))
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
