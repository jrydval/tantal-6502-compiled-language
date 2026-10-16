package compiler

import (
	"github.com/jrydval/tantal-6502-compiled-language/asm"
	"github.com/jrydval/tantal-6502-compiled-language/lang"
)

// Type is the type of a value.
type Type int

const (
	TVoid Type = iota
	TByte
	TCard
	TBool
	TAddr // address (string literal, @x) - behaves like card
)

func (t Type) wide() bool { return t == TCard || t == TAddr }

func (t Type) String() string {
	return [...]string{"void", "byte", "card", "bool", "address"}[t]
}

// Var is a variable (global, local or at a fixed address).
type Var struct {
	Name       string
	Pos        lang.Pos
	Elem       Type // TByte / TCard (element type for arrays)
	Pointer    bool
	Array      bool
	String     bool
	Ring       bool
	Len        int // number of array elements (for strings including the length byte); 0 = unknown
	Const      bool
	Fixed      bool
	Addr       int
	ReadOnly   bool
	WriteOnly  bool
	Shadow     *Var
	NoInit     bool
	ShadowInit bool          // the register is written with the initial value at startup
	Init       []asm.Operand // initial data (bytes)
	HasInit    bool
	Proc       *Proc // owner of a local variable (nil = global)
	Off        int   // offset within the procedure frame
	Label      string
	Static     bool // local, but allocated statically (pointers)
	Head       *Var // ring
	Count      *Var // ring
	Hidden     bool
	Order      int
	// allocation result
	Final int
	InZP  bool
}

// Size returns the size of the variable in bytes.
func (v *Var) Size() int {
	switch {
	case v.Ring || v.Array:
		n := v.Len
		if v.Elem == TCard {
			n *= 2
		}
		return n
	case v.Pointer || v.Elem == TCard:
		return 2
	}
	return 1
}

// Scalar: a simple value (not an array, not a ring).
func (v *Var) Scalar() bool { return !v.Array && !v.Ring }

// Type of the value of a scalar variable.
func (v *Var) Type() Type {
	if v.Pointer {
		return TCard
	}
	return v.Elem
}

// Bit is an alias of a bit or bit field of a register.
type Bit struct {
	Name   string
	Reg    *Var
	Lo, Hi int
}

func (b *Bit) mask() int { return ((1 << (b.Hi - b.Lo + 1)) - 1) << b.Lo }

// Proc is a procedure.
type Proc struct {
	Name      string
	Decl      *lang.ProcDecl
	Params    []*Var
	Ret       Type
	Extern    bool
	Addr      int
	Interrupt string
	IrqSafe   bool
	Locals    map[string]*Var
	LocalList []*Var
	LocalSize int
	Insts     map[string]*Inst
}

// Inst is an instance of a procedure in a context (main program, irq, nmi).
type Inst struct {
	Proc     *Proc
	Ctx      string
	Label    string
	FrameSym string
	Temps    int // maximum number of temporary bytes
	Calls    map[*Inst]bool
	Base     int
	Size     int
	Done     bool
	Main     bool
}
