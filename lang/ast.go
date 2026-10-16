package lang

// Expr je výraz.
type Expr interface{ P() Pos }

// Stmt je příkaz nebo deklarace.
type Stmt interface{ P() Pos }

type (
	// Num je číselná konstanta.
	Num struct {
		Pos Pos
		V   int
	}
	// Str je řetězcový literál.
	Str struct {
		Pos   Pos
		S     string
		ZTerm bool
	}
	// Ident je jméno.
	Ident struct {
		Pos  Pos
		Name string
	}
	// Index je a[i].
	Index struct {
		Pos  Pos
		Base string
		Idx  Expr
	}
	// BitRef je X.n nebo X.a..b.
	BitRef struct {
		Pos    Pos
		Base   string
		Lo, Hi int
	}
	// Unary je unární operátor: - ~ not @.
	Unary struct {
		Pos Pos
		Op  string
		X   Expr
	}
	// Binary je binární operátor.
	Binary struct {
		Pos  Pos
		Op   string
		L, R Expr
	}
	// Call je volání procedury nebo zabudované funkce.
	Call struct {
		Pos  Pos
		Name string
		Args []Expr
	}
	// Wait je výraz wait.
	Wait struct {
		Pos     Pos
		Kind    string // "cond", "change", "rising", "falling"
		Cond    Expr   // pro "cond"
		Target  Expr   // Ident nebo BitRef
		From    string // proměnná pro change ... from
		Timeout Expr   // hodnota limitu, nil = bez limitu
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

// TypeSpec popisuje typ v deklaraci.
type TypeSpec struct {
	Base    string // "byte", "card"
	Pointer bool
	String  bool
	Ring    bool
	Array   bool
	LenExpr Expr // nil = odvodit z inicializace
}

type (
	// VarDecl je deklarace proměnné.
	VarDecl struct {
		Pos       Pos
		Name      string
		Type      TypeSpec
		Const     bool
		Addr      Expr // @ adresa
		Init      []Expr
		File      string // file "x.bin"
		ReadOnly  bool
		WriteOnly bool
		Shadow    bool
		NoInit    bool
	}
	// ConstDecl je const JMÉNO = výraz.
	ConstDecl struct {
		Pos  Pos
		Name string
		X    Expr
	}
	// BitDecl je bit/bits alias, případně bitový příznak v RAM.
	BitDecl struct {
		Pos    Pos
		Name   string
		Reg    string // "" = příznak v RAM
		Lo, Hi int
		Init   Expr
	}
	// Param je parametr procedury.
	Param struct {
		Name string
		Type TypeSpec
		Reg  string // A/X/Y u extern
	}
	// ProcDecl je procedura.
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
	// Directive je .org a podobné.
	Directive struct {
		Pos  Pos
		Name string
		Args []Expr
		Text string
	}
	// Assign je přiřazení (Op "=", "+=", ...).
	Assign struct {
		Pos    Pos
		Target Expr
		Op     string
		Value  Expr
	}
	// Push je buf <- x.
	Push struct {
		Pos   Pos
		Ring  string
		Value Expr
	}
	// If je podmínka.
	If struct {
		Pos    Pos
		Conds  []Expr
		Blocks [][]Stmt
		Else   []Stmt
	}
	// Loop je do ... od, případně s until na konci.
	Loop struct {
		Pos   Pos
		Body  []Stmt
		Until Expr
	}
	// While je while c do ... od.
	While struct {
		Pos  Pos
		Cond Expr
		Body []Stmt
	}
	// For je smyčka for.
	For struct {
		Pos       Pos
		Var       string
		From, To  Expr
		Exclusive bool // until
		Step      Expr
		Body      []Stmt
	}
	// Exit vyskočí ze smyčky.
	Exit struct{ Pos Pos }
	// Return vrací z procedury.
	Return struct {
		Pos Pos
		X   Expr
	}
	// ExprStmt je výraz jako příkaz (volání, wait).
	ExprStmt struct {
		Pos Pos
		X   Expr
	}
	// Asm je blok inline assembleru.
	Asm struct {
		Pos   Pos
		Lines []string
		First int // číslo prvního řádku
	}
	// Atomic je blok se zakázaným přerušením.
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
