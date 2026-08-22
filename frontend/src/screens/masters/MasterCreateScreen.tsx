import { useCallback, useEffect, useRef, useState } from "react";
import { SelectionList } from "../SelectionList";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { MasterKind } from "../../keystroke/types";
import type {
  CreateGroupRequest,
  CreateLedgerRequest,
  CreateStockItemRequest,
  OpeningBillRequest,
  RefType,
} from "../../api";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import {
  screenRefusal,
  useRegisterScreenAction,
  type ScreenRefusal,
} from "../../app/ScreenActions";
import {
  LEDGER_PARENT_GROUPS,
  GROUP_PARENT_OPTIONS,
  GROUP_NATURES,
  type GroupNature,
} from "./groups";
// ⛔ IMPORTED, NOT RE-DERIVED. `./gst` shipped with ZERO importers — the GSTIN
// validator, the state-code-from-GSTIN helper and the registration vocabulary
// were all written, tested and dead. Re-deriving any of them here would have
// left the dead copy in place to drift.
import {
  GST_REGISTRATION_TYPES,
  GST_REGISTRATION_WIRE,
  STATE_CODES,
  isKnownStateCode,
  isValidGstin,
  isValidPan,
  panFromGstin,
  requiresGstin,
  stateCodeFromGstin,
  stateNameForCode,
  type GstRegistrationType,
} from "./gst";
import { moneyEq, normalizeMoney, subMoney, sumMoney } from "../vouchers/money";

/**
 * The master-create screen, wired to the LIVE backend. Reached two ways:
 *
 *  - INLINE, with `Alt+C` from a voucher field — creates a LEDGER on the fly and
 *    returns its name to the field that triggered it (Ctrl+A) or abandons it
 *    (Esc). This is the door that always existed.
 *  - STANDALONE, with `Alt+G` (Go To) > `Create Ledger` / `Create Group` /
 *    `Create Stock Item` / `Create Unit` / `Create Voucher Type` — the route
 *    RESEARCH.md:568 documents, added because ⛔ **NO keystroke in this build
 *    reached `masterKind` "stock-item" or "unit" at all**, so a company could
 *    never be given a stock item or a unit from the keyboard (audit row 20,
 *    DRIVEN in a browser and confirmed). See MASTER_GOTO_ENTRIES in
 *    keystroke/navMachine.ts for the route and why it is not a keymap change.
 *
 * ⛔⛔ WHAT THIS FORM USED TO BE, AND WHY THAT WAS THE DEFECT (register row D36).
 * It rendered exactly TWO inputs — Name and Under — for every kind. Consequences,
 * all driven:
 *
 *  - There was **no keyboard path to an opening balance, an opening Dr/Cr, a
 *    bill-wise opening breakup or `maintain_bill_by_bill` on ANY ledger**, even
 *    though `masters.CreateLedger` has accepted all four since it was written and
 *    REQUIRES the breakup to sum to the opening balance. The server half was
 *    complete and unreachable.
 *  - `MASTER_FIELDS`' third cursor position, `"details"`, had **no control behind
 *    it**: Enter/Tab moved the cursor onto nothing.
 *  - `PARENT_OPTIONS["stock-item"]` and `["unit"]` were `[]`, so the second field
 *    never rendered and the create call sent `unit_symbol: p || "Nos"` — **a Kg
 *    item was impossible**, and the default was invisible.
 *
 * Every field below therefore exists because a real, keyable business fact had
 * nowhere to go. Nothing here is speculative: each one is already a field of the
 * request type the client declares and the server reads.
 */

/**
 * The unit symbols offered for a stock item, `allowCreate` so ANY symbol can be
 * keyed. The list is muscle memory, not a whitelist — `masters.CreateStockItem`
 * creates the unit on demand — and it exists because an empty picker is what made
 * "Nos" the only reachable unit.
 */
const UNIT_SYMBOLS: readonly string[] = [
  "Nos",
  "Pcs",
  "Kg",
  "Gm",
  "Ton",
  "Ltr",
  "Ml",
  "Mtr",
  "Cm",
  "Sq.Ft",
  "Box",
  "Bag",
  "Bottle",
  "Dozen",
  "Pair",
  "Set",
];

/** Parent classes a user-defined voucher type can inherit from. */
const VOUCHER_TYPE_CLASSES: readonly string[] = [
  "Sales",
  "Purchase",
  "Receipt",
  "Payment",
  "Contra",
  "Journal",
];

/**
 * The reference types an OPENING bill can carry.
 *
 * ⚠️ "AGAINST" is deliberately absent even though {@link RefType} declares it: an
 * opening bill IS the reference, so there is nothing for it to be against. The
 * server defaults an empty ref_type to "NEW" (masters.CreateLedger); the picker
 * makes that default visible rather than implicit.
 */
const OPENING_REF_TYPES: readonly RefType[] = ["NEW", "ADVANCE", "ON_ACCOUNT"];

/**
 * The `account_nature` enum the server stores (migration 0002), keyed by the
 * label groups.ts offers. ⛔ THE TWO ARE NOT THE SAME STRINGS — "Assets" is a
 * label and 'ASSET' is the enum, and sending the label would be refused by
 * Postgres itself, after the request had already passed every check in Go.
 */
const NATURE_ENUM: Readonly<Record<GroupNature, string>> = {
  Assets: "ASSET",
  Liabilities: "LIABILITY",
  Income: "INCOME",
  Expenses: "EXPENSE",
};

/**
 * The `Under` option that means "no parent — this is a top-level group".
 *
 * ⛔ IT MUST NOT BE SENT AS A PARENT NAME. `masters.CreateGroup` resolves
 * `parent_name` with `SELECT ... FROM account_group WHERE name = $1`, and NO group
 * is seeded under the name "Primary" — so sending it answers "parent group
 * \"Primary\" not found" for what the operator read as the ordinary top-level
 * choice. It is translated to "no parent + an explicit nature" below, which is
 * what the server asks for.
 */
const PRIMARY = "Primary";

/** The second field (after the name) each kind carries, or null for none. */
interface SecondField {
  readonly label: string;
  readonly options: readonly string[];
  /** Offer the typed text itself when nothing matches (stock item units). */
  readonly allowCreate?: boolean;
}

const SECOND_FIELD: Readonly<Record<MasterKind, SecondField | null>> = {
  ledger: { label: "Under (group)", options: LEDGER_PARENT_GROUPS },
  group: { label: "Under (parent)", options: GROUP_PARENT_OPTIONS },
  "voucher-type": { label: "Type of voucher", options: VOUCHER_TYPE_CLASSES },
  "stock-item": { label: "Units", options: UNIT_SYMBOLS, allowCreate: true },
  // A unit has no parent at all: its own symbol IS the master.
  unit: null,
};

/** What the first field is CALLED per kind (a unit is keyed by its symbol). */
const NAME_LABEL: Readonly<Record<MasterKind, string>> = {
  ledger: "Name",
  group: "Name",
  "voucher-type": "Name",
  "stock-item": "Name",
  unit: "Symbol",
};

/** The noun used in every status sentence, so a refusal names what was refused. */
const NOUN: Readonly<Record<MasterKind, string>> = {
  ledger: "ledger",
  group: "group",
  "voucher-type": "voucher type",
  "stock-item": "stock item",
  unit: "unit",
};

/**
 * The control the `"details"` cursor position lands on, per kind.
 *
 * ⛔ WITHOUT THIS THE FIX WOULD BE HALF-BUILT, WHICH IS THE DEFECT ITSELF.
 * `MASTER_FIELDS` is `["name","group","details"]` and Enter/Tab advance that
 * cursor — but `autoFocus` is a MOUNT-time prop in React, so re-rendering the
 * same frame with `autoFocus` newly true moves nothing. Adding the fields
 * without this would leave "details" still landing on nothing, which is exactly
 * what D36 reports. The effect below runs only when the cursor MOVES, so it can
 * never steal focus from a control the operator is typing in.
 */
const DETAIL_FOCUS: Readonly<Record<MasterKind, string | null>> = {
  ledger: '[data-testid="master-opening-balance"]',
  group: '[data-testid="master-group-nature"]',
  unit: '[data-testid="master-unit-formal-name"]',
  "stock-item": '[data-testid="master-item-hsn"]',
  // A voucher type has no further keyable field — see the note it renders.
  "voucher-type": null,
};

/** One row of a ledger's bill-wise opening breakup, as keyed. */
interface BillDraft {
  ref: string;
  refType: RefType;
  amount: string;
  dueDate: string;
}

function emptyBill(): BillDraft {
  return { ref: "", refType: "NEW", amount: "", dueDate: "" };
}

/** Everything the form holds, in one object so the accept handler reads one ref. */
interface Draft {
  name: string;
  /** The second field: parent group / parent / voucher class / unit symbol. */
  parent: string;
  // ---- ledger
  openingBalance: string;
  openingDC: "D" | "C";
  maintainBillByBill: boolean;
  openingBills: BillDraft[];
  /**
   * The party block (register row D87). The server has accepted `gstin`,
   * `state_code`, `registration_type` and `pan` since they were written, and
   * this form rendered NONE of them — so every party keyed from the keyboard
   * landed `(null)|(null)|UNKNOWN`, which is the exact input that made D19
   * derive an intra-state bill as IGST.
   *
   * ⚠️ `creditDays` is a STRING here because it is what an input holds. It is
   * parsed once, in `build()`, and "" means NOT STATED — sent as an absent key
   * rather than 0, because "no term agreed" and "due immediately" are different
   * facts about a party.
   */
  gstin: string;
  stateCode: string;
  registrationType: GstRegistrationType;
  pan: string;
  creditDays: string;
  // ---- group
  nature: GroupNature | "";
  // ---- unit
  formalName: string;
  decimalPlaces: string;
  // ---- stock item
  hsn: string;
  gstRatePct: string;
  openingQty: string;
  openingRate: string;
}

function emptyDraft(): Draft {
  return {
    name: "",
    // ⛔ NO KIND GETS A SEEDED SECOND FIELD, AND THE STOCK ITEM IS WHY. The old
    // call site was `unit_symbol: p || "Nos"` over a picker with NO options, so
    // every stock item in this product became a "Nos" item and nobody chose
    // that. Seeding "Nos" visibly here would fix the display and keep the
    // silence: an operator who never looks at the field still ships a Nos item.
    // The unit is CHOSEN, and the form refuses without one (see `build`).
    parent: "",
    openingBalance: "",
    openingDC: "D",
    maintainBillByBill: false,
    openingBills: [emptyBill()],
    gstin: "",
    stateCode: "",
    // ⛔ "Unregistered", NOT the server's UNKNOWN default. UNKNOWN means
    // "nobody said", and it is exactly what D87 is about: it is a legitimate
    // state for a party nobody has looked at, and an ILLEGITIMATE one for a
    // party somebody just sat down and keyed. Defaulting to a real answer means
    // a keyed party always carries a statement — and Unregistered is the right
    // default because it is the one that charges tax normally and claims
    // nothing, so getting it wrong is the least costly way to be wrong.
    registrationType: "Unregistered",
    pan: "",
    creditDays: "",
    nature: "",
    formalName: "",
    decimalPlaces: "2",
    hsn: "",
    gstRatePct: "",
    openingQty: "",
    openingRate: "",
  };
}

/**
 * The groups whose ledgers are PARTIES, and therefore the groups a state code is
 * mandatory under (register row D87, the user's decision 2026-08-21).
 *
 * ⛔ NOT EVERY LEDGER NEEDS ONE, and that is why this is a set rather than a
 * blanket rule. A bank account, a sales account and an expense head have no
 * place of supply; demanding a state code on those would refuse most of the
 * chart of accounts to protect a rule that does not apply to it.
 */
const PARTY_GROUPS: ReadonlySet<string> = new Set([
  "Sundry Debtors",
  "Sundry Creditors",
]);

/** The bills actually worth sending (a blank spare row is not a bill). */
function keyedBills(d: Draft): BillDraft[] {
  return d.openingBills.filter(
    (b) => b.ref.trim() !== "" || normalizeMoney(b.amount) !== "0.0000",
  );
}

export function MasterCreateScreen({
  snapshot,
  onSelect,
  onCreated,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
  /** Optional sink for the created master's id (voucher field id wiring). */
  onCreated?: (result: { name: string; id: string }) => void;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const kind = (snapshot.masterKind ?? "ledger") as MasterKind;

  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [status, setStatus] = useState<string | null>(null);

  // ONE ref, updated every render, so the accept handler reads the live form
  // without re-registering itself on every keystroke (see ScreenActions.tsx).
  const draftRef = useRef(draft);
  draftRef.current = draft;

  // THE FIELD CURSOR MOVES THE FOCUS. See DETAIL_FOCUS for why an `autoFocus`
  // prop cannot do this on its own.
  const rootRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const selector =
      snapshot.field === "name"
        ? '[data-testid="master-name"]'
        : snapshot.field === "group"
          ? 'input[role="combobox"]'
          : snapshot.field === "details"
            ? DETAIL_FOCUS[kind]
            : null;
    if (!selector) return;
    root.querySelector<HTMLElement>(selector)?.focus();
  }, [snapshot.field, kind]);

  const patch = useCallback((p: Partial<Draft>) => {
    setDraft((d) => ({ ...d, ...p }));
  }, []);

  const patchBill = useCallback((i: number, p: Partial<BillDraft>) => {
    setDraft((d) => ({
      ...d,
      openingBills: d.openingBills.map((b, j) => (j === i ? { ...b, ...p } : b)),
    }));
  }, []);

  // Surface the typed/selected master name to the machine as the pending pick so
  // the accept reducer can return it to the originating voucher field.
  const publishName = useCallback(
    (n: string) => {
      if (n) onSelect(n);
    },
    [onSelect],
  );

  const second = SECOND_FIELD[kind];
  const bills = keyedBills(draft);
  const billTotal = sumMoney(bills.map((b) => b.amount));
  const billDifference = subMoney(normalizeMoney(draft.openingBalance), billTotal);

  /**
   * Build the request for `kind`, or return a REFUSAL SENTENCE.
   *
   * ⚠️ Every refusal here is at least as strict as the server's, never looser —
   * the rule §7.2 of HANDOFF-PARITY-AUDIT.md was written for peeks and which
   * applies to any client-side pre-flight. Two are deliberately STRICTER, and
   * both say so at the check.
   */
  const build = useCallback(
    (n: string, d: Draft): { req: unknown; summary: string } | string => {
      const p = d.parent.trim();
      switch (kind) {
        case "ledger": {
          if (!p) {
            return `A ledger must be filed under a group (masters.CreateLedger requires one) — nothing was created.`;
          }
          const rows = keyedBills(d);
          // ⛔ THE BREAKUP MUST ACCOUNT FOR THE WHOLE OPENING BALANCE, and the
          // reason is not tidiness: a breakup that does not add up makes the
          // Outstandings total disagree with the Balance Sheet by exactly the
          // shortfall, silently and forever. masters.CreateLedger refuses it; so
          // does this form, so the operator learns it while the figures are still
          // in front of them rather than from a status line after the frame has
          // popped.
          const send = d.maintainBillByBill ? rows : [];
          if (d.maintainBillByBill && rows.length > 0) {
            const total = sumMoney(rows.map((b) => b.amount));
            const opening = normalizeMoney(d.openingBalance);
            if (!moneyEq(total, opening)) {
              return `The bill-wise opening breakup sums to ${total} but the opening balance is ${opening} — the difference is ${subMoney(opening, total)}. Nothing was created.`;
            }
            const unnamed = rows.findIndex((b) => b.ref.trim() === "");
            if (unnamed >= 0) {
              return `Opening bill ${unnamed + 1} has an amount but no reference name — nothing was created.`;
            }
          }
          const opening_bills: OpeningBillRequest[] = send.map((b) => ({
            ref_name: b.ref.trim(),
            ref_type: b.refType,
            amount: normalizeMoney(b.amount),
            ...(b.dueDate ? { due_date: b.dueDate } : {}),
          }));
          // ---- THE PARTY BLOCK (register row D87) -----------------------
          // ⛔ EVERY REFUSAL BELOW FIRES CLIENT-SIDE, BEFORE ANY REQUEST.
          // `masters.CreateLedger` CREATES the ledger and then has nothing to
          // refuse with — it accepts a null state code happily — so a
          // server-side check is not available, and a check that ran after the
          // POST would be refusing a party that already exists.
          const gstin = d.gstin.trim().toUpperCase();
          const stateCode = d.stateCode.trim();
          const pan = d.pan.trim().toUpperCase();
          const regWire = GST_REGISTRATION_WIRE[d.registrationType];

          if (PARTY_GROUPS.has(p) && stateCode === "") {
            // ⛔ THE USER'S DECISION, AND THE ONE THAT CLOSES D19's INPUT.
            // A party with no state code makes the server fall back to the
            // COMPANY's state, so an intra-state supply from a Maharashtra
            // supplier to a Maharashtra buyer was derived as IGST for a
            // Karnataka company. It is refused here rather than defaulted,
            // because a default would be a guess about where somebody trades.
            return (
              `A party under ${p} needs a STATE CODE — it decides CGST+SGST ` +
              `versus IGST on every bill, and with none the split is derived ` +
              `from the company's own state instead. Nothing was created.`
            );
          }
          if (stateCode !== "" && !isKnownStateCode(stateCode)) {
            return (
              `“${stateCode}” is not a GST state code. Nothing was created.`
            );
          }
          if (requiresGstin(d.registrationType) && gstin === "") {
            // A Regular or Composition party BY DEFINITION holds a
            // registration, so the number is not optional once that claim is
            // made. Unregistered / Consumer / SEZ are untouched by this.
            return (
              `A ${d.registrationType} party has a GSTIN by definition — ` +
              `state it, or set the registration type to Unregistered. ` +
              `Nothing was created.`
            );
          }
          if (gstin !== "" && !isValidGstin(gstin)) {
            return (
              `“${gstin}” is not a valid GSTIN (15 characters: 2-digit state, ` +
              `10-character PAN, entity digit, “Z”, checksum). Nothing was created.`
            );
          }
          if (gstin !== "" && stateCode !== "" && stateCodeFromGstin(gstin) !== stateCode) {
            // ⛔ THE TWO MUST AGREE OR ONE OF THEM IS WRONG, and after the
            // master is saved nothing ever looks again. This is the exact
            // relationship `verify:demo` checks across the whole book.
            return (
              `The GSTIN begins ${stateCodeFromGstin(gstin)} ` +
              `(${stateNameForCode(stateCodeFromGstin(gstin)) || "unknown state"}) ` +
              `but the state code says ${stateCode} ` +
              `(${stateNameForCode(stateCode) || "unknown state"}) — one of them ` +
              `is wrong. Nothing was created.`
            );
          }
          if (pan !== "" && !isValidPan(pan)) {
            return `“${pan}” is not a valid PAN (5 letters, 4 digits, 1 letter). Nothing was created.`;
          }
          if (pan !== "" && gstin !== "" && panFromGstin(gstin) !== pan) {
            // A GSTIN is BUILT from the PAN, so when both are stated they
            // cannot disagree.
            return (
              `The GSTIN contains PAN ${panFromGstin(gstin)} but the PAN field ` +
              `says ${pan} — a GSTIN is built from the PAN, so one of them is ` +
              `wrong. Nothing was created.`
            );
          }
          // ⚠️ "" IS NOT STATED AND IS SENT AS AN ABSENT KEY, not as 0.
          // "No term agreed" and "due immediately" are different facts, and the
          // ageing report reads them differently.
          const creditRaw = d.creditDays.trim();
          let creditDays: number | undefined;
          if (creditRaw !== "") {
            const parsed = Number(creditRaw);
            if (!Number.isInteger(parsed) || parsed < 0) {
              return `Credit days must be a whole number of days (or blank for “no term agreed”) — “${creditRaw}” is not. Nothing was created.`;
            }
            creditDays = parsed;
          }

          const req: CreateLedgerRequest = {
            name: n,
            group_name: p,
            opening_balance: normalizeMoney(d.openingBalance),
            opening_dc: d.openingDC,
            maintain_bill_by_bill: d.maintainBillByBill,
            ...(opening_bills.length > 0 ? { opening_bills } : {}),
            // Sent only when stated. An empty string would OVERWRITE nothing
            // today (this is a create) but would read back as "" rather than
            // NULL, which is a different claim.
            ...(gstin !== "" ? { gstin } : {}),
            ...(stateCode !== "" ? { state_code: stateCode } : {}),
            registration_type: regWire,
            ...(pan !== "" ? { pan } : {}),
            ...(creditDays !== undefined ? { credit_days: creditDays } : {}),
          };
          const ob = normalizeMoney(d.openingBalance);
          const summary =
            ob === "0.0000"
              ? `under ${p}`
              : `under ${p}, opening ${ob} ${d.openingDC}${
                  opening_bills.length > 0
                    ? ` across ${String(opening_bills.length)} bill(s)`
                    : ""
                }`;
          return { req, summary };
        }
        case "group": {
          // "Primary" is the operator's word for "no parent". Translate it, and
          // demand the nature the server demands in that case — rather than
          // sending a parent name that resolves to nothing.
          const isPrimary = p === "" || p === PRIMARY;
          if (isPrimary && !d.nature) {
            return `A primary group needs a nature (Assets / Liabilities / Income / Expenses) — a sub-group inherits its parent's, but a top-level group has none to inherit. Nothing was created.`;
          }
          const nature = d.nature ? NATURE_ENUM[d.nature] : "";
          const req: CreateGroupRequest = {
            name: n,
            ...(isPrimary ? {} : { parent_name: p }),
            ...(isPrimary
              ? {
                  nature,
                  // is_pl follows the nature: a group of income or expense
                  // closes into the P&L, one of assets or liabilities does not.
                  is_pl: nature === "INCOME" || nature === "EXPENSE",
                }
              : {}),
          };
          return {
            req,
            summary: isPrimary ? `as a primary ${d.nature} group` : `under ${p}`,
          };
        }
        case "voucher-type": {
          if (!p) {
            return `A voucher type must name the class it behaves as — nothing was created.`;
          }
          return { req: { name: n, parent_class: p }, summary: `behaving as ${p}` };
        }
        case "unit": {
          const dp = Number.parseInt(d.decimalPlaces, 10);
          if (!Number.isFinite(dp) || dp < 0 || dp > 4) {
            return `Decimal places must be 0 to 4 (money and quantity are stored fixed-4) — nothing was created.`;
          }
          return {
            req: {
              symbol: n,
              ...(d.formalName.trim() ? { formal_name: d.formalName.trim() } : {}),
              decimal_places: dp,
            },
            summary: dp === 0 ? "whole units" : `${String(dp)} decimal places`,
          };
        }
        case "stock-item": {
          // ⛔ STRICTER THAN THE SERVER, ON PURPOSE. `masters.CreateStockItem`
          // writes `unit_id NULL` for an empty `unit_symbol` with no CHECK and no
          // refusal, and the ONLY thing that ever notices is the e-invoice
          // builder (einvoice.go:711, ErrNoUnitOfMeasure) — by which time the
          // invoice is already posted and the statutory document cannot be
          // produced for it. Refusing here costs one sentence; not refusing costs
          // an unfilable invoice.
          if (!p) {
            return `A stock item needs a unit of measure — without one it can be keyed onto an invoice that then cannot be e-invoiced at all. Nothing was created.`;
          }
          const req: CreateStockItemRequest = {
            name: n,
            unit_symbol: p,
            ...(d.hsn.trim() ? { hsn: d.hsn.trim() } : {}),
            ...(d.gstRatePct.trim()
              ? { gst_rate_pct: normalizeMoney(d.gstRatePct) }
              : {}),
            ...(normalizeMoney(d.openingQty) !== "0.0000"
              ? { opening_qty: normalizeMoney(d.openingQty) }
              : {}),
            ...(normalizeMoney(d.openingRate) !== "0.0000"
              ? { opening_rate: normalizeMoney(d.openingRate) }
              : {}),
          };
          const qty = normalizeMoney(d.openingQty);
          return {
            req,
            summary:
              qty === "0.0000"
                ? `in ${p}`
                : `in ${p}, opening ${qty} @ ${normalizeMoney(d.openingRate)}`,
          };
        }
        default:
          return `This build cannot create a ${String(kind)}.`;
      }
    },
    [kind],
  );

  const create = useCallback(
    async (req: unknown): Promise<{ id: string }> => {
      switch (kind) {
        case "ledger": {
          const r = await client.createLedger(companyID, req as CreateLedgerRequest);
          return { id: r.ledger_id };
        }
        case "group": {
          const r = await client.createGroup(companyID, req as CreateGroupRequest);
          return { id: r.group_id };
        }
        case "voucher-type": {
          const r = await client.createVoucherType(
            companyID,
            req as { name: string; parent_class: string },
          );
          return { id: r.voucher_type_id };
        }
        case "unit": {
          const r = await client.createUnit(companyID, req as { symbol: string });
          return { id: r.unit_id };
        }
        case "stock-item": {
          const r = await client.createStockItem(
            companyID,
            req as CreateStockItemRequest,
          );
          return { id: r.stock_item_id };
        }
        default:
          throw new Error(`unknown master kind ${String(kind)}`);
      }
    },
    [client, companyID, kind],
  );

  /**
   * Ctrl+A.
   *
   * ⛔⛔ IT RETURNS ITS OUTCOME, AND THAT RETURN IS THE WHOLE OF REGISTER ROW D47.
   * This handler used to be `void`: it fired `create(...)` and reported the result
   * with `setStatus` — into a screen the machine had ALREADY POPPED. Driven
   * 2026-08-07: `Ctrl+A` on Create ledger returned the operator to the Gateway
   * **with no message at all** while `POST /ledgers` answered **403** and nothing
   * was created. A refusal that reaches nobody is indistinguishable from a
   * success, and the operator's next act is to key the voucher that needs the
   * master they think they just made.
   *
   * `ScreenActionHandler` may return a `string` OR a `Promise<string>`, and
   * AppShell awaits the promise and puts whatever arrives on the STATUS LINE —
   * which lives in the shell and therefore survives this component's unmount.
   * That surface was built for exactly this ("an async failure can never be
   * silent"); nothing here needed inventing, it needed USING.
   *
   * ⚠️ WHAT IT STILL CANNOT UNDO. `onSelect(n)` must run SYNCHRONOUSLY — the
   * machine's accept reducer runs on the very next line of the shell's keydown
   * handler, and the pending pick is how an inline `Alt+C` pre-fills the voucher
   * field that triggered it. So on a REFUSED inline create the field now names a
   * master that does not exist. The message says so, in those words, and only
   * when the parent really is a voucher.
   */
  const onAccept = useCallback((): ScreenRefusal | Promise<string> => {
    const d = draftRef.current;
    const noun = NOUN[kind];
    // ⛔ A REFUSAL RETURNS A MARKER, NOT A MESSAGE STRING — register row D92.
    // A plain string is what a SUCCESS returns, so the shell could not tell the
    // two apart and forwarded the key to the machine either way. The machine's
    // `accept` reducer pops this frame unconditionally, so a refusal used to
    // throw the operator back to the Gateway and DISCARD everything they had
    // keyed, then show the reason on a screen they could no longer act on.
    //
    // Every refusal below is now non-destructive: the form stays, the figures
    // stay, the cursor stays, and the status line says what is wrong. That is
    // what the breakup refusal's own comment already CLAIMED ("so the operator
    // learns it while the figures are still in front of them") and, until this,
    // was not true of.
    const refuse = (message: string): ScreenRefusal => {
      setStatus(message);
      return screenRefusal(message);
    };

    const n = d.name.trim();
    if (!n) {
      return refuse(
        `A ${noun} needs a ${NAME_LABEL[kind].toLowerCase()} — nothing was created.`,
      );
    }
    const built = build(n, d);
    if (typeof built === "string") return refuse(built);

    // Set the machine's pending pick synchronously so the accept reducer (which
    // runs immediately after this handler) returns it to the parent voucher.
    onSelect(n);

    const inlineFromVoucher = snapshot.parentScreen === "voucher";
    return create(built.req)
      .then((res) => {
        const message = `Created ${noun} “${n}” ${built.summary}.`;
        setStatus(message);
        if (onCreated) onCreated({ name: n, id: res.id });
        return message;
      })
      .catch((err: unknown) => {
        const why = err instanceof Error ? err.message : String(err);
        const message =
          `Could not create ${noun} “${n}”: ${why} — nothing was created.` +
          (inlineFromVoucher
            ? ` The field that asked for it now names “${n}”; clear it before saving the voucher.`
            : "");
        setStatus(message);
        return message;
      });
  }, [build, create, kind, onCreated, onSelect, snapshot.parentScreen]);

  useRegisterScreenAction("accept", onAccept);

  /** True when the field cursor is parked on the details block. */
  const onDetails = snapshot.field === "details";

  return (
    <section
      ref={rootRef}
      className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field"
      data-testid={`master-create:${kind}`}
    >
      <h1 className="text-lg font-bold text-tally-title">
        Create {kind.replace("-", " ")}
      </h1>
      <div className="mt-3 max-w-md bg-tally-panel p-3">
        <label className="block text-tally-field">
          {NAME_LABEL[kind]}
          <input
            aria-label={NAME_LABEL[kind]}
            data-testid="master-name"
            className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={draft.name}
            onChange={(e) => {
              patch({ name: e.target.value });
            }}
            onBlur={() => publishName(draftRef.current.name)}
            autoFocus={snapshot.field === "name"}
          />
        </label>

        {second ? (
          <div className="mt-2">
            <SelectionList
              label={second.label}
              options={second.options}
              value={draft.parent}
              allowCreate={second.allowCreate ?? false}
              onSelect={(item) => patch({ parent: item })}
              autoFocus={snapshot.field === "group"}
            />
          </div>
        ) : null}
        {draft.parent ? (
          <p className="mt-1 text-xs" data-testid="master-parent-echo">
            {second?.label ?? "Under"}: {draft.parent}
          </p>
        ) : null}

        {/* ---- THE DETAILS BLOCK: the MASTER_FIELDS "details" cursor position,
            which had no control behind it on any kind before this. */}
        {kind === "ledger" ? (
          <div className="mt-3 border-t border-tally-field/30 pt-2">
            <label className="block text-tally-field">
              Opening balance
              <input
                aria-label="Opening balance"
                data-testid="master-opening-balance"
                inputMode="decimal"
                className="ml-2 w-32 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                value={draft.openingBalance}
                onChange={(e) => {
                  patch({ openingBalance: e.target.value });
                }}
                autoFocus={onDetails}
              />
            </label>
            {/* Dr/Cr is a CHOICE and never a default the operator cannot see:
                a capital account opening on the wrong side reverses the Balance
                Sheet and still balances to the paisa. */}
            <fieldset className="mt-2">
              <legend className="text-xs">Opening Dr / Cr</legend>
              {(["D", "C"] as const).map((dc) => (
                <label key={dc} className="mr-3 text-xs">
                  <input
                    type="radio"
                    name="opening-dc"
                    data-testid={`master-opening-dc-${dc}`}
                    className="mr-1"
                    checked={draft.openingDC === dc}
                    onChange={() => {
                      patch({ openingDC: dc });
                    }}
                  />
                  {dc === "D" ? "Dr (debit)" : "Cr (credit)"}
                </label>
              ))}
            </fieldset>
            {/* ---- THE PARTY BLOCK (register row D87) ----------------------
                ⛔ IT SITS AFTER THE OPENING BALANCE, NOT BEFORE IT. `DETAIL_FOCUS`
                lands the "details" cursor on `master-opening-balance`, and
                putting these fields ahead of it would move where that cursor
                arrives — which is D36's whole lesson, arrived at from the other
                direction. The opening balance is also what the operator is most
                often here for; the party block is the thing they fill in once.

                ⚠️ IT RENDERS FOR EVERY LEDGER KIND, not only parties. A GSTIN on
                a bank ledger is meaningless but harmless, whereas hiding the
                block unless the group is a party group would mean the fields
                vanish and reappear as the operator moves the group cursor — and
                anything already keyed would be lost with them. Only the
                REFUSAL is party-specific. */}
            <div className="mt-3 border-t border-tally-field/30 pt-2" data-testid="master-party-block">
              <p className="text-xs">
                GST identity —{" "}
                <span className="text-tally-title">
                  a state code is REQUIRED under Sundry Debtors and Sundry
                  Creditors
                </span>
                : it decides CGST+SGST versus IGST on every bill, and with none
                the split is derived from this company&apos;s own state instead.
              </p>
              <label className="mt-2 block text-xs">
                State code
                <select
                  aria-label="State code"
                  data-testid="master-state-code"
                  className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
                  value={draft.stateCode}
                  onChange={(e) => {
                    patch({ stateCode: e.target.value });
                  }}
                >
                  {/* "" is offered so a NON-party ledger can leave it unset —
                      the refusal, not the control, is what makes it mandatory
                      where it is mandatory. */}
                  <option value="">— not stated —</option>
                  {STATE_CODES.map((st) => (
                    <option key={st.code} value={st.code}>
                      {st.code} {st.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="mt-2 block text-xs">
                Registration type
                <select
                  aria-label="Registration type"
                  data-testid="master-registration-type"
                  className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
                  value={draft.registrationType}
                  onChange={(e) => {
                    patch({
                      registrationType: e.target.value as GstRegistrationType,
                    });
                  }}
                >
                  {/* ⛔ FIVE, AND `SEZ` IS THE FIFTH. The vocabulary this reads
                      from carried four for its whole life while the server's
                      enum had six, so a picker built from it made SEZ
                      unreachable — and a supply to an SEZ unit is ZERO-RATED,
                      so an SEZ party keyed Regular is charged tax it does not
                      owe. `UNKNOWN` is deliberately absent: it is the absence
                      of a statement, not one an operator can make. */}
                  {GST_REGISTRATION_TYPES.map((rt) => (
                    <option key={rt} value={rt}>
                      {rt}
                    </option>
                  ))}
                </select>
              </label>
              <label className="mt-2 block text-xs">
                GSTIN
                <input
                  aria-label="GSTIN"
                  data-testid="master-gstin"
                  className="ml-2 w-48 bg-tally-field px-1 uppercase text-tally-fieldText outline-none"
                  value={draft.gstin}
                  placeholder="29AACCA5678L1Z3"
                  onChange={(e) => {
                    patch({ gstin: e.target.value });
                  }}
                />
              </label>
              <label className="mt-2 block text-xs">
                PAN
                <input
                  aria-label="PAN"
                  data-testid="master-pan"
                  className="ml-2 w-32 bg-tally-field px-1 uppercase text-tally-fieldText outline-none"
                  value={draft.pan}
                  placeholder="AACCA5678L"
                  onChange={(e) => {
                    patch({ pan: e.target.value });
                  }}
                />
              </label>
              <label className="mt-2 block text-xs">
                Credit days
                <input
                  aria-label="Credit days"
                  data-testid="master-credit-days"
                  inputMode="numeric"
                  className="ml-2 w-20 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                  value={draft.creditDays}
                  placeholder="blank"
                  onChange={(e) => {
                    patch({ creditDays: e.target.value });
                  }}
                />
                {/* ⚠️ BLANK IS A REAL ANSWER AND IT IS NOT ZERO. "No term
                    agreed" and "due immediately" age differently. */}
                <span className="ml-2 text-tally-bar">
                  blank = no term agreed (not “due immediately”)
                </span>
              </label>
            </div>

            <label className="mt-2 block text-xs">
              <input
                type="checkbox"
                data-testid="master-billwise"
                className="mr-1"
                checked={draft.maintainBillByBill}
                onChange={(e) => {
                  patch({ maintainBillByBill: e.target.checked });
                }}
              />
              Maintain balances bill-by-bill
            </label>
            {/* ⛔ WITHOUT THIS FLAG THE PARTY CAN NEVER CARRY A BILL REFERENCE,
                which is audit row 5's headline: an Alt+C party was created with
                maintain_bill_by_bill = false and every later attempt to allocate
                an invoice against it had nothing to allocate to. */}
            {draft.maintainBillByBill ? (
              <div className="mt-2" data-testid="master-opening-bills">
                <p className="text-xs">
                  Bill-wise breakup of the opening balance — it must ADD UP to it,
                  or Outstandings and the Balance Sheet disagree by the difference.
                </p>
                {draft.openingBills.map((b, i) => (
                  <div key={i} className="mt-1 flex flex-wrap items-center gap-1">
                    <input
                      aria-label={`Opening bill ${i + 1} reference`}
                      data-testid={`master-bill-${i}-ref`}
                      placeholder="Ref name"
                      className="w-32 bg-tally-field px-1 text-tally-fieldText outline-none"
                      value={b.ref}
                      onChange={(e) => {
                        patchBill(i, { ref: e.target.value });
                      }}
                    />
                    <select
                      aria-label={`Opening bill ${i + 1} type`}
                      data-testid={`master-bill-${i}-type`}
                      className="bg-tally-field px-1 text-tally-fieldText"
                      value={b.refType}
                      onChange={(e) => {
                        patchBill(i, { refType: e.target.value as RefType });
                      }}
                    >
                      {OPENING_REF_TYPES.map((t) => (
                        <option key={t} value={t}>
                          {t}
                        </option>
                      ))}
                    </select>
                    <input
                      aria-label={`Opening bill ${i + 1} amount`}
                      data-testid={`master-bill-${i}-amount`}
                      inputMode="decimal"
                      placeholder="Amount"
                      className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                      value={b.amount}
                      onChange={(e) => {
                        patchBill(i, { amount: e.target.value });
                      }}
                    />
                    <input
                      aria-label={`Opening bill ${i + 1} due date`}
                      data-testid={`master-bill-${i}-due`}
                      type="date"
                      className="bg-tally-field px-1 text-tally-fieldText outline-none"
                      value={b.dueDate}
                      onChange={(e) => {
                        patchBill(i, { dueDate: e.target.value });
                      }}
                    />
                  </div>
                ))}
                <button
                  type="button"
                  data-testid="master-bill-add"
                  className="mt-1 bg-tally-field px-2 text-xs text-tally-fieldText"
                  onClick={() => {
                    setDraft((d) => ({
                      ...d,
                      openingBills: [...d.openingBills, emptyBill()],
                    }));
                  }}
                >
                  + another bill
                </button>
                {/* The running DIFFERENCE, shown while the figures are still on
                    screen. The refusal is the server's rule; this is arithmetic,
                    so the operator never has to discover it from a status line
                    after the frame has popped. */}
                <p className="mt-1 text-xs" data-testid="master-bill-difference">
                  Breakup {billTotal} of {normalizeMoney(draft.openingBalance)} —{" "}
                  {billDifference === "0.0000"
                    ? "it adds up"
                    : `${billDifference} unaccounted`}
                </p>
              </div>
            ) : null}
          </div>
        ) : null}

        {kind === "group" ? (
          <div className="mt-3 border-t border-tally-field/30 pt-2">
            <label className="block text-xs">
              Nature (a top-level group only — a sub-group inherits its parent's)
              <select
                aria-label="Nature"
                data-testid="master-group-nature"
                className="ml-2 bg-tally-field px-1 text-tally-fieldText"
                value={draft.nature}
                onChange={(e) => {
                  patch({ nature: e.target.value as GroupNature | "" });
                }}
                autoFocus={onDetails}
              >
                <option value="">—</option>
                {GROUP_NATURES.map((nat) => (
                  <option key={nat} value={nat}>
                    {nat}
                  </option>
                ))}
              </select>
            </label>
          </div>
        ) : null}

        {kind === "unit" ? (
          <div className="mt-3 border-t border-tally-field/30 pt-2">
            <label className="block text-tally-field">
              Formal name
              <input
                aria-label="Formal name"
                data-testid="master-unit-formal-name"
                className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={draft.formalName}
                onChange={(e) => {
                  patch({ formalName: e.target.value });
                }}
                autoFocus={onDetails}
              />
            </label>
            <label className="mt-2 block text-tally-field">
              Decimal places
              <input
                aria-label="Decimal places"
                data-testid="master-unit-decimals"
                inputMode="numeric"
                className="ml-2 w-12 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                value={draft.decimalPlaces}
                onChange={(e) => {
                  patch({ decimalPlaces: e.target.value });
                }}
              />
            </label>
          </div>
        ) : null}

        {kind === "stock-item" ? (
          <div className="mt-3 border-t border-tally-field/30 pt-2">
            <label className="block text-tally-field">
              HSN / SAC
              <input
                aria-label="HSN / SAC"
                data-testid="master-item-hsn"
                className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={draft.hsn}
                onChange={(e) => {
                  patch({ hsn: e.target.value });
                }}
                autoFocus={onDetails}
              />
            </label>
            <label className="mt-2 block text-tally-field">
              GST rate %
              <input
                aria-label="GST rate %"
                data-testid="master-item-gst-rate"
                inputMode="decimal"
                className="ml-2 w-20 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                value={draft.gstRatePct}
                onChange={(e) => {
                  patch({ gstRatePct: e.target.value });
                }}
              />
            </label>
            <label className="mt-2 block text-tally-field">
              Opening quantity
              <input
                aria-label="Opening quantity"
                data-testid="master-item-opening-qty"
                inputMode="decimal"
                className="ml-2 w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                value={draft.openingQty}
                onChange={(e) => {
                  patch({ openingQty: e.target.value });
                }}
              />
            </label>
            <label className="mt-2 block text-tally-field">
              Opening rate
              <input
                aria-label="Opening rate"
                data-testid="master-item-opening-rate"
                inputMode="decimal"
                className="ml-2 w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                value={draft.openingRate}
                onChange={(e) => {
                  patch({ openingRate: e.target.value });
                }}
              />
            </label>
          </div>
        ) : null}

        {kind === "voucher-type" && onDetails ? (
          // ⚠️ THE ONE KIND WITH NOTHING ON THE DETAILS CURSOR, AND IT SAYS SO.
          // `CreateVoucherTypeRequest` also declares numbering_method,
          // affects_stock and is_accounting — but numbering_method's vocabulary
          // is a server-side enum this client has no list of (masters.go only
          // shows it defaults to "AUTOMATIC"), and offering a guessed second
          // value would be inventing a value the server may refuse. A cursor
          // position that lands on a stated "nothing here" is honest; one that
          // lands on nothing at all is the D36 defect repeated.
          <p className="mt-3 text-xs" data-testid="master-no-details">
            No further fields — the numbering method defaults to AUTOMATIC.
          </p>
        ) : null}

        {status ? (
          <p
            className="mt-2 text-xs text-tally-active"
            role="status"
            data-testid="master-status"
          >
            {status}
          </p>
        ) : null}
        <p className="mt-2 text-xs">Ctrl+A to accept and return; Esc to cancel.</p>
      </div>
    </section>
  );
}
