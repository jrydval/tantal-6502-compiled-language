package lang

// Expr is an expression.
type Expr interface{ P() Pos }

// Stmt is a statement or declaration.
type Stmt interface{ P() Pos }

type (
	// Num is a numeric constant.
	Num struct {
		Pos Pos
		V   int
	}
	// Str is a string literal.
	Str struct {
		Pos   Pos
		S     string
		ZTerm bool
	}
	// Ident is a name.
	Ident struct {
		Pos  Pos
		Name string
	}
	// Index is a[i].
	Index struct {
		Pos  Pos
		Base string
		Idx  Expr
	}
	// BitRef is X.n or X.a..b.
	BitRef struct {
		Pos    Pos
		Base   string
		Lo, Hi int
	}
	// Unary is a unary operator: - ~ not @.
	Unary struct {
		Pos Pos
		Op  string
		X   Expr
	}
	// Binary is a binary operator.
	Binary struct {
		Pos  Pos
		Op   string
		L, R Expr
	}
	// Call is a call of a procedure or built-in function.
	Call struct {
		Pos  Pos
		Name string
		Args []Expr
	}
	// Wait is a wait expression.
	Wait struct {
		Pos     Pos
		Kind    string // "cond", "change", "rising", "falling"
		Cond    Expr   // for "cond"
		Target  Expr   // Ident or BitRef
		From    string // variable for change ... from
		Timeout Expr   // timeout value, nil = no timeout
		Unit    string // us, ms, s
	}
)

func (e *Num) P() Pos    { return e.Pos }
func (e *Str) P() Pos    { return e.Pos }
func (e *Ident) P() Pos  { return e.Pos }
func (e *Index) P() Pos  { return e.Pos }
func (e *BitRef) P() Pos { return e.Pos }
func (e *Unary) P() Pos  { return e.Pos }
func (e *Binary) P() Pos { return e.Pos }
func (e *Call) P() Pos   { return e.Pos }
func (e *Wait) P() Pos   { return e.Pos }

// TypeSpec describes a type in a declaration.
type TypeSpec struct {
	Base    string // "byte", "card"
	Pointer bool
	String  bool
	Ring    bool
	Array   bool
	LenExpr Expr // nil = derive from the initializer
}

type (
	// VarDecl is a variable declaration.
	VarDecl struct {
		Pos       Pos
		Name      string
		Type      TypeSpec
		Const     bool
		Addr      Expr // @ address
		Init      []Expr
		File      string // file "x.bin"
		ReadOnly  bool
		WriteOnly bool
		Shadow    bool
		NoInit    bool
	}
	// ConstDecl is const NAME = expr.
	ConstDecl struct {
		Pos  Pos
		Name string
		X    Expr
	}
	// BitDecl is a bit/bits alias, or a bit flag in RAM.
	BitDecl struct {
		Pos    Pos
		Name   string
		Reg    string // "" = flag in RAM
		Lo, Hi int
		Init   Expr
	}
	// Param is a procedure parameter.
	Param struct {
		Name string
		Type TypeSpec
		Reg  string // A/X/Y for extern
	}
	// ProcDecl is a procedure.
	ProcDecl struct {
		Pos       Pos
		Name      string
		Params    []Param
		Ret       string // "", "byte", "card"
		RetReg    string
		Body      []Stmt
		Interrupt string // "", "irq", "nmi"
		Extern    Expr
		IrqSafe   bool
		EndPos    Pos
	}
	// Directive is .org and similar.
	Directive struct {
		Pos  Pos
		Name string
		Args []Expr
		Text string
	}
	// Assign is an assignment (Op "=", "+=", ...).
	Assign struct {
		Pos    Pos
		Target Expr
		Op     string
		Value  Expr
	}
	// Push is buf <- x.
	Push struct {
		Pos   Pos
		Ring  string
		Value Expr
	}
	// If is a conditional.
	If struct {
		Pos    Pos
		Conds  []Expr
		Blocks [][]Stmt
		Else   []Stmt
	}
	// Loop is do ... od, optionally with until at the end.
	Loop struct {
		Pos   Pos
		Body  []Stmt
		Until Expr
	}
	// While is while c do ... od.
	While struct {
		Pos  Pos
		Cond Expr
		Body []Stmt
	}
	// For is a for loop.
	For struct {
		Pos       Pos
		Var       string
		From, To  Expr
		Exclusive bool // until
		Step      Expr
		Body      []Stmt
	}
	// Exit leaves a loop.
	Exit struct{ Pos Pos }
	// Return returns from a procedure.
	Return struct {
		Pos Pos
		X   Expr
	}
	// ExprStmt is an expression used as a statement (call, wait).
	ExprStmt struct {
		Pos Pos
		X   Expr
	}
	// Asm is an inline assembler block.
	Asm struct {
		Pos   Pos
		Lines []string
		First int // number of the first line
	}
	// Atomic is a block with interrupts disabled.
	Atomic struct {
		Pos  Pos
		Body []Stmt
	}
)

func (s *VarDecl) P() Pos   { return s.Pos }
func (s *ConstDecl) P() Pos { return s.Pos }
func (s *BitDecl) P() Pos   { return s.Pos }
func (s *ProcDecl) P() Pos  { return s.Pos }
func (s *Directive) P() Pos { return s.Pos }
func (s *Assign) P() Pos    { return s.Pos }
func (s *Push) P() Pos      { return s.Pos }
func (s *If) P() Pos        { return s.Pos }
func (s *Loop) P() Pos      { return s.Pos }
func (s *While) P() Pos     { return s.Pos }
func (s *For) P() Pos       { return s.Pos }
func (s *Exit) P() Pos      { return s.Pos }
func (s *Return) P() Pos    { return s.Pos }
func (s *ExprStmt) P() Pos  { return s.Pos }
func (s *Asm) P() Pos       { return s.Pos }
func (s *Atomic) P() Pos    { return s.Pos }
