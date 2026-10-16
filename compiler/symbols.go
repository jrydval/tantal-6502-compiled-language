package compiler

import (
	"tantal/asm"
	"tantal/lang"
)

// Type je typ hodnoty.
type Type int

const (
	TVoid Type = iota
	TByte
	TCard
	TBool
	TAddr // adresa (řetězcový literál, @x) – chová se jako card
)

func (t Type) wide() bool { return t == TCard || t == TAddr }

func (t Type) String() string {
	return [...]string{"void", "byte", "card", "bool", "adresa"}[t]
}

// Var je proměnná (globální, lokální nebo pevná na adrese).
type Var struct {
	Name       string
	Pos        lang.Pos
	Elem       Type // TByte / TCard (u pole typ prvku)
	Pointer    bool
	Array      bool
	String     bool
	Ring       bool
	Len        int // počet prvků pole (u řetězce včetně bajtu délky); 0 = neznámá
	Const      bool
	Fixed      bool
	Addr       int
	ReadOnly   bool
	WriteOnly  bool
	Shadow     *Var
	NoInit     bool
	ShadowInit bool          // registr se při startu zapíše počáteční hodnotou
	Init       []asm.Operand // počáteční data (bajty)
	HasInit    bool
	Proc       *Proc // vlastník lokální proměnné (nil = globální)
	Off        int   // posun v rámci procedury
	Label      string
	Static     bool // lokální, ale alokovaná staticky (ukazatele)
	Head       *Var // ring
	Count      *Var // ring
	Hidden     bool
	Order      int
	// výsledek alokace
	Final int
	InZP  bool
}

// Size vrací velikost proměnné v bajtech.
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

// Scalar: jednoduchá hodnota (ne pole, ne ring).
func (v *Var) Scalar() bool { return !v.Array && !v.Ring }

// Type hodnoty skalární proměnné.
func (v *Var) Type() Type {
	if v.Pointer {
		return TCard
	}
	return v.Elem
}

// Bit je alias na bit nebo pole bitů registru.
type Bit struct {
	Name   string
	Reg    *Var
	Lo, Hi int
}

func (b *Bit) mask() int { return ((1 << (b.Hi - b.Lo + 1)) - 1) << b.Lo }

// Proc je procedura.
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

// Inst je instance procedury v kontextu (hlavní program, irq, nmi).
type Inst struct {
	Proc     *Proc
	Ctx      string
	Label    string
	FrameSym string
	Temps    int // maximální počet bajtů dočasných proměnných
	Calls    map[*Inst]bool
	Base     int
	Size     int
	Done     bool
	Main     bool
}
