package orders

// bom.go — WHAT A FINISHED GOOD IS MADE OF, and what a batch of it consumes
// (T3, migration 0043).
//
// A BOM rides in this package with orders because it shares their shape — a
// quantity that is not a movement — but it is one step further from the ledger.
// An order is at least a fact about the world (a customer has promised to buy); a
// BOM is a STATEMENT OF INTENT about arithmetic: "one Table takes 4 Legs, 1 Top
// and 200g of screws".
//
// It becomes real only when EXPLODED into the inventory lines of a Stock Journal,
// which already exists (ledger.StockJournal). Explode() below returns those lines
// as data; it does not post them, and this package could not post them if it
// wanted to — internal/archtest forbids the edge to internal/ledger. The operator
// reviews the explosion and submits it as an ordinary Stock Journal, which is what
// keeps a manufacture an auditable voucher rather than a side effect of opening a
// recipe.
//
// ===========================================================================
// ⛔⛔ THE RECIPE IS STORED AS WRITTEN AND SCALED ONCE, NEVER PRE-DIVIDED
// ===========================================================================
// This is the one arithmetic decision in the file and the naive shape gets it
// wrong irrecoverably.
//
// Recipes are stated as a YIELD: "10 kg of resin makes 3 mouldings". The tempting
// normalisation is a per-unit figure — 10/3 = 3.3333 kg per moulding.
// NUMERIC(19,4) truncates that, and exploding it back for a batch of 3 gives
// 9.9999 kg. The consumption is short by a tenth of a gram, every batch, forever;
// it accumulates in closing stock as phantom material that reconciles against
// nothing, and it is exactly the tolerance HANDOFF §7.5 names as how a real error
// hides for a year.
//
// So both numbers are stored exactly as the operator wrote them, and Explode
// scales by the RATIO (wanted / output_qty) in ONE multiplication at full
// precision, quantizing only at the end. A batch of 3 consumes exactly 10 kg
// because that is the number that was stored.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// bomScale is the number of decimal places a component quantity carries. It
// matches the NUMERIC(19,4) column and internal/ledger's own moneyScale, which is
// what lets an exploded quantity and the stock movement written from it agree
// exactly.
const bomScale int32 = 4

// ComponentInput is one material a recipe consumes.
type ComponentInput struct {
	StockItemID uuid.UUID
	// Qty is consumed to yield BomInput.OutputQty of the finished good — NOT per
	// unit of it. See the file header.
	Qty decimal.Decimal
}

// BomInput is a recipe as an operator states it.
type BomInput struct {
	// StockItemID is the FINISHED GOOD this recipe produces.
	StockItemID uuid.UUID
	// Name distinguishes several recipes for one item — a standard build and a
	// cheaper substitute build, or a summer and a winter formulation. Unique
	// within its item, case-insensitively.
	Name string
	// OutputQty is the YIELD. ⛔ Never divide the components by it to store a
	// per-unit figure; see the file header.
	OutputQty  decimal.Decimal
	IsDefault  bool
	CreatedBy  string
	Components []ComponentInput
}

// Validate refuses what the database would refuse anyway, with a sentence.
//
// ⚠️ IT DOES NOT CHECK THE SELF-REFERENCE. That an item is not its own component
// is migration 0043's bom_component_not_self trigger, because the finished good
// lives on the PARENT row and a validator here would need a second copy of it
// that can disagree. CreateBom translates that refusal below.
func (in BomInput) Validate() error {
	if in.StockItemID == uuid.Nil {
		return errors.New(
			"orders: a bill of materials must name the finished good it produces")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New(
			"orders: a bill of materials needs a name — an item may have several recipes (a standard " +
				"build and a substitute one) and the name is what an operator picks at explosion time")
	}
	if !in.OutputQty.IsPositive() {
		return errors.New(
			"orders: the yield must be more than zero — it is how many of the finished good this " +
				"recipe makes, and the explosion scales the components by (wanted / yield), so a zero " +
				"would make every batch either nothing or a division by zero")
	}
	if len(in.Components) == 0 {
		return errors.New(
			"orders: a recipe with no components consumes nothing — exploding it would produce a " +
				"finished good out of thin air, and the stock journal would receive goods with no issue " +
				"against them")
	}
	seen := make(map[uuid.UUID]bool, len(in.Components))
	for i, c := range in.Components {
		where := fmt.Sprintf(" on component %d", i+1)
		if c.StockItemID == uuid.Nil {
			return errors.New("orders: every component must name a stock item" + where)
		}
		if !c.Qty.IsPositive() {
			return fmt.Errorf(
				"orders: a component quantity must be more than zero%s — it is what the recipe consumes "+
					"to yield the stated output, so a zero component is a material that is listed and "+
					"never issued", where)
		}
		if c.StockItemID == in.StockItemID {
			// Refused here as well as by the trigger, because this one IS decidable
			// from the request: the finished good is right there on the same struct.
			// The trigger stays the authority for the case where a component is added
			// to a recipe later.
			return fmt.Errorf(
				"orders: the finished good cannot also be a component of its own recipe%s — exploding it "+
					"would consume the very thing it produces, and the stock journal would both issue and "+
					"receive the same item with no material actually converted", where)
		}
		if seen[c.StockItemID] {
			return fmt.Errorf(
				"orders: the same item appears twice in this recipe%s — the same requirement keyed twice "+
					"can only make a total come out right by making the other line wrong; an operator who "+
					"wants 4 legs writes 4, not two lines of 2", where)
		}
		seen[c.StockItemID] = true
	}
	return nil
}

// Component is one recipe line read back.
type Component struct {
	LineNo      int
	StockItemID uuid.UUID
	StockItem   string
	Qty         decimal.Decimal
}

// Bom is a recipe read back.
type Bom struct {
	ID          uuid.UUID
	StockItemID uuid.UUID
	StockItem   string
	Name        string
	OutputQty   decimal.Decimal
	IsDefault   bool
	Components  []Component
}

// CreateBom writes one recipe and its components in ONE transaction, for the
// reason Create gives: a head with no components is the state Validate refuses,
// and it must also be unreachable by a partial write.
func (s *Service) CreateBom(ctx context.Context, in BomInput) (uuid.UUID, error) {
	if err := in.Validate(); err != nil {
		return uuid.Nil, err
	}
	bomID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("orders: new bom id: %w", err)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, errors.New("orders: no company in context")
	}
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = "unknown"
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO bom_head
			   (id, company_id, stock_item_id, name, output_qty, is_default, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			bomID, companyID, in.StockItemID, strings.TrimSpace(in.Name),
			in.OutputQty, in.IsDefault, createdBy,
		); err != nil {
			return wrapBomWrite(err, in)
		}
		for i, c := range in.Components {
			compID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("orders: new bom component id: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO bom_component
				   (id, company_id, bom_id, line_no, stock_item_id, qty)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				compID, companyID, bomID, i+1, c.StockItemID, c.Qty,
			); err != nil {
				return wrapBomWrite(err, in)
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return bomID, nil
}

// GetBom reads one recipe with its components.
func (s *Service) GetBom(ctx context.Context, bomID uuid.UUID) (Bom, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return Bom{}, errors.New("orders: no company in context")
	}
	var out Bom
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := readBom(ctx, tx, companyID, bomID)
		if err != nil {
			return err
		}
		out = b
		return nil
	})
	return out, err
}

// ListBoms returns a company's recipes, optionally only those for one item.
func (s *Service) ListBoms(ctx context.Context, forItem uuid.UUID) ([]Bom, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return nil, errors.New("orders: no company in context")
	}
	var out []Bom
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT bh.id, bh.stock_item_id, COALESCE(si.name, ''), bh.name,
			        bh.output_qty, bh.is_default
			   FROM bom_head bh
			   LEFT JOIN stock_item si
			     ON si.company_id = bh.company_id AND si.id = bh.stock_item_id
			  WHERE bh.company_id = $1
			    AND ($2::uuid IS NULL OR bh.stock_item_id = $2::uuid)
			  ORDER BY lower(btrim(COALESCE(si.name, ''))), bh.is_default DESC,
			           lower(btrim(bh.name))`,
			companyID, nullIfNilUUID(forItem))
		if err != nil {
			return fmt.Errorf("orders: list boms: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b Bom
			if err := rows.Scan(&b.ID, &b.StockItemID, &b.StockItem, &b.Name,
				&b.OutputQty, &b.IsDefault); err != nil {
				return fmt.Errorf("orders: scan bom: %w", err)
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	// The components are deliberately NOT loaded here, for the reason List gives
	// about order lines: a picker needs to know which recipes exist, and loading
	// every component of every recipe to render one is how a list becomes a query
	// that times out.
	return out, err
}

// Explosion is what a Stock Journal's lines would be for a wanted quantity.
type Explosion struct {
	BomID     uuid.UUID
	StockItem string
	// WantedQty is what the operator asked to make; OutputQty is the recipe's own
	// yield. BOTH are returned so a screen can show the ratio it scaled by rather
	// than leaving an operator to wonder why 3 units consumed 10 kg.
	WantedQty  decimal.Decimal
	OutputQty  decimal.Decimal
	Components []Component
	Notes      []string
}

// Explode scales a recipe to a wanted quantity.
//
// ⛔ ONE MULTIPLICATION AT FULL PRECISION, QUANTIZED ONLY AT THE END. The ratio
// (wanted / output) is never materialised as a stored or rounded number: for a
// 10kg-makes-3 recipe the ratio is 0.333… and rounding it first is exactly the
// truncation the file header describes. decimal.Div carries enough precision that
// qty.Mul(wanted).Div(output) is exact for every case that has an exact answer,
// and the multiplication is done FIRST so the division happens on the larger
// number.
//
// ⚠️ SINGLE-LEVEL. A component that itself has a recipe is NOT exploded
// recursively — a Table explodes to 4 Legs, and if a Leg has its own BOM the
// operator issues a second Stock Journal. That is Tally's own default and it is
// what keeps work-in-progress visible as stock instead of vanishing inside one
// entry. The notes say so on the artefact, because a screen that implied the
// explosion reached raw materials would have an operator issuing components they
// do not hold.
func (s *Service) Explode(ctx context.Context, bomID uuid.UUID, wanted decimal.Decimal) (Explosion, error) {
	if !wanted.IsPositive() {
		return Explosion{}, errors.New(
			"orders: the quantity to make must be more than zero")
	}
	b, err := s.GetBom(ctx, bomID)
	if err != nil {
		return Explosion{}, err
	}
	out := Explosion{
		BomID:     b.ID,
		StockItem: b.StockItem,
		WantedQty: wanted,
		OutputQty: b.OutputQty,
	}
	for _, c := range b.Components {
		out.Components = append(out.Components, Component{
			LineNo:      c.LineNo,
			StockItemID: c.StockItemID,
			StockItem:   c.StockItem,
			Qty:         ScaleComponent(c.Qty, wanted, b.OutputQty),
		})
	}
	out.Notes = explosionNotes(b, wanted)
	return out, nil
}

// ScaleComponent is THE arithmetic of a bill of materials, extracted as a pure
// function so it can be tested without a database — because it is the one thing in
// this package that is wrong in a way no integration test would notice.
//
// ⛔⛔ MULTIPLY FIRST, DIVIDE SECOND, QUANTIZE ONCE AT THE END.
//
// The order of those three operations is the whole decision. The tempting form is
// `wanted.Div(output).Mul(qty)` — compute the ratio, then scale — and for the
// standard recipe shape it is silently, permanently wrong:
//
//	10 kg of resin yields 3 mouldings. Make 3.
//	  ratio first:  10 * (3/3 = 1.0000)                     = 10.0000  ✓ (this case survives)
//	  make 1:       10 * (1/3 = 0.3333)                     =  3.3330  ✗ (0.0003 short)
//	  make 6:       10 * (6/3 = 2.0000)                     = 20.0000  ✓
//	  make 7:       10 * (7/3 = 2.3333)                     = 23.3330  ✗ (0.0003 short)
//	multiply first: (10 * 7) / 3                            = 23.3333  ✓
//
// The error is small, it is ALWAYS in the same direction (short), and it lands in
// closing stock as phantom material that reconciles against nothing. That is
// exactly the tolerance HANDOFF §7.5 names as how a real error hides for a year.
// And note which cases survive: every whole-multiple batch is exact under both
// forms, so a test that only tried "make 3" and "make 6" would pass on the wrong
// implementation. TestScaleComponentMultipliesBeforeDividing tries the cases that
// can tell them apart.
//
// Rounded (half-away-from-zero) rather than truncated at the end, because a
// consistent downward bias is the failure mode above and rounding at least
// distributes the residue in both directions.
func ScaleComponent(qty, wanted, output decimal.Decimal) decimal.Decimal {
	if !output.IsPositive() {
		// Unreachable via Explode (Validate refuses a non-positive yield and the
		// column CHECKs it), and returning zero rather than panicking because the
		// alternative to a clear zero here is a division-by-zero panic that the
		// Recoverer turns into an opaque 500.
		return decimal.Zero
	}
	return qty.Mul(wanted).Div(output).Round(bomScale)
}

// explosionNotes states on the artefact what a reader would otherwise have to
// know from the source. Each is computed from THESE numbers rather than being a
// claim about the software, which is the distinction HANDOFF §7.1 draws.
func explosionNotes(b Bom, wanted decimal.Decimal) []string {
	// ⛔ THE FINISHED GOOD BELONGS ON THE YIELDING SIDE OF "yields", NEVER ON THE
	// CONSUMING SIDE. This sentence used to read "%s of %s yields %s" with the
	// recipe's OUTPUT QUANTITY substituted at both ends and the finished good in
	// the middle, so a 3-yield recipe for SS Sheet 2mm printed "3 of SS Sheet 2mm
	// yields 3" — which states, on the artefact an operator reviews before issuing
	// a Stock Journal, that the thing being MADE is being CONSUMED. There is no
	// component phrase available here to put on the left: a recipe has many
	// components and they are listed underneath, so the sentence says what the
	// recipe yields and leaves the consuming side to the table.
	notes := []string{fmt.Sprintf(
		"Scaled from the recipe as written: it yields %s of %s, so this batch of %s multiplies every "+
			"component by %s/%s. The recipe is never stored per unit — that would truncate at four "+
			"decimal places and short every batch.",
		b.OutputQty.String(), b.StockItem, wanted.String(),
		wanted.String(), b.OutputQty.String())}

	notes = append(notes,
		"SINGLE-LEVEL explosion: a component that has its own bill of materials is NOT exploded "+
			"further. Issue a separate Stock Journal for it — that is what keeps work-in-progress "+
			"visible as stock rather than vanishing inside one entry.")

	notes = append(notes,
		"Nothing has moved. This is what a Stock Journal's lines WOULD be; the manufacture is not "+
			"recorded until that voucher is posted.")
	return notes
}

// ---------------------------------------------------------------------------
// Reads and error translation
// ---------------------------------------------------------------------------

func readBom(ctx context.Context, tx pgx.Tx, companyID, bomID uuid.UUID) (Bom, error) {
	var b Bom
	if err := tx.QueryRow(ctx,
		`SELECT bh.id, bh.stock_item_id, COALESCE(si.name, ''), bh.name,
		        bh.output_qty, bh.is_default
		   FROM bom_head bh
		   LEFT JOIN stock_item si
		     ON si.company_id = bh.company_id AND si.id = bh.stock_item_id
		  WHERE bh.company_id = $1 AND bh.id = $2`,
		companyID, bomID).Scan(&b.ID, &b.StockItemID, &b.StockItem, &b.Name,
		&b.OutputQty, &b.IsDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Bom{}, ErrNotFound
		}
		return Bom{}, fmt.Errorf("orders: read bom: %w", err)
	}
	rows, err := tx.Query(ctx,
		`SELECT bc.line_no, bc.stock_item_id, COALESCE(si.name, ''), bc.qty
		   FROM bom_component bc
		   LEFT JOIN stock_item si
		     ON si.company_id = bc.company_id AND si.id = bc.stock_item_id
		  WHERE bc.company_id = $1 AND bc.bom_id = $2
		  ORDER BY bc.line_no`,
		companyID, bomID)
	if err != nil {
		return Bom{}, fmt.Errorf("orders: read bom components: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Component
		if err := rows.Scan(&c.LineNo, &c.StockItemID, &c.StockItem, &c.Qty); err != nil {
			return Bom{}, fmt.Errorf("orders: scan bom component: %w", err)
		}
		b.Components = append(b.Components, c)
	}
	if err := rows.Err(); err != nil {
		return Bom{}, fmt.Errorf("orders: read bom components: %w", err)
	}
	return b, nil
}

// wrapBomWrite turns a constraint violation into the sentence an operator can act
// on. Keyed on the constraint NAME (ours, pinned by the migration) rather than on
// the message text (Postgres's, and free to change with a version) — except for
// the self-reference, which is a plpgsql RAISE and has no name to key on.
func wrapBomWrite(err error, in BomInput) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return fmt.Errorf("orders: insert bom: %w", err)
	}
	switch pg.ConstraintName {
	case "bom_head_name_unique":
		return fmt.Errorf(
			"%w: this item already has a recipe named %q — a recipe name is unique within its item "+
				"and is matched case-insensitively, so \"Standard\" and \"standard\" are the same recipe",
			ErrConflict, strings.TrimSpace(in.Name))
	case "bom_head_one_default_per_item":
		return fmt.Errorf(
			"%w: this item already has a DEFAULT recipe. Two defaults is a silent coin flip over which "+
				"one a manufacture used, and the two consume different materials — clear the other "+
				"default first, or add this one as a non-default alternative",
			ErrConflict)
	case "bom_head_item_fk", "bom_component_item_fk":
		return fmt.Errorf(
			"orders: the recipe names a stock item that does not exist in this company: %w", err)
	case "bom_component_one_per_item":
		return fmt.Errorf(
			"%w: the same item appears twice in this recipe", ErrConflict)
	}
	if isRaise(err, "cannot be a component of its own bill of materials") {
		return fmt.Errorf(
			"orders: the finished good cannot also be a component of its own recipe — exploding it "+
				"would consume the very thing it produces: %w", err)
	}
	return fmt.Errorf("orders: insert bom: %w", err)
}

func nullIfNilUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
