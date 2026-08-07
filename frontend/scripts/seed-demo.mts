/**
 * SEED A DEMO COMPANY — one company whose books exercise every feature the UI
 * can show, so the product can be walked end to end without hunting for data.
 *
 *   cd frontend && pnpm seed:demo            # against a running cored (:8087)
 *   cd frontend && pnpm seed:demo --insightd # also queue a Bill Inbox draft
 *
 * It prints a URL that opens the UI ON THIS COMPANY (`?company=<id>`, which the
 * shell persists), plus a summary of what was posted and which keystroke shows it.
 *
 * WHAT IT BUILDS (and why each row is there):
 *   - Masters: two customers (one Karnataka, one Maharashtra) and two suppliers
 *     (ditto), sales/purchase/expense ledgers, a bank, and the six GST tax
 *     ledgers — so BOTH the intra-state (CGST+SGST) and inter-state (IGST)
 *     derivations appear in the same books.
 *   - Opening capital, so the Balance Sheet has an equity side.
 *   - Sales + purchases across April–July, so the F2 period picker, the Day Book
 *     range and the cash-flow buckets all have something to move.
 *   - BILL-WISE references on every party voucher and a PART payment against
 *     one, so Outstandings shows both a fully open bill and a partly-settled one
 *     (which is what makes the ageing slabs non-trivial).
 *   - One ALTERED voucher (version 2) and one DELETED voucher (a tombstone), so
 *     the append-only edit trail is visible in the data even though this build
 *     has no Edit Log viewer yet.
 *   - Cash/bank contra + an expense journal, so all six voucher types (F4–F9)
 *     have real entries behind them.
 *
 * Money is fixed-4 decimal STRINGS end to end. Every voucher is posted through
 * the same REST client the UI uses — nothing is written to the database directly,
 * so the seed inherits every server-side invariant (balanced postings, the GST
 * re-derivation, server numbering).
 */

import { writeFileSync } from "node:fs";

import { createRestClient, createInsightdClient } from "../src/api";
import type {
  CoredClient,
  InventoryLineRequest,
  PostingRequest,
  VoucherRequest,
} from "../src/api";
// ⛔ THE TWO ORDINALS THIS FILE MUST NOT WRITE DOWN. A cost allocation's
// posting_seq is 0-based, a fulfilment's line_no is 1-based, and both are wrong
// SILENTLY: the voucher balances, the sum trigger is satisfied and the reports
// foot either way. costSplitOn derives the sequence from the LEDGER and
// fulfilmentOn derives the line number from the ITEM, so nothing below counts.
// See src/seed/demoLinks.ts, whose tests are mutation-proved against the inputs
// that distinguish the right ordinal from the plausible wrong one.
import { costSplitOn, fulfilmentOn } from "../src/seed/demoLinks";
import { DEMO_PASSWORD, ensurePassword } from "./auth.mjs";

const baseUrl =
  process.env.CORED_BASE_URL ??
  process.env.VITE_CORED_BASE_URL ??
  "http://localhost:8087";
const insightdUrl = process.env.INSIGHTD_BASE_URL ?? "http://localhost:8088";
const uiUrl = process.env.UI_BASE_URL ?? "http://localhost:5173";
const withInsightd = process.argv.includes("--insightd");

/** Karnataka = the company's own state; Maharashtra = the inter-state case. */
const HOME_STATE = "29";
const OTHER_STATE = "27";
/** Delhi — the second inter-state case, and what "Delhi Parts" actually means. */
const DELHI_STATE = "07";

/**
 * HSN codes. GSTR-1 carries a mandatory HSN summary, and the previous demo
 * emitted `"hsn_sc": ""` for every line because nothing supplied one. Rate and
 * HSN are correlated in reality, so the 12% line uses a different code from the
 * 18% ones rather than reusing one everywhere.
 */
const HSN_STEEL_ARTICLES = "7326"; // other articles of iron/steel — 18%
const HSN_STEEL_KITCHEN = "7323"; // table/kitchen articles of iron/steel — 12%
const HSN_FLAT_ROLLED = "7208"; // flat-rolled iron/steel — 18% (purchases)
// Stainless flat-rolled, carried by the Kg-denominated stock item. It is a
// DIFFERENT chapter heading from HSN_FLAT_ROLLED on purpose: the two stock
// items must not be distinguishable only by their name, or a report that
// grouped by HSN would fold them together and nobody would notice.
const HSN_STAINLESS_FLAT = "7219"; // flat-rolled stainless steel — 18%

/**
 * The filing company's own GSTIN. Synthetic, but STRUCTURALLY correct, because
 * the shape carries meaning we rely on elsewhere:
 *   29        two-digit state code — must equal HOME_STATE
 *   AACCD1234K  PAN (5 letters, 4 digits, 1 letter)
 *   1         entity code for this PAN in this state
 *   Z         fixed
 *   8         check character
 * Nothing validates GSTINs server-side today, so the discipline has to live
 * here: every GSTIN in this file starts with the state code of the ledger it
 * belongs to, and the PARTIES guard loop below proves it before any voucher
 * is posted.
 */
const COMPANY_GSTIN = "29AACCD1234K1Z8";

/**
 * The seed MUST run as the same subject the dev browser authenticates as
 * (`src/App.tsx` devAuthedFetch), because company membership is per-subject:
 * seeded under any other subject, the browser's user is not a member, cored
 * answers 403, and the shell falls back to minting its own empty company — so
 * the demo data would appear to "not work". Override with SEED_SUBJECT when
 * pointing the UI at a different identity.
 */
const SEED_SUBJECT = process.env.SEED_SUBJECT ?? "browser-dev-owner";

const money = (n: number): string => n.toFixed(4);

/** Mint an Owner token for {@link SEED_SUBJECT} (amr webauthn satisfies step-up). */
async function seedAuthHeaders(): Promise<Record<string, string>> {
  const res = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: SEED_SUBJECT, amr: ["webauthn"] }),
  });
  if (!res.ok) {
    throw new Error(
      `mint token for "${SEED_SUBJECT}" failed: ${res.status} ${await res.text()}` +
        " (is cored running with AUTH_DEV_TOKEN_ENABLED=true?)",
    );
  }
  const body = (await res.json()) as { token?: string };
  if (!body.token) throw new Error("mint token: no token in response");
  return { Authorization: `Bearer ${body.token}` };
}

async function main(): Promise<void> {
  const headers = await seedAuthHeaders();
  const client: CoredClient = createRestClient({ baseUrl, headers });

  // ---- The Owner gets a REAL PASSWORD ------------------------------------
  // Until now the browser authenticated as a hardcoded dev subject, so every
  // session was the same person and the whole roles/levels story was invisible
  // per-user. The demo is only walkable end-to-end if the Owner can actually
  // SIGN IN, so the seed establishes the credential the login screen expects.
  //
  // Idempotent (see ensurePassword) and self-proving: it logs in afterwards, so
  // the line printed below can never advertise a password that does not work.
  //
  // NOTE the seeder keeps using its /auth/token Owner token for every WRITE
  // below. A login token carries no `amr`, and the Owner level requires WebAuthn
  // step-up for writes, so seeding through the login token would 403.
  const ownerLogin = await ensurePassword(baseUrl, SEED_SUBJECT);
  console.log(
    `\n  LOGIN:    ${SEED_SUBJECT} / ${DEMO_PASSWORD}  ` +
      `(verified: POST /auth/login -> 200, user id ${ownerLogin.user.id})`,
  );

  const stamp = new Date().toISOString().slice(0, 10);
  const company = await client.createCompany({
    name: "Demo Traders",
    fy_start_date: "2026-04-01",
    // BOOKS BEGINNING FROM is a SEPARATE date from the financial-year start.
    // They are equal here because Demo Traders starts fresh at the top of its
    // year; a business migrating in on 1 October would keep fy_start_date
    // 2026-04-01 and set this to 2026-10-01, and its opening balances would be
    // stated as at THAT date. Set explicitly rather than left to default so the
    // demo shows the field exists and what it means.
    books_beginning_from: "2026-04-01",
    base_currency: "INR",
    currency_symbol: "₹",
    currency_formal_name: "Indian Rupees",
    decimal_places: 2,
    state_code: HOME_STATE,
    // The FILER's own GSTIN — the top-level "gstin" of every GSTR-1 we emit.
    // Without it the return has no filer identity at all. Synthetic but
    // correctly structured: 29 (Karnataka) + PAN-shaped AACCD1234K + entity "1"
    // + "Z" + check char.
    gstin: COMPANY_GSTIN,
    mailing_name: "Demo Traders",
    address: "14, 3rd Cross, Industrial Layout\nKoramangala",
    city: "Bengaluru",
    pincode: "560095",
    country: "India",
  });
  const co = company.company_id;
  console.log(`\nDemo Traders  (created ${stamp})\n  company id: ${co}`);
  // The id in MACHINE-READABLE form as well as in the log line. `pnpm verify:demo`
  // takes the company as argv[2] and `pnpm seed:tenancy` takes it as argv[2] too,
  // so until this file existed the only way to chain the steps was to eyeball the
  // line above and paste it — which is why `make reset-dev-db` could not have a
  // `make seed-all` sibling. Written next to the script, gitignored.
  writeFileSync(new URL("../.demo-company-id", import.meta.url), co);

  // ---- Masters -------------------------------------------------------------
  const ledger = async (name: string, group: string, stateCode?: string) => {
    const res = await client.createLedger(co, {
      name,
      group_name: group,
      ...(stateCode ? { state_code: stateCode } : {}),
    });
    return res.ledger_id;
  };

  /**
   * A trading party. Everything about its GST identity is derived from ONE
   * record, so the three things that must agree cannot drift apart:
   *   - the ledger's state_code,
   *   - the first two digits of its GSTIN,
   *   - the place-of-supply on every voucher raised against it.
   * The previous seed kept these as independent literals and they DID drift:
   * "Delhi Parts" was created with state 27 (Maharashtra) while its purchase
   * was posted with place-of-supply 07 (Delhi). Both are non-Karnataka, so IGST
   * came out right BY LUCK and nothing complained.
   */
  interface Party {
    name: string;
    group: "Sundry Debtors" | "Sundry Creditors";
    /** Two-digit GST state code. Drives intra (CGST+SGST) vs inter (IGST). */
    state: string;
    /** Must begin with `state`; asserted below. */
    gstin: string;
    /**
     * THE PARTY'S POSTAL ADDRESS, and it is REQUIRED — deliberately not
     * optional.
     *
     * Rule 46(d) of the CGST Rules 2017 makes the recipient's name, ADDRESS and
     * GSTIN mandatory particulars of a tax invoice, so internal/printing refuses
     * to render an invoice whose recipient address is blank. Before migration
     * 0039 the party master had nowhere to keep one, and the consequence was
     * that THE DEMO COMPANY COULD NOT PRINT A SINGLE ONE OF ITS OWN INVOICES:
     * every sales voucher this script posts answered 4xx on print.
     *
     * Required rather than optional because `satisfies Record<string, Party>`
     * below then makes it a TYPE ERROR to add a demo party without an address —
     * i.e. it is impossible to reintroduce the unprintable demo by forgetting a
     * field. `address` is a NEWLINE-SEPARATED BLOCK (the same shape as the
     * company's own, so printingdb splits both with one helper), which is why
     * these are multi-line: a one-line address would not exercise the split.
     */
    postal: { address: string; city: string; pincode: string };
    /**
     * OPENING BALANCE as at books_beginning_from, with its BILL-WISE BREAKUP.
     * In Tally a party's opening balance is never a lump sum: it is the specific
     * unpaid invoices carried in from the old books, each with its own due date.
     * Without the breakup every carried-forward rupee would age identically and
     * the Outstandings report would be a fiction on day one.
     * `bills` MUST sum to `amount` — the server refuses otherwise.
     */
    opening?: {
      amount: number;
      dc: "D" | "C";
      bills: { ref: string; amount: number; dueDate: string }[];
    };
  }

  const PARTIES = {
    acme: {
      name: "Acme Traders",
      group: "Sundry Debtors",
      state: HOME_STATE, // 29 Karnataka -> intra-state -> CGST + SGST
      gstin: "29AACCA5678L1Z3",
      postal: {
        address: "No. 42, 3rd Cross, Gandhi Bazaar\nBasavanagudi",
        city: "Bengaluru",
        pincode: "560004",
      },
      // Carried in owing two invoices from the previous books.
      opening: {
        amount: 120000,
        dc: "D",
        bills: [
          { ref: "OB/ACME/24-25/07", amount: 70000, dueDate: "2026-04-20" },
          { ref: "OB/ACME/24-25/11", amount: 50000, dueDate: "2026-05-05" },
        ],
      },
    },
    mumbai: {
      name: "Mumbai Metals",
      group: "Sundry Debtors",
      state: OTHER_STATE, // 27 Maharashtra -> inter-state -> IGST
      gstin: "27AACCM9012M1Z7",
      postal: {
        address: "Unit 7, Kamala Mills Compound\nSenapati Bapat Marg, Lower Parel",
        city: "Mumbai",
        pincode: "400013",
      },
    },
    steel: {
      name: "Steel Suppliers",
      group: "Sundry Creditors",
      state: HOME_STATE, // 29 Karnataka -> intra-state -> CGST + SGST
      gstin: "29AACCS3456N1Z1",
      postal: {
        address: "Shed 11, Peenya Industrial Area\n2nd Stage, Tumkur Road",
        city: "Bengaluru",
        pincode: "560058",
      },
      // We carried in owing this supplier two bills.
      opening: {
        amount: 68000,
        dc: "C",
        bills: [
          { ref: "OB/STEEL/24-25/42", amount: 40000, dueDate: "2026-04-15" },
          { ref: "OB/STEEL/24-25/55", amount: 28000, dueDate: "2026-05-10" },
        ],
      },
    },
    pune: {
      name: "Pune Fabricators",
      group: "Sundry Creditors",
      // 27 Maharashtra. This is the supplier on the Bill Inbox OCR draft, and
      // the reason that draft must propose IGST: 27 -> 29 is INTER-state.
      state: OTHER_STATE,
      gstin: "27AACCP2345Q1Z9",
      postal: {
        address: "Gat No. 218, Bhosari MIDC\nPimpri-Chinchwad",
        city: "Pune",
        pincode: "411026",
      },
    },
    delhi: {
      name: "Delhi Parts",
      group: "Sundry Creditors",
      // 07 DELHI — matches the name, and matches the place-of-supply its
      // purchase is posted with. Still != 29, so IGST continues to apply.
      state: DELHI_STATE,
      gstin: "07AACCD7890P1Z5",
      postal: {
        address: "B-19, Naraina Industrial Area\nPhase I",
        city: "New Delhi",
        pincode: "110028",
      },
    },
  } as const satisfies Record<string, Party>;

  // Fail LOUD before any voucher is posted if a GSTIN and its state disagree.
  // A wrong prefix is invisible in the UI but silently misfiles the return.
  for (const p of Object.values(PARTIES) as Party[]) {
    if (!p.gstin.startsWith(p.state)) {
      throw new Error(
        `party "${p.name}": GSTIN ${p.gstin} does not begin with its state code ${p.state}`,
      );
    }
  }
  if (!COMPANY_GSTIN.startsWith(HOME_STATE)) {
    throw new Error(`company GSTIN ${COMPANY_GSTIN} does not begin with ${HOME_STATE}`);
  }

  // Fail LOUD if any demo party would be seeded WITHOUT an address, because the
  // consequence is not cosmetic: Rule 46(d) makes the recipient's address a
  // mandatory particular, internal/printing refuses an invoice without one, and
  // the demo would ship unable to print any of the invoices this script posts.
  // The type already requires the field; this catches an empty string in it,
  // which the type cannot.
  for (const p of Object.values(PARTIES) as Party[]) {
    if (!p.postal.address.trim() || !p.postal.city.trim() || !p.postal.pincode.trim()) {
      throw new Error(
        `party "${p.name}": no postal address on file, so every tax invoice raised on ` +
          `them would be refused for want of the Rule 46(d) recipient address`,
      );
    }
  }

  /**
   * Create a party ledger with its FULL GST identity.
   *
   *   registration_type: "REGULAR"  — without it the ledger reads as UNKNOWN and
   *     gstr1.go classifies every invoice to it as B2CS (consumer) instead of
   *     B2B, so the customer never gets input credit. This was the single
   *     largest silent defect in the previous demo.
   *   maintain_bill_by_bill: true   — Tally only tracks bill references on a
   *     ledger that says Yes here. The old seed attached bill allocations to
   *     ledgers flagged false, which is self-contradictory data.
   *   address / city / pincode     — the Rule 46(d) recipient block. WITHOUT
   *     THESE THE DEMO CANNOT PRINT ANY OF ITS OWN INVOICES: internal/printing
   *     refuses a tax invoice whose recipient address is blank, so before
   *     migration 0039 gave the party master somewhere to keep one, every sales
   *     voucher this script posts answered 4xx on print.
   *
   * They are SPREAD rather than written as literal properties on purpose:
   * CreateLedgerRequest in src/api/types.ts does not declare them yet (that file
   * belongs to another slice, and the handoff to add them is recorded), and TS
   * excess-property checking rejects an undeclared property written inline while
   * a spread passes. The wire body is identical either way — rest.ts posts the
   * whole object — and this keeps `npm run typecheck` green both before and
   * after the declaration lands.
   */
  const partyLedger = async (p: Party) => {
    const postal = {
      address: p.postal.address,
      city: p.postal.city,
      pincode: p.postal.pincode,
    };
    const res = await client.createLedger(co, {
      name: p.name,
      group_name: p.group,
      state_code: p.state,
      gstin: p.gstin,
      registration_type: "REGULAR",
      maintain_bill_by_bill: true,
      ...postal,
      ...(p.opening
        ? {
            opening_balance: money(p.opening.amount),
            opening_dc: p.opening.dc,
            opening_bills: p.opening.bills.map((b) => ({
              ref_name: b.ref,
              ref_type: "NEW" as const,
              dr_cr: p.opening!.dc,
              amount: money(b.amount),
              due_date: b.dueDate,
            })),
          }
        : {}),
    });
    return res.ledger_id;
  };

  const acme = await partyLedger(PARTIES.acme);
  const mumbai = await partyLedger(PARTIES.mumbai);
  const steel = await partyLedger(PARTIES.steel);
  const delhi = await partyLedger(PARTIES.delhi);
  const pune = await partyLedger(PARTIES.pune);

  const sales = await ledger("Domestic Sales", "Sales Accounts");
  const purchases = await ledger("Raw Material Purchases", "Purchase Accounts");
  const rent = await ledger("Rent", "Indirect Expenses");
  const power = await ledger("Electricity", "Indirect Expenses");
  // OPENING BALANCES, as at books_beginning_from (2026-04-01).
  //
  // This is what a real migration into Tally looks like, and what the previous
  // demo could not teach at all: it introduced its entire equity with a Journal
  // dated day one, which is a TRANSACTION, not an opening position. The
  // difference matters — an opening balance is the state the books START in and
  // is dated at books_beginning_from; a journal is something that HAPPENED.
  //
  //   assets  opening : Cash 1,50,000 + Bank 2,50,000 + Acme 1,20,000 = 5,20,000 Dr
  //   liab.   opening : Steel Suppliers                                =   68,000 Cr
  //   capital         : the RESIDUAL that makes it balance              = 4,52,000 Cr
  //
  // Capital is derived, not invented: in a real migration you know your assets
  // and liabilities, and owners' equity is whatever reconciles them.
  const OPENING_CASH = 150000;
  const OPENING_BANK = 250000;
  const OPENING_DEBTORS = PARTIES.acme.opening.amount;
  const OPENING_CREDITORS = PARTIES.steel.opening.amount;
  const OPENING_CAPITAL =
    OPENING_CASH + OPENING_BANK + OPENING_DEBTORS - OPENING_CREDITORS;

  // "Capital A/c", not "Capital Account": a LEDGER may not share its GROUP's
  // name in Tally, and the old seed had exactly that collision.
  const capital = await client
    .createLedger(co, {
      name: "Capital A/c",
      group_name: "Capital Account",
      opening_balance: money(OPENING_CAPITAL),
      opening_dc: "C",
    })
    .then((r) => r.ledger_id);
  const bank = await client
    .createLedger(co, {
      name: "HDFC Bank",
      group_name: "Bank Accounts",
      opening_balance: money(OPENING_BANK),
      opening_dc: "D",
      // THE ACCOUNT IDENTITY IS SEEDED WITH THE LEDGER, and that is invariant
      // I-11 rather than polish: migration 0064 added these columns and a
      // BLANK LABELLED FIELD READS AS "this bank has no IFSC". A demo whose
      // bank shows two empty rows looks like a product that lost the data.
      //
      // Both values are fictional but WELL-FORMED, and the IFSC is the one
      // already used by the demo's own OCR fixture further down this file
      // (`Bank IFSC: HDFC0004321`), so the seeded company agrees with the bill
      // it is shown scanning. It also satisfies 0064's shape CHECK: four
      // letters, '0', six alphanumerics.
      bank_account_number: "50200012345678",
      bank_ifsc: "HDFC0004321",
    })
    .then((r) => r.ledger_id);

  // CASH: reuse the ledger every company is SEEDED with rather than creating a
  // second one. seed_company_defaults already made "Cash" under Cash-in-Hand;
  // the old seed could not resolve its id (there was no way to read ledgers
  // back), so it created "Cash A/c" alongside it and left "Cash" sitting on the
  // Trial Balance as a permanently-zero dead row. GET .../ledgers now exists.
  const existing = await client.listLedgers(co);
  const seededCash = existing.ledgers.find(
    (l) => l.name === "Cash" && l.is_system,
  );
  if (!seededCash) {
    throw new Error(
      "expected the seeded system ledger 'Cash' to exist — did seed_company_defaults change?",
    );
  }
  const cash = seededCash.id;

  // The seeded Cash ledger exists BEFORE any client can speak, so its opening
  // balance cannot be supplied at creation — hence the dedicated setter.
  await client.setLedgerOpening(co, cash, {
    opening_balance: money(OPENING_CASH),
    opening_dc: "D",
    created_by: "seed",
  });

  const outCgst = await ledger("Output CGST", "Duties & Taxes");
  const outSgst = await ledger("Output SGST", "Duties & Taxes");
  const outIgst = await ledger("Output IGST", "Duties & Taxes");
  const inCgst = await ledger("Input CGST", "Duties & Taxes");
  const inSgst = await ledger("Input SGST", "Duties & Taxes");
  const inIgst = await ledger("Input IGST", "Duties & Taxes");

  console.log("  masters:    4 parties, 6 tax ledgers, sales/purchase/expense, bank + cash");

  // ---- T2 COST MASTERS: TWO AXES, BECAUSE ONE AXIS TEACHES THE WRONG MODEL --
  //
  // WHY THIS SECTION EXISTS AT ALL. Until it did, the Cost Centre Summary and
  // the Cost Category Summary — two real screens, reachable from the Gateway,
  // served by real report names the PDP knows about — rendered EMPTY against a
  // fresh demo company and could not be demonstrated at all. Worse (HANDOFF
  // §11), the shared dev database HAD cost masters, put there by a browser, so
  // anyone reasoning from what was on screen believed the seed produced them.
  //
  // ⛔⛔ TWO AXES AND NOT ONE, AND THAT IS THE WHOLE POINT OF THE SECTION.
  // A cost CATEGORY is an AXIS of analysis; a cost CENTRE is a POSITION on it
  // (HANDOFF §8.1). Several axes allocate THE SAME MONEY IN PARALLEL: the
  // 45,000 rent below is allocated in full across Departments AND AGAIN in full
  // across Branches, and both allocations are complete and both are correct.
  //
  // A demo with ONE axis cannot show that, and a reader who only ever sees one
  // will reasonably conclude that cost centres add up — which is the single
  // error this model is most often built with. With two, the Cost Category
  // Summary shows 53,600 allocated on Department and 45,000 on Branch over
  // 53,600 of allocable cost, side by side, WITH NO GRAND TOTAL, and the report
  // says in its own `notes` why there is none. That is the lesson, on screen,
  // in the demo data, rather than in a comment nobody reads.
  //
  // allocate_non_revenue is left FALSE (the default) on both: departmental
  // analysis of revenue and expense is the ordinary case, and it is also what
  // makes the off-axis detector below able to fire — with it TRUE, a split
  // wrongly attached to the BANK leg would be a legal allocation and the
  // seed's own cross-check could not see it.
  const deptAxis = (
    await client.createCostCategory(co, {
      name: "Department",
      allocate_non_revenue: false,
      created_by: "seed",
    })
  ).id;
  const branchAxis = (
    await client.createCostCategory(co, {
      name: "Branch",
      allocate_non_revenue: false,
      created_by: "seed",
    })
  ).id;

  // ⛔ THE WRITE REPLY IS AN ENVELOPE, NOT THE VIEW. `{ id, cost_centre }`,
  // where cost_centre is null when the read-back failed — the row IS written
  // either way. Typing the envelope AS the flat view is a lie the compiler
  // cannot catch, and it shipped once: the masters screen said "Created
  // undefined on the undefined axis" with 597 tests green. The id is taken from
  // the envelope; the view is used only to prove the axis it landed on.
  const costCentre = async (axisID: string, name: string): Promise<string> => {
    const res = await client.createCostCentre(co, {
      cost_category_id: axisID,
      name,
      created_by: "seed",
    });
    if (res.cost_centre && res.cost_centre.cost_category_id !== axisID) {
      throw new Error(
        `cost centre "${name}" was created on axis ${res.cost_centre.cost_category_id}, ` +
          `not on ${axisID} — a centre on the wrong axis makes every figure ` +
          `drawn on that axis wrong while both reports still foot`,
      );
    }
    return res.id;
  };

  const deptSales = await costCentre(deptAxis, "Sales");
  const deptAdmin = await costCentre(deptAxis, "Admin");
  // Two branches whose names are DELIBERATELY NOT the department names: a demo
  // where "Sales" is both a Department and a Branch cannot show that a bare
  // centre name is ambiguous across axes, which is why CostCentre carries
  // cost_category_name on every row.
  const branchBengaluru = await costCentre(branchAxis, "Bengaluru");
  const branchMumbai = await costCentre(branchAxis, "Mumbai");

  console.log(
    "  cost:       2 AXES — Department (Sales, Admin) + Branch (Bengaluru, Mumbai)",
  );

  // ---- Helpers -------------------------------------------------------------
  /** A GST sales invoice with a bill-wise NEW reference (so it ages). */
  const salesInvoice = async (opts: {
    date: string;
    party: string;
    partyId: string;
    partyState: string;
    taxable: number;
    ratePct: number;
    hsn: string;
    ref: string;
    dueDate: string;
  }) => {
    const inter = opts.partyState !== HOME_STATE;
    const tax = (opts.taxable * opts.ratePct) / 100;
    const total = opts.taxable + tax;
    const body: VoucherRequest = {
      voucher_date: opts.date,
      party: opts.party,
      party_ledger_id: opts.partyId,
      narration: `${opts.ref} — ${opts.party}`,
      postings: [
        { ledger_id: opts.partyId, dr_cr: "D", amount: money(total) },
        { ledger_id: sales, dr_cr: "C", amount: money(opts.taxable) },
        ...(inter
          ? [{ ledger_id: outIgst, dr_cr: "C" as const, amount: money(tax) }]
          : [
              { ledger_id: outCgst, dr_cr: "C" as const, amount: money(tax / 2) },
              { ledger_id: outSgst, dr_cr: "C" as const, amount: money(tax / 2) },
            ]),
      ],
      bill_allocations: [
        {
          ledger_id: opts.partyId,
          ref_name: opts.ref,
          ref_type: "NEW",
          dr_cr: "D",
          amount: money(total),
          due_date: opts.dueDate,
        },
      ],
      gst: {
        supplier_state_code: HOME_STATE,
        place_of_supply_state_code: opts.partyState,
        lines: [
          {
            taxable_value: money(opts.taxable),
            gst_rate_pct: String(opts.ratePct),
            hsn: opts.hsn,
          },
        ],
        cgst_ledger_id: outCgst,
        sgst_ledger_id: outSgst,
        igst_ledger_id: outIgst,
      },
    };
    return client.postSalesVoucher(co, body);
  };

  /** A GST purchase bill with a bill-wise NEW reference on the supplier. */
  const purchaseBill = async (opts: {
    date: string;
    party: string;
    partyId: string;
    partyState: string;
    taxable: number;
    ratePct: number;
    hsn: string;
    ref: string;
    dueDate: string;
  }) => {
    const inter = opts.partyState !== HOME_STATE;
    const tax = (opts.taxable * opts.ratePct) / 100;
    const total = opts.taxable + tax;
    const body: VoucherRequest = {
      voucher_date: opts.date,
      party: opts.party,
      party_ledger_id: opts.partyId,
      narration: `${opts.ref} — ${opts.party}`,
      postings: [
        { ledger_id: purchases, dr_cr: "D", amount: money(opts.taxable) },
        ...(inter
          ? [{ ledger_id: inIgst, dr_cr: "D" as const, amount: money(tax) }]
          : [
              { ledger_id: inCgst, dr_cr: "D" as const, amount: money(tax / 2) },
              { ledger_id: inSgst, dr_cr: "D" as const, amount: money(tax / 2) },
            ]),
        { ledger_id: opts.partyId, dr_cr: "C", amount: money(total) },
      ],
      bill_allocations: [
        {
          ledger_id: opts.partyId,
          ref_name: opts.ref,
          ref_type: "NEW",
          dr_cr: "C",
          amount: money(total),
          due_date: opts.dueDate,
        },
      ],
      // THE GST BLOCK WAS MISSING HERE. salesInvoice() sent one; this did not,
      // so the tax POSTINGS were written but taxable_value/cgst/sgst/igst on the
      // voucher version stayed NULL. gstr3b.go filters on
      // `taxable_value IS NOT NULL`, so every purchase was skipped and the
      // return reported ZERO input tax credit while ~9,000 sat in the Input
      // CGST/SGST/IGST ledgers. Supplying it makes the ITC real.
      //
      // Note the orientation: on a PURCHASE the supplier's state is the party's
      // and the place of supply is OUR state (we are the recipient) — the mirror
      // image of the sales case above.
      gst: {
        supplier_state_code: opts.partyState,
        place_of_supply_state_code: HOME_STATE,
        lines: [
          {
            taxable_value: money(opts.taxable),
            gst_rate_pct: String(opts.ratePct),
            hsn: opts.hsn,
          },
        ],
        cgst_ledger_id: inCgst,
        sgst_ledger_id: inSgst,
        igst_ledger_id: inIgst,
      },
    };
    return client.postPurchaseVoucher(co, body);
  };

  // ---- The books -----------------------------------------------------------
  // 1. ADDITIONAL capital brought in DURING the year (F7 Journal).
  //
  //    Contrast this with the OPENING capital above, and the contrast is the
  //    whole lesson: the 4,52,000 opening is the equity the books STARTED with,
  //    dated at books_beginning_from and carrying no voucher; this 50,000 is a
  //    thing that HAPPENED on 20 May and has a voucher, a narration and a place
  //    in the Day Book. The old seed conflated the two by introducing all equity
  //    as a day-one journal.
  await client.postJournalVoucher(co, {
    voucher_date: "2026-05-20",
    narration: "Additional capital introduced by proprietor",
    postings: [
      { ledger_id: bank, dr_cr: "D", amount: money(50000) },
      { ledger_id: capital, dr_cr: "C", amount: money(50000) },
    ],
  });

  // 2. F4 Contra — cash into the bank.
  await client.postContraVoucher(co, {
    voucher_date: "2026-04-02",
    narration: "Cash deposited to HDFC",
    postings: [
      { ledger_id: bank, dr_cr: "D", amount: money(100000) },
      { ledger_id: cash, dr_cr: "C", amount: money(100000) },
    ],
  });

  // 3. F8 Sales — intra-state (CGST+SGST) and inter-state (IGST), both aged.
  await salesInvoice({
    date: "2026-04-12", party: PARTIES.acme.name, partyId: acme, partyState: PARTIES.acme.state,
    taxable: 50000, ratePct: 18, hsn: HSN_STEEL_ARTICLES, ref: "INV-001", dueDate: "2026-05-12",
  });
  await salesInvoice({
    date: "2026-05-08", party: PARTIES.mumbai.name, partyId: mumbai, partyState: PARTIES.mumbai.state,
    taxable: 80000, ratePct: 18, hsn: HSN_STEEL_ARTICLES, ref: "INV-002", dueDate: "2026-06-22",
  });
  const inv3 = await salesInvoice({
    date: "2026-06-19", party: PARTIES.acme.name, partyId: acme, partyState: PARTIES.acme.state,
    taxable: 24000, ratePct: 12, hsn: HSN_STEEL_KITCHEN, ref: "INV-003", dueDate: "2026-07-19",
  });

  // 4. F9 Purchases — intra and inter-state input tax.
  await purchaseBill({
    date: "2026-04-20", party: PARTIES.steel.name, partyId: steel, partyState: PARTIES.steel.state,
    taxable: 30000, ratePct: 18, hsn: HSN_FLAT_ROLLED, ref: "SUP-101", dueDate: "2026-05-20",
  });
  await purchaseBill({
    date: "2026-05-15", party: PARTIES.delhi.name, partyId: delhi, partyState: PARTIES.delhi.state,
    taxable: 20000, ratePct: 18, hsn: HSN_FLAT_ROLLED, ref: "SUP-102", dueDate: "2026-06-29",
  });

  // 5. F6 Receipt — a PART payment against INV-001, so one bill stays partly
  //    open (the interesting Outstandings case) while INV-002/3 are fully open.
  await client.postReceiptVoucher(co, {
    voucher_date: "2026-05-30",
    party: PARTIES.acme.name,
    party_ledger_id: acme,
    narration: "Part payment against INV-001",
    postings: [
      { ledger_id: bank, dr_cr: "D", amount: money(35000) },
      { ledger_id: acme, dr_cr: "C", amount: money(35000) },
    ],
    bill_allocations: [
      { ledger_id: acme, ref_name: "INV-001", ref_type: "AGAINST", dr_cr: "C", amount: money(35000) },
    ],
  });

  // 6. F5 Payments — settle a supplier bill, and pay the rent.
  await client.postPaymentVoucher(co, {
    voucher_date: "2026-06-05",
    party: PARTIES.steel.name,
    party_ledger_id: steel,
    narration: "Payment against SUP-101",
    postings: [
      { ledger_id: steel, dr_cr: "D", amount: money(20000) },
      { ledger_id: bank, dr_cr: "C", amount: money(20000) },
    ],
    bill_allocations: [
      { ledger_id: steel, ref_name: "SUP-101", ref_type: "AGAINST", dr_cr: "D", amount: money(20000) },
    ],
  });
  // ⛔ THE RENT IS THE VOUCHER THAT CARRIES THE COST SPLIT, AND IT CARRIES IT
  // TWICE — once per axis. Read HANDOFF §8.1 before touching the figures.
  //
  //   Department : Sales 27,000 + Admin  18,000 = 45,000
  //   Branch     : B'luru 31,500 + Mumbai 13,500 = 45,000
  //
  // Both total the posting EXACTLY, and neither total may be added to the
  // other: 90,000 of departmental+branch cost against 45,000 of rent is not a
  // bigger number, it is the same rent counted from two directions. That is
  // what the per-(posting, CATEGORY) sum trigger permits and what a naive
  // "total allocated" figure would get wrong.
  //
  // ⛔⛔ AND THE LEG. `rentPostings[0]` is RENT and `[1]` is HDFC BANK. Attach
  // the split to the bank leg and the voucher still balances, both cost reports
  // still foot, and the Cost Centre Summary answers a question about a bank
  // account. costSplitOn is given the LEDGER and derives the sequence, because
  // the two legs carry THE SAME AMOUNT and nothing but the ledger tells them
  // apart. The seed then proves it landed right in two independent ways, at the
  // bottom of this file: by reading the voucher back and naming the ledger the
  // stored posting_seq points at, and by asserting the Cost Category Summary's
  // off_axis_postings is zero (HDFC Bank is not a P&L ledger, so a split on it
  // would be off-axis for an allocate_non_revenue = false axis).
  const rentPostings: PostingRequest[] = [
    { ledger_id: rent, dr_cr: "D", amount: money(45000) },
    { ledger_id: bank, dr_cr: "C", amount: money(45000) },
  ];
  const rentVoucher = await client.postPaymentVoucher(co, {
    voucher_date: "2026-06-30",
    narration: "Office rent — Q1 (allocated by Department and by Branch)",
    postings: rentPostings,
    cost_allocations: [
      ...costSplitOn(rentPostings, rent, deptAxis, [
        { costCentreID: deptSales, amount: "27000.0000" },
        { costCentreID: deptAdmin, amount: "18000.0000" },
      ]),
      ...costSplitOn(rentPostings, rent, branchAxis, [
        { costCentreID: branchBengaluru, amount: "31500.0000" },
        { costCentreID: branchMumbai, amount: "13500.0000" },
      ]),
    ],
  });

  // 7. F7 Journal — an accrued expense (no cash movement).
  //
  // ⛔ ALLOCATED ON THE DEPARTMENT AXIS ONLY, AND THE OMISSION IS THE FEATURE.
  // The Cost Category Summary exists to make an INCOMPLETE allocation visible:
  // no database constraint can see one, because the sum trigger fires on the
  // allocations and where there are none there is nothing to fire on. If every
  // seeded cost were allocated on every axis, `unallocated_*` would be zero
  // everywhere, the one column the report is FOR would render as a row of
  // zeros, and a wrong implementation of it would be indistinguishable from a
  // right one. So this 8,600 is departmental and is NOT branch-attributed, and
  // the Branch axis therefore reports a real 8,600 gap that the Department axis
  // does not — two different, correct answers about the same books.
  const powerPostings: PostingRequest[] = [
    { ledger_id: power, dr_cr: "D", amount: money(8600) },
    { ledger_id: cash, dr_cr: "C", amount: money(8600) },
  ];
  await client.postJournalVoucher(co, {
    voucher_date: "2026-07-01",
    narration: "Electricity accrued for June (allocated by Department)",
    postings: powerPostings,
    cost_allocations: costSplitOn(powerPostings, power, deptAxis, [
      { costCentreID: deptSales, amount: "5160.0000" },
      { costCentreID: deptAdmin, amount: "3440.0000" },
    ]),
  });

  // 8. An ALTERED voucher: INV-003 is corrected from 24,000 to 26,500 @12%.
  //    This appends VERSION 2 — the original is retained, never overwritten.
  const inv3Tax = (26500 * 12) / 100;
  await client.alterSalesVoucher(co, inv3.voucher_id, {
    voucher_date: "2026-06-19",
    party: PARTIES.acme.name,
    party_ledger_id: acme,
    narration: "INV-003 — corrected quantity",
    postings: [
      { ledger_id: acme, dr_cr: "D", amount: money(26500 + inv3Tax) },
      { ledger_id: sales, dr_cr: "C", amount: money(26500) },
      { ledger_id: outCgst, dr_cr: "C", amount: money(inv3Tax / 2) },
      { ledger_id: outSgst, dr_cr: "C", amount: money(inv3Tax / 2) },
    ],
    // RESTATE the bill reference at the corrected total. An alter re-states the
    // WHOLE voucher, allocations included; the previous seed omitted this, so
    // the bill kept ageing at the original 26,880 while the debtor's balance
    // moved to 29,680 — Outstandings and the Balance Sheet disagreed by 2,800.
    bill_allocations: [
      {
        ledger_id: acme,
        ref_name: "INV-003",
        ref_type: "NEW",
        dr_cr: "D",
        amount: money(26500 + inv3Tax),
        due_date: "2026-07-19",
      },
    ],
    gst: {
      supplier_state_code: HOME_STATE,
      place_of_supply_state_code: HOME_STATE,
      lines: [
        { taxable_value: money(26500), gst_rate_pct: "12", hsn: HSN_STEEL_KITCHEN },
      ],
      cgst_ledger_id: outCgst,
      sgst_ledger_id: outSgst,
      igst_ledger_id: outIgst,
    },
  });

  // 8b. A CREDIT NOTE — a partial sales return against INV-002 (Mumbai Metals).
  //     THE POINT OF THIS VOUCHER: it is how you undo part of an invoice
  //     CORRECTLY. Note 8 above ALTERS INV-003, which rewrites history — fine
  //     for a data-entry typo caught the same day, wrong for goods that came
  //     back in July against an April sale. A credit note is its own dated
  //     document with its own number series, and it lands in GSTR-1's cdnr
  //     section (table 9B) rather than silently changing a filed invoice.
  //
  //     Goods worth 12,000 of the 80,000 inter-state sale come back @18% IGST.
  //     Being a return of an INTER-state sale it reverses IGST, not CGST+SGST.
  const CN_TAXABLE = 12000;
  const CN_TAX = (CN_TAXABLE * 18) / 100;
  await client.postCreditNoteVoucher(co, {
    voucher_date: "2026-07-08",
    party: PARTIES.mumbai.name,
    party_ledger_id: mumbai,
    narration: "Sales return against INV-002 — 12,000 of goods returned",
    postings: [
      // Exact mirror of the sale: sales income and output tax come back DOWN,
      // and the customer no longer owes us the returned amount.
      { ledger_id: sales, dr_cr: "D", amount: money(CN_TAXABLE) },
      { ledger_id: outIgst, dr_cr: "D", amount: money(CN_TAX) },
      { ledger_id: mumbai, dr_cr: "C", amount: money(CN_TAXABLE + CN_TAX) },
    ],
    // AGAINST the original bill reference, so Outstandings reduces INV-002
    // rather than opening a new negative bill.
    bill_allocations: [
      {
        ledger_id: mumbai,
        ref_name: "INV-002",
        ref_type: "AGAINST",
        dr_cr: "C",
        amount: money(CN_TAXABLE + CN_TAX),
      },
    ],
    gst: {
      supplier_state_code: HOME_STATE,
      place_of_supply_state_code: PARTIES.mumbai.state,
      lines: [
        { taxable_value: money(CN_TAXABLE), gst_rate_pct: "18", hsn: HSN_STEEL_ARTICLES },
      ],
      cgst_ledger_id: outCgst,
      sgst_ledger_id: outSgst,
      igst_ledger_id: outIgst,
    },
  });

  // 8c. A DEBIT NOTE — a PURCHASE RETURN raised on a supplier.
  //     We send 2,500 of defective steel back to Steel Suppliers (intra-state,
  //     so CGST+SGST). WE debit THEM: they owe us, our purchases come down, and
  //     the input credit we claimed on those goods is REVERSED.
  //
  //     Note which way this faces. A note raised on a SUPPLIER is a purchase-side
  //     document: it belongs in GSTR-3B as an ITC reversal and must NOT appear in
  //     GSTR-1, which is the OUTWARD return. The reports decide that from the
  //     party's group (Sundry Creditors => liability => purchase side), not from
  //     the voucher class — because both note classes can face either way.
  const DN_TAXABLE = 2500;
  const DN_TAX = (DN_TAXABLE * 18) / 100;
  await client.postDebitNoteVoucher(co, {
    voucher_date: "2026-07-12",
    party: PARTIES.steel.name,
    party_ledger_id: steel,
    narration: "Purchase return against SUP-101 — defective material sent back",
    postings: [
      { ledger_id: steel, dr_cr: "D", amount: money(DN_TAXABLE + DN_TAX) },
      { ledger_id: purchases, dr_cr: "C", amount: money(DN_TAXABLE) },
      { ledger_id: inCgst, dr_cr: "C", amount: money(DN_TAX / 2) },
      { ledger_id: inSgst, dr_cr: "C", amount: money(DN_TAX / 2) },
    ],
    // AGAINST SUP-101: we now owe the supplier less on that bill.
    bill_allocations: [
      {
        ledger_id: steel,
        ref_name: "SUP-101",
        ref_type: "AGAINST",
        dr_cr: "D",
        amount: money(DN_TAXABLE + DN_TAX),
      },
    ],
    gst: {
      supplier_state_code: PARTIES.steel.state,
      place_of_supply_state_code: HOME_STATE,
      lines: [
        { taxable_value: money(DN_TAXABLE), gst_rate_pct: "18", hsn: HSN_FLAT_ROLLED },
      ],
      cgst_ledger_id: inCgst,
      sgst_ledger_id: inSgst,
      igst_ledger_id: inIgst,
    },
  });

  // 9. A DELETED voucher: posted, then tombstoned. It vanishes from the reports
  //    but its versions remain in the ledger (the audit trail is append-only).
  const mistake = await client.postJournalVoucher(co, {
    voucher_date: "2026-07-03",
    narration: "Duplicate entry — to be deleted",
    postings: [
      { ledger_id: power, dr_cr: "D", amount: money(1200) },
      { ledger_id: cash, dr_cr: "C", amount: money(1200) },
    ],
  });
  await client.deleteVoucher(co, mistake.voucher_id);

  console.log("  vouchers:   13 live across Apr–Jul 2026 (all 8 types incl. credit/debit note), 1 altered (v2), 1 deleted");

  // ---- INVENTORY: goods that move WITHOUT an accounting entry --------------
  //
  // WHY THIS SECTION EXISTS AT ALL. Until it did, `stock_movement` was EMPTY
  // for every company in the dev database and `seed-demo.mts` contained not one
  // occurrence of the word "stock". That is not a cosmetic gap: `verify:demo`
  // asserts that the Balance Sheet balances at every month-end and that the P&L
  // is additive, and BOTH of those assertions evaluate byte-identically over a
  // correct closing-stock fold-in, an inverted one, an off-by-one one, and no
  // fold-in at all, when there is no stock. Gated on a demo with no inventory,
  // the whole closing-stock feature would ship UNGATED. Every date and quantity
  // below is chosen so that one specific gate check becomes ABLE TO FAIL.
  //
  // WHY STOCK-ONLY VOUCHERS (receipt note / delivery note / stock journal) AND
  // NOT ITEM INVOICES. These three classes record the movement of GOODS and
  // post no accounting lines at all — `validateStockVoucher` refuses a posting,
  // a bill allocation or a GST block on them. Two consequences, both wanted:
  //
  //   1. NOTHING ALREADY DOCUMENTED MOVES. The Trial Balance, Sundry Debtors,
  //      Sundry Creditors, Domestic Sales and every GST figure stay BYTE-
  //      IDENTICAL to the run before this section existed, so the verify:demo
  //      checks that pin them keep passing on unchanged numbers and no figure
  //      quoted in docs/TALLY-FOR-FOUNDERS.md is invalidated by the seed alone.
  //      An item invoice would have moved sales, purchases, GST and the
  //      outstandings all at once, and the diff would have been unreadable.
  //
  //   2. IT IS A REAL EXERCISE OF THE CASE THE ACCOUNTING DEPENDS ON. Goods
  //      that arrive and leave with no ledger entry are exactly the
  //      physical-movement-with-zero-accounting-effect case. In a PERIODIC
  //      inventory system — which is what this schema is; the value of stock is
  //      never stored, only replayed — the entire effect of such a movement
  //      reaches the accounts through the closing-stock term and nowhere else.
  //      So "stock that appeared without a purchase" is not a data artifact to
  //      apologise for: it is the demo showing that closing stock, and only
  //      closing stock, is what carries a physical movement into the profit.
  //
  // WHAT WILL CHANGE ONCE THE FOLD-IN LANDS: assets and net profit BOTH rise by
  // the closing-stock value, by the same amount, which is why the sheet still
  // balances. That is the honest consequence of a periodic system holding stock
  // the accounts never paid for.
  //
  // NOT CREATED HERE, DELIBERATELY: any ledger under the `Stock-in-Hand` group.
  // A company that books stock BOTH as an ordinary ledger and as inventory
  // movement double-counts it on both sides of the sheet — and because the
  // double count is symmetric the sheet still FOOTS, so no `balanced` check can
  // ever see it. The Balance Sheet refuses to render in that case. Demo Traders
  // must therefore keep its stock in exactly one plane: this one.
  //
  // EVERY QUANTITY AND RATE BELOW IS A LITERAL FIXED-4 STRING. Nothing here is
  // computed in JavaScript, because a JSON number is a float64 and a float
  // quantity makes a closing balance depend on the order the lines were keyed.

  // `Nos` is seeded with every company; `Kg` is not, and the second item is
  // denominated in it so the demo shows two different units side by side (and
  // so a report that assumed one unit per company would break here).
  await client.createUnit(co, {
    symbol: "Kg",
    formal_name: "Kilograms",
    created_by: "seed",
  });

  // The system godown every company is seeded with. Movements MUST land on the
  // same (item, godown, batch) key the opening position was written to —
  // `stock_opening` is keyed by that triple — or the replay would value an
  // opening at Main Location and a movement somewhere else as two unrelated
  // positions, and the negative-stock trigger would judge each against the
  // wrong book.
  const godowns = await client.listGodowns(co);
  const mainLocation = godowns.godowns.find((g) => g.name === "Main Location");
  if (!mainLocation) {
    throw new Error(
      "expected the seeded system godown 'Main Location' to exist — did seed_inventory_defaults change?",
    );
  }

  // ITEM A — no opening position. It arrives, leaves, is re-priced by a
  // back-dated purchase, is altered and is partly reversed. It is the item that
  // exercises the MOVEMENT half of the replay.
  const itemAngle = (
    await client.createStockItem(co, {
      name: "MS Angle 50x50",
      unit_symbol: "Nos",
      hsn: HSN_STEEL_ARTICLES,
      gst_rate_pct: "18.0000",
      created_by: "seed",
    })
  ).stock_item_id;

  // ITEM B — WITH an opening position, and that is mandatory rather than
  // decorative. `loadStockOpenings` returns nothing for a date before
  // `books_beginning_from`, which is what stops pre-books stock leaking onto a
  // Balance Sheet dated 2020 (the accounting side had exactly that bug once).
  // A gate check that asserts "no closing stock before the books began" is
  // VACUOUS unless there is an opening position for the cutoff to suppress —
  // so one exists, at a rate different from every movement rate below.
  //
  // 40 Kg at 812.5000 = 32,500.0000 as at 2026-04-01. The opening is written to
  // Main Location by POST /stock-items.
  const itemSheet = (
    await client.createStockItem(co, {
      name: "SS Sheet 2mm",
      unit_symbol: "Kg",
      hsn: HSN_STAINLESS_FLAT,
      gst_rate_pct: "18.0000",
      opening_qty: "40.0000",
      opening_rate: "812.5000",
      created_by: "seed",
    })
  ).stock_item_id;

  // ---- T3 ORDERS: A COMMITTED QUANTITY, WHICH IS NOT A MOVEMENT ------------
  //
  // WHY THIS SECTION EXISTS. The Order Register and the two order books
  // rendered EMPTY against a fresh demo company, so T3 — a shipped, tested,
  // trigger-guarded feature — could not be shown to anybody. The dev database
  // had a `SO-BROWSER-1` put there by hand (HANDOFF §11); a fresh one had
  // nothing, which is the worst of both worlds.
  //
  // ⛔⛔ NOTHING IN THIS SECTION TOUCHES CLOSING STOCK, AND THAT IS THE MODEL,
  // NOT AN OMISSION. An order is a PROMISE. When a customer orders 50 units the
  // goods are still on our shelf, still ours, and still in closing stock at
  // full value — so orders live in their own tables, affect the ORDER BOOK, and
  // never the trial balance, the P&L or the balance sheet (migration 0042's
  // header states the consequence of getting this wrong: closing stock
  // corrupted by every open order, reconciling against nothing). The two orders
  // and the two fulfilment links below therefore move NO figure that
  // `verify:demo` asserts.
  //
  // ⛔ PARTIAL FULFILMENT IS THE NORMAL CASE AND THE OUTSTANDING BALANCE IS THE
  // ENTIRE POINT (HANDOFF §8.2), so both orders below are PART-delivered by
  // vouchers that already existed in this seed. A fully-delivered order would
  // demonstrate the feature's least interesting state and would leave both
  // order books empty, which is exactly the situation this section exists to
  // end.
  //
  // ⛔ THERE IS NO fulfilled_qty AND NO status COLUMN. Every figure quoted in
  // the assertions at the bottom of this file is DERIVED by the server, on
  // every read, by summing order_fulfilment across CURRENT, non-deleted voucher
  // versions. Nothing here writes one, and nothing may cache one across an
  // alter.
  const SO_NO = "SO-2026-001";
  const PO_NO = "PO-2026-001";
  const salesOrderID = (
    await client.createSalesOrder(co, {
      order_no: SO_NO,
      order_date: "2026-06-15",
      party_ledger_id: acme,
      narration: "Customer order — MS Angle for the Basavanagudi site, SS Sheet to follow",
      created_by: "seed",
      lines: [
        // Line 1 is PART-delivered by the 2026-06-30 delivery note below: 30 of
        // 50 go out, leaving 20 outstanding at the agreed 300.00 = 6,000.00.
        //
        // ⚠️ THE AGREED RATE IS NOT A VALUATION. 300.00 is what the customer
        // agreed to pay; the same item's stock is carried at its weighted
        // average cost (~19.73 after the back-dated purchase further down), and
        // the invoice that eventually prices this delivery may lawfully bill a
        // third figure. Three different, correct numbers for one item — which
        // is why the order book's value column says "at the ORDER's agreed
        // rate" and never reconciles itself to stock.
        {
          stock_item_id: itemAngle,
          qty: "50.0000",
          rate: "300.0000",
          due_date: "2026-07-15",
        },
        // Line 2 is UNTOUCHED, deliberately. Fulfilment is per LINE, not per
        // order, and an order book that only ever showed part-delivered lines
        // could not show that a line nobody has shipped against is fully
        // outstanding rather than absent. 10 @ 1,050.00 = 10,500.00.
        {
          stock_item_id: itemSheet,
          qty: "10.0000",
          rate: "1050.0000",
          due_date: "2026-08-15",
        },
      ],
    })
  ).order_id;

  // THE PURCHASE SIDE IS A SEPARATE OBJECT WITH A SEPARATE AUTHORITY, and it is
  // seeded separately for that reason rather than for symmetry: `sales-order`
  // and `purchase-order` are two report names and two PDP decisions, so a demo
  // that seeded only one could not show "this salesperson may see the pipeline
  // but not what we have committed to pay suppliers" at all (HANDOFF §8.3).
  // Note the METHOD carries the direction — there is no `direction` field.
  //
  // 200 committed on 5 April; the 10 April receipt note brings 120 in, leaving
  // 80 outstanding at 25.75 = 2,060.00.
  const purchaseOrderID = (
    await client.createPurchaseOrder(co, {
      order_no: PO_NO,
      order_date: "2026-04-05",
      party_ledger_id: steel,
      narration: "Purchase order — MS Angle, delivered in lots",
      created_by: "seed",
      lines: [
        {
          stock_item_id: itemAngle,
          qty: "200.0000",
          rate: "25.7500",
          due_date: "2026-05-10",
        },
      ],
    })
  ).order_id;

  // The order LINE ids, read back, because a fulfilment names the LINE and
  // never the order — a delivery note routinely completes one line of a
  // three-line order and leaves the rest outstanding, and an order-level link
  // could not say which. The server assigns line_no from the position in the
  // `lines` array above (the first line is 1); it is not client-supplied, so it
  // has to be read.
  const soView = await client.getSalesOrder(co, salesOrderID);
  const poView = await client.getPurchaseOrder(co, purchaseOrderID);
  const soAngleLine = soView.lines.find((l) => l.stock_item_id === itemAngle);
  const soSheetLine = soView.lines.find((l) => l.stock_item_id === itemSheet);
  const poAngleLine = poView.lines.find((l) => l.stock_item_id === itemAngle);
  if (!soAngleLine || !soSheetLine || !poAngleLine) {
    throw new Error(
      `order line read-back is missing an item: ${SO_NO} has ` +
        `[${soView.lines.map((l) => l.stock_item).join(", ")}] and ${PO_NO} has ` +
        `[${poView.lines.map((l) => l.stock_item).join(", ")}]`,
    );
  }

  /**
   * POST or PUT a stock-only voucher through the route the UI would use.
   *
   * `createRestClient` exposes no method for these three classes yet, and
   * `frontend/src/api/rest.ts` is not this change's to grow, so the seed speaks
   * to the endpoint directly — the same raw-fetch shape `seedAuthHeaders` above
   * already uses for `/auth/token`. It still goes over HTTP, so it inherits
   * every server-side invariant (the fixed direction per class, the
   * negative-stock constraint trigger, server numbering, the append-only
   * version chain); nothing is written to the database behind cored's back.
   */
  type StockVoucherClass = "receipt-note" | "delivery-note" | "stock-journal";
  const companyPath = `${baseUrl.replace(/\/$/, "")}/v1/companies/${encodeURIComponent(co)}`;
  const stockVoucher = async (
    method: "POST" | "PUT",
    cls: StockVoucherClass,
    body: VoucherRequest,
    voucherID?: string,
  ): Promise<string> => {
    const url =
      `${companyPath}/vouchers/${cls}` +
      (voucherID ? `/${encodeURIComponent(voucherID)}` : "");
    const res = await fetch(url, {
      method,
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify({ created_by: "seed", ...body }),
    });
    const text = await res.text();
    if (!res.ok) {
      throw new Error(
        `${method} ${cls} dated ${body.voucher_date ?? "?"} -> ${res.status}: ${text}`,
      );
    }
    return (JSON.parse(text) as { voucher_id: string }).voucher_id;
  };

  /**
   * One line of the item grid, always at Main Location.
   *
   * `rate` is REQUIRED even on an outward line: a stock-only document is not
   * priced from a price list (it neither sells nor buys), so the ledger refuses
   * a line that leaves the rate unstated. On an OUTWARD line the figure is
   * documentary only — the weighted-average valuer consumes at the pool's
   * running average recomputed at that instant, never at the rate on the line —
   * which is precisely why a back-dated inward can re-value an outward that was
   * already reported.
   */
  const stockLine = (
    stockItemID: string,
    direction: "IN" | "OUT",
    qty: string,
    rate: string,
    amount: string,
  ): InventoryLineRequest => ({
    stock_item_id: stockItemID,
    godown_id: mainLocation.id,
    direction,
    qty,
    rate,
    amount,
  });

  const moves: string[] = [];

  // 1. Goods IN. The first purchase of item A, at 25.7500.
  //
  //    AND IT PART-FULFILS PO-2026-001 — 120 of the 200 committed. The link is
  //    added to the voucher that ALREADY moved these goods rather than to a new
  //    one, because a fulfilment is a LINK and not a movement: inventing a
  //    second receipt to hang it on would have put 120 more units into closing
  //    stock, and closing stock feeds the Balance Sheet.
  //
  //    ⛔ 0042's trigger requires a PURCHASE order to be fulfilled by goods
  //    coming IN (and a SALES order by goods going OUT), reading the direction
  //    off the stock_movement row rather than off the voucher class. A receipt
  //    note is IN, so this is the right way round; the same link on the
  //    delivery note below would be refused by the database.
  const rnAngleLines = [stockLine(itemAngle, "IN", "120.0000", "25.7500", "3090.0000")];
  await stockVoucher("POST", "receipt-note", {
    voucher_date: "2026-04-10",
    narration:
      "Receipt note — MS Angle from Steel Suppliers, first lot against " +
      PO_NO + " (bill to follow)",
    inventory: rnAngleLines,
    fulfilments: [
      fulfilmentOn(rnAngleLines, itemAngle, poAngleLine.order_line_id, "120.0000"),
    ],
  });
  moves.push("2026-04-10  RN  MS Angle 50x50   IN  120 @ 25.7500  (fulfils 120 of " + PO_NO + ")");

  // 2. Goods IN. Item B at 940.0000 — a DIFFERENT rate from its 812.5000
  //    opening, so the weighted average actually has two prices to average.
  await stockVoucher("POST", "receipt-note", {
    voucher_date: "2026-05-20",
    narration: "Receipt note — SS Sheet from Pune Fabricators",
    inventory: [stockLine(itemSheet, "IN", "60.0000", "940.0000", "56400.0000")],
  });
  moves.push("2026-05-20  RN  SS Sheet 2mm     IN   60 @ 940.0000");

  // 3. Goods OUT, dated exactly ON the UPPER bound of the Apr–Jun window the
  //    gate's additivity check uses. A period bound that was exclusive on the
  //    top would silently drop this movement, and a bound flip once survived
  //    the entire reporting package because no fixture sat ON a bound.
  //
  //    AND IT PART-FULFILS SO-2026-001 LINE 1 — 30 of the 50 committed, leaving
  //    20 outstanding, which is the figure the Sales Order Book exists to show.
  //    The SS Sheet line of the same order is left untouched: fulfilment is per
  //    LINE.
  const dnAngleLines = [stockLine(itemAngle, "OUT", "30.0000", "25.7500", "772.5000")];
  const dnAngle = await stockVoucher("POST", "delivery-note", {
    voucher_date: "2026-06-30",
    narration: "Delivery note — MS Angle to Acme Traders against " + SO_NO + " (invoice to follow)",
    inventory: dnAngleLines,
    fulfilments: [
      fulfilmentOn(dnAngleLines, itemAngle, soAngleLine.order_line_id, "30.0000"),
    ],
  });
  moves.push(
    "2026-06-30  DN  MS Angle 50x50   OUT  30   (ON the Apr–Jun upper bound; fulfils 30 of " +
      SO_NO + ")",
  );

  // 4. Goods IN, dated exactly ON the LOWER bound of the Jul–Mar window.
  //
  //    THIS IS THE MOVEMENT THAT MAKES THE OFF-BY-ONE VISIBLE, and it is the
  //    single most load-bearing date in this section. The P&L's opening-stock
  //    term must be taken at d0 MINUS ONE, never at d0: taken at d0 it would
  //    include whatever moved ON d0, the two contiguous halves would no longer
  //    share a boundary value, and they would differ by exactly this receipt.
  //    Delete this voucher and the additivity check passes over the bug.
  await stockVoucher("POST", "receipt-note", {
    voucher_date: "2026-07-01",
    narration: "Receipt note — MS Angle, second lot at a higher price",
    inventory: [stockLine(itemAngle, "IN", "40.0000", "88.0000", "3520.0000")],
  });
  moves.push("2026-07-01  RN  MS Angle 50x50   IN   40 @ 88.0000  (ON the Jul–Mar lower bound)");

  // 5. Goods OUT. Item B is issued, so BOTH items have movement in both
  //    directions and neither closing position is a pure opening.
  await stockVoucher("POST", "delivery-note", {
    voucher_date: "2026-08-15",
    narration: "Delivery note — SS Sheet to Mumbai Metals",
    inventory: [stockLine(itemSheet, "OUT", "25.0000", "940.0000", "23500.0000")],
  });
  moves.push("2026-08-15  DN  SS Sheet 2mm     OUT  25");

  // 6. AN ALTERED STOCK VOUCHER — a period already reported is restated.
  //    Posted as 20 out, corrected to 35 out. The alter appends version 2; the
  //    original movement row is retained and superseded, never overwritten, so
  //    a replay that forgot to follow `voucher_head.current_version_id` would
  //    consume 55 instead of 35 and every later closing value would be wrong.
  const dnAlter = await stockVoucher("POST", "delivery-note", {
    voucher_date: "2026-09-10",
    narration: "Delivery note — MS Angle to Mumbai Metals",
    inventory: [stockLine(itemAngle, "OUT", "20.0000", "25.7500", "515.0000")],
  });
  await stockVoucher(
    "PUT",
    "delivery-note",
    {
      voucher_date: "2026-09-10",
      narration: "Delivery note — MS Angle to Mumbai Metals (quantity corrected)",
      inventory: [stockLine(itemAngle, "OUT", "35.0000", "25.7500", "901.2500")],
    },
    dnAlter,
  );
  moves.push("2026-09-10  DN  MS Angle 50x50   OUT  20 -> ALTERED to 35 (version 2)");

  // 7. A DELETED STOCK VOUCHER — a movement already reported is withdrawn.
  //    A stock journal with a single outward line is a CONSUMPTION (the class
  //    has no fixed direction precisely so it can express a transfer, an issue
  //    to production, or a manufacture). Tombstoning it must remove its effect
  //    from every position at every date, while its versions remain in the
  //    append-only chain.
  const sjDeleted = await stockVoucher("POST", "stock-journal", {
    voucher_date: "2026-10-05",
    narration: "Stock journal — MS Angle issued to the workshop",
    inventory: [stockLine(itemAngle, "OUT", "10.0000", "25.7500", "257.5000")],
  });
  await client.deleteVoucher(co, sjDeleted, "seed");
  moves.push("2026-10-05  SJ  MS Angle 50x50   OUT  10 -> DELETED (tombstoned)");

  // 8. POSTED LAST, BACK-DATED TO 2026-05-15. This is the whole argument for
  //    never storing a stock value.
  //
  //    Inserting an older purchase RE-VALUES every outward that follows it: at
  //    2026-06-30 item A's pool is now 120 @ 25.7500 plus 100 @ 12.5000 =
  //    4,340.0000 over 220 units = 19.72727272... per unit, deliberately
  //    NON-TERMINATING so an implementation that rounded early, or that used a
  //    float, diverges visibly instead of agreeing by luck. A materialised
  //    closing value written before this voucher arrived would now be wrong,
  //    and nothing would ever recompute it.
  await stockVoucher("POST", "receipt-note", {
    voucher_date: "2026-05-15",
    narration: "Receipt note — MS Angle, late-entered older purchase",
    inventory: [stockLine(itemAngle, "IN", "100.0000", "12.5000", "1250.0000")],
  });
  moves.push("2026-05-15  RN  MS Angle 50x50   IN  100 @ 12.5000  (BACK-DATED, entered last)");

  // ---- The invariants the gate rests on, asserted rather than assumed ------
  // A seed that quietly stopped satisfying these would leave verify:demo green
  // and blind. Each throw below names the gate check it would otherwise make
  // vacuous.
  const stockDates = [
    "2026-04-30",
    "2026-05-31",
    "2026-06-30",
    "2026-07-31",
    "2027-01-31",
    "2027-02-28",
    "2027-03-31",
  ] as const;
  const stockValue = new Map<string, string>();
  for (const d of stockDates) {
    stockValue.set(d, (await client.getStockSummary(co, d)).total_value);
  }
  // "Zero" is matched as a STRING pattern, never by parsing the figure into a
  // JavaScript number: money is a fixed-4 decimal string end to end here.
  const isZeroMoney = (v: string): boolean => /^-?0(\.0*)?$/.test(v.trim());
  for (const [d, v] of stockValue) {
    if (isZeroMoney(v)) {
      throw new Error(
        `stock is worth ${v} at ${d}: the month-end Balance-Sheet loop would be ` +
          "exercised on an empty position and could not fail on a wrong fold-in",
      );
    }
  }
  // Nothing moves after 2026-10-05, so two dates in an empty window must agree
  // EXACTLY. This is what makes the gate's empty-window check able to fail: a
  // P&L over an empty window whose opening term was dropped would report the
  // whole warehouse as profit.
  if (stockValue.get("2027-01-31") !== stockValue.get("2027-02-28")) {
    throw new Error(
      `stock moved between 2027-01-31 (${stockValue.get("2027-01-31")}) and ` +
        `2027-02-28 (${stockValue.get("2027-02-28")}): the empty-window check would be testing a non-empty window`,
    );
  }
  // ...and two dates that straddle the 2026-07-01 receipt must DISAGREE, or the
  // additivity check has no stock movement to be additive about.
  if (stockValue.get("2026-06-30") === stockValue.get("2026-07-31")) {
    throw new Error(
      "closing stock is unchanged across 2026-06-30 -> 2026-07-31: the P&L " +
        "additivity check could not fail on an opening term taken at d0 instead of d0-1",
    );
  }
  // The opening POSITION really landed (POST /stock-items writes stock_opening
  // only when a quantity or a rate is non-zero). Without it, the gate's
  // "no closing stock before the books began" check has nothing to suppress.
  const aprSummary = await client.getStockSummary(co, "2026-04-30");
  const sheetItem = aprSummary.items.find((i) => i.item === "SS Sheet 2mm");
  if (!sheetItem || isZeroMoney(sheetItem.opening_value)) {
    throw new Error(
      "SS Sheet 2mm has no opening position: the before-the-books-began check " +
        "would pass whether or not the books-beginning cutoff exists",
    );
  }

  console.log(
    `  inventory:  2 stock items (Nos + Kg), 1 opening position ` +
      `(SS Sheet 2mm ${sheetItem.opening_qty} @ 812.5000), ${moves.length} stock-only vouchers`,
  );
  for (const m of moves) console.log(`              ${m}`);
  console.log(
    "              closing stock: " +
      stockDates.map((d) => `${d}=${stockValue.get(d)}`).join("  "),
  );

  // ---- T2 + T3: the seeded data PROVED AGAINST THE SERVER'S OWN REPORTS ----
  //
  // Every assertion below reads a figure back through the same report a human
  // opens, because the two defects this section guards against — a cost split
  // attached to the wrong posting, and a fulfilment counted against a
  // superseded voucher version — both produce data that balances, foots and
  // reconciles. Nothing but an independent read can see them.
  //
  // ⛔ NOT ONE FIGURE HERE MOVES THE BALANCE SHEET. Cost allocations attach to
  // postings that already existed and change no amount; orders and fulfilments
  // live in their own tables and never reach stock_movement. That is stated as
  // an assertion and not a hope: the closing-stock figures captured above were
  // taken AFTER every order and link was written.
  const FY_FROM = "2026-04-01";
  const FY_TO = "2027-03-31";

  // ⛔⛔ PROOF 1 — THE SPLIT IS ON THE RENT LEG, NOT THE BANK LEG (HANDOFF §1.2).
  // Both legs of the payment are 45,000, so no arithmetic can tell them apart.
  // The read-back can: `cost_allocations[].posting_seq` indexes THIS RESPONSE's
  // own postings array, so the ledger it points at is the ledger the cost was
  // attributed to. With the offset wrong, every line below would say "HDFC
  // Bank" and every report would still foot.
  const rentBack = await client.getVoucher(co, rentVoucher.voucher_id);
  const rentAllocs = rentBack.cost_allocations ?? [];
  if (rentAllocs.length !== 4) {
    throw new Error(
      `the rent voucher stored ${rentAllocs.length} cost allocations, expected 4 ` +
        `(2 centres x 2 axes) — an axis that failed to write leaves a report ` +
        `silently reporting a fraction of the cost`,
    );
  }
  for (const a of rentAllocs) {
    const leg = rentBack.postings[a.posting_seq];
    if (!leg || leg.ledger !== "Rent") {
      throw new Error(
        `a cost allocation of ${a.amount} to ${a.cost_centre_name} landed on ` +
          `posting ${a.posting_seq} (${leg ? leg.ledger : "out of range"}), not on Rent. ` +
          `The voucher still balances and both cost reports still foot; the ` +
          `Cost Centre Summary is simply answering a question about the wrong ` +
          `ledger. This is the buildCostAllocations primary-leg offset bug.`,
      );
    }
  }

  // PROOF 2 — the Cost Centre Summary, per axis, reconciles and is non-empty.
  // ⛔ ONE AXIS PER CALL. There is no "all axes" mode and there must not be:
  // summing Department and Branch would report 98,600 of cost against 53,600
  // actually borne.
  const deptSummary = await client.costCentreSummary(co, deptAxis, FY_FROM, FY_TO);
  const branchSummary = await client.costCentreSummary(co, branchAxis, FY_FROM, FY_TO);
  for (const [label, s, wantDebit] of [
    ["Department", deptSummary, "53600.0000"],
    ["Branch", branchSummary, "45000.0000"],
  ] as const) {
    if (s.centres.length === 0) {
      throw new Error(
        `the ${label} Cost Centre Summary renders no centres, so the screen a ` +
          `human opens is empty and the seed has not fixed what it exists to fix`,
      );
    }
    if (!s.reconciles) {
      throw new Error(
        `the ${label} Cost Centre Summary does not reconcile: roll-up ` +
          `${s.total_debit}/${s.total_credit} vs flat ${s.allocated_debit}/` +
          `${s.allocated_credit}, difference ${s.difference_debit}/${s.difference_credit}`,
      );
    }
    if (s.unattached.length > 0) {
      throw new Error(
        `the ${label} axis has ${s.unattached.length} unattached centre(s), so a ` +
          `screen that did not render the unattached block would lose their money`,
      );
    }
    if (s.total_debit !== wantDebit) {
      throw new Error(
        `the ${label} Cost Centre Summary totals ${s.total_debit} Dr over FY 2026-27, ` +
          `expected ${wantDebit}`,
      );
    }
  }

  // ⛔⛔ PROOF 3 — THE OFF-AXIS DETECTOR, WHICH IS A SECOND, INDEPENDENT WITNESS
  // TO THE SAME OFFSET BUG. Both axes are allocate_non_revenue = false, so an
  // allocation on a ledger that is NOT in the Profit & Loss is off-axis. HDFC
  // Bank is a Bank Accounts ledger. So a split that had landed on the bank leg
  // would make off_axis_postings non-zero HERE, computed by the server from the
  // stored rows, with no help from anything this file believes.
  //
  // And the GAP is asserted to be REAL rather than merely present: an axis with
  // nothing unallocated cannot show what the report is for.
  const catSummary = await client.costCategorySummary(co, FY_FROM, FY_TO);
  if (!catSummary.reconciles) {
    throw new Error(
      `the Cost Category Summary does not reconcile: ${catSummary.notes.join(" | ")}`,
    );
  }
  if (catSummary.rows.length < 2) {
    throw new Error(
      `the Cost Category Summary has ${catSummary.rows.length} row(s); with one axis ` +
        `the demo cannot show that axes describe the same money in parallel and ` +
        `must never be added together`,
    );
  }
  for (const row of catSummary.rows) {
    if (row.off_axis_postings !== 0) {
      throw new Error(
        `the ${row.cost_category} axis carries ${row.off_axis_postings} OFF-AXIS ` +
          `posting(s) worth ${row.off_axis_debit} Dr — a cost split landed on a ` +
          `ledger this axis may not carry, which on a two-leg payment means it ` +
          `landed on the bank leg`,
      );
    }
    if (row.partly_allocated_postings !== 0) {
      throw new Error(
        `the ${row.cost_category} axis has ${row.partly_allocated_postings} PART-` +
          `allocated posting(s), short by ${row.partly_allocated_shortfall} — the ` +
          `deferred sum trigger forbids that, so this means the guarantee is gone`,
      );
    }
    if (!row.reconciles) {
      throw new Error(
        `the ${row.cost_category} axis does not reconcile: allocated ` +
          `${row.allocated_debit} + unallocated ${row.unallocated_debit} != allocable ` +
          `${row.allocable_debit} (difference ${row.difference_debit})`,
      );
    }
    if (isZeroMoney(row.unallocated_debit) && isZeroMoney(row.unallocated_credit)) {
      throw new Error(
        `the ${row.cost_category} axis has NOTHING unallocated, so the one column ` +
          `the Cost Category Summary exists for renders as a row of zeros and a ` +
          `wrong implementation of the gap would be indistinguishable from a right one`,
      );
    }
  }
  const deptRow = catSummary.rows.find((r) => r.cost_category === "Department");
  const branchRow = catSummary.rows.find((r) => r.cost_category === "Branch");
  if (!deptRow || !branchRow) {
    throw new Error(
      `the Cost Category Summary is missing an axis: ` +
        `[${catSummary.rows.map((r) => r.cost_category).join(", ")}]`,
    );
  }
  // The two axes MUST disagree about the gap, or the demo teaches that every
  // axis covers the same ground and the per-axis reading is decorative.
  if (deptRow.unallocated_debit === branchRow.unallocated_debit) {
    throw new Error(
      `Department and Branch report the SAME unallocated debit ` +
        `(${deptRow.unallocated_debit}); the demo cannot then show that coverage ` +
        `is a fact about an AXIS rather than about the books`,
    );
  }

  console.log(
    `  cost:       Cost Centre Summary FY 2026-27 — Department ${deptSummary.total_debit} Dr ` +
      `over ${deptSummary.centres.length} centres, Branch ${branchSummary.total_debit} Dr ` +
      `over ${branchSummary.centres.length}  (⛔ NOT ADDABLE: the same money on two axes)`,
  );
  for (const row of catSummary.rows) {
    console.log(
      `              ${row.cost_category.padEnd(11)} allocable ${row.allocable_debit} Dr` +
        `  allocated ${row.allocated_debit}  GAP ${row.unallocated_debit}` +
        ` (${row.unallocated_postings} posting(s))`,
    );
  }
  console.log(
    `              allocation legs verified against the READ-BACK: all 4 splits on "Rent", ` +
      `0 off-axis postings on either axis`,
  );

  // PROOF 4 — the order books show a real OUTSTANDING balance, derived.
  //
  // ⛔ EVERY FIGURE BELOW IS COMPUTED BY THE SERVER ON THIS READ, by summing
  // order_fulfilment across CURRENT, non-deleted voucher versions. There is no
  // fulfilled_qty column to have gone stale, and the seed asserts the derived
  // figures rather than the ones it sent — which is the only way to notice that
  // a link was written against a superseded version or not written at all.
  const soAfter = await client.getSalesOrder(co, salesOrderID);
  const poAfter = await client.getPurchaseOrder(co, purchaseOrderID);
  const wantLines: [string, string, string, string][] = [
    // [order, item, fulfilled, outstanding]
    [SO_NO, "MS Angle 50x50", "30.0000", "20.0000"],
    [SO_NO, "SS Sheet 2mm", "0.0000", "10.0000"],
    [PO_NO, "MS Angle 50x50", "120.0000", "80.0000"],
  ];
  for (const [orderNo, item, wantFulfilled, wantOutstanding] of wantLines) {
    const view = orderNo === SO_NO ? soAfter : poAfter;
    const line = view.lines.find((l) => l.stock_item === item);
    if (!line) {
      throw new Error(`${orderNo} has no line for ${item}`);
    }
    if (line.fulfilled_qty !== wantFulfilled || line.outstanding_qty !== wantOutstanding) {
      throw new Error(
        `${orderNo} line ${line.line_no} (${item}) is ${line.fulfilled_qty} fulfilled / ` +
          `${line.outstanding_qty} outstanding, expected ${wantFulfilled} / ` +
          `${wantOutstanding}. A link written against a superseded voucher version, ` +
          `or not written at all, looks exactly like this and nothing else reports it.`,
      );
    }
  }
  // PARTIAL, on both sides. A fully-delivered order leaves the order book EMPTY,
  // which is the state this whole section exists to end, so it is refused here
  // rather than discovered on screen.
  const sob = await client.salesOrderBook(co);
  const pob = await client.purchaseOrderBook(co);
  if (sob.total_outstanding_value !== "16500.0000") {
    throw new Error(
      `the Sales Order Book totals ${sob.total_outstanding_value} outstanding, ` +
        `expected 16500.0000 (20 MS Angle @ 300 + 10 SS Sheet @ 1050)`,
    );
  }
  if (pob.total_outstanding_value !== "2060.0000") {
    throw new Error(
      `the Purchase Order Book totals ${pob.total_outstanding_value} outstanding, ` +
        `expected 2060.0000 (80 MS Angle @ 25.75)`,
    );
  }
  if (sob.rows.length === 0 || pob.rows.length === 0) {
    throw new Error(
      `an order book renders no rows (sales ${sob.rows.length}, purchase ` +
        `${pob.rows.length}) — the screen a human opens is still empty`,
    );
  }
  // The FULFILMENT LINK IS STORED ON THE VOUCHER, and a screen that rebuilds an
  // alter without it would silently re-open the order. Proved by reading it
  // back off the delivery note rather than off the order.
  const dnBack = await client.getVoucher(co, dnAngle);
  const dnLinks = dnBack.fulfilments ?? [];
  if (dnLinks.length !== 1 || dnLinks[0]!.order_no !== SO_NO || dnLinks[0]!.qty !== "30.0000") {
    throw new Error(
      `the delivery note stores ${dnLinks.length} fulfilment link(s) ` +
        `(${dnLinks.map((f) => `${f.order_no} ${f.qty}`).join(", ")}), expected one ` +
        `of 30.0000 against ${SO_NO}`,
    );
  }
  // ⛔ line_no is 1-BASED and STORED (unlike a cost allocation's posting_seq,
  // which is 0-based and re-derived). If it came back 0 the link would point at
  // no stock_movement row at all.
  if (dnLinks[0]!.line_no !== 1) {
    throw new Error(
      `the delivery note's fulfilment names inventory line ${dnLinks[0]!.line_no}; ` +
        `stock_movement.line_no is 1-BASED, so a 0 here is an index sent as a line number`,
    );
  }

  console.log(
    `  orders:     ${SO_NO} (Acme, 2 lines) 30/50 MS Angle delivered + 10 SS Sheet untouched ` +
      `-> ${sob.total_outstanding_value} outstanding`,
  );
  console.log(
    `              ${PO_NO} (Steel Suppliers) 120/200 MS Angle received ` +
      `-> ${pob.total_outstanding_value} outstanding`,
  );
  console.log(
    "              ⛔ neither order moves closing stock: an order is a PROMISE, " +
      "not an entry — the figures above are DERIVED on every read",
  );

  // ---- Optional: a pending OCR draft for the Bill Inbox --------------------
  // Uses the SAME benchmark fixture the OCR e2e drives, with its placeholder
  // ledger ids rewritten to this company's real ones, so the draft that lands in
  // the inbox resolves against these books and can actually be accepted.
  if (withInsightd) {
    try {
      const insightd = createInsightdClient({ baseUrl: insightdUrl });

      // The bill is BUILT FROM THESE BOOKS rather than string-patched out of a
      // benchmark fixture. The old approach remapped ledger IDs but left the
      // fixture's own content behind, so the Bill Inbox showed a bill dated
      // 2025-05-11 (eleven months BEFORE these books begin), from "Acme
      // Components Pvt Ltd" and "Steel widgets" — names that exist nowhere in
      // this company — proposing CGST+SGST for a 27->27 supply that, for a
      // Karnataka company, is inter-state and must be IGST. Accepting it would
      // have posted an out-of-period, GST-wrong voucher.
      const BILL_NO = "PF/2026-27/0117";
      const BILL_DATE = "2026-07-10"; // inside FY 2026-27, after books begin
      const BILL_HSN = "7216"; // angles/shapes/sections of iron or steel
      const BILL_QTY = 40;
      const BILL_RATE = 450;
      const BILL_TAXABLE = BILL_QTY * BILL_RATE; // 18,000
      const BILL_RATE_PCT = 18;
      const BILL_DESC = "MS Angle 50x50x5mm";

      // 27 (supplier) vs 29 (place of supply = where WE take delivery) => IGST.
      const ocrLine = (text: string, y: number, confidence: number) => ({
        text,
        bbox: [10, y, 520, y + 20],
        confidence,
      });

      const fixture = {
        id: "demo-traders-bill-inbox",
        description: `MH(27)->KA(29) purchase @${BILL_RATE_PCT}.00 -> IGST; clean scan`,
        rulesetVersion: "gst-2024-09",
        ocr_quality: "clean",
        // The deterministic classifier reads this block to resolve the supplier
        // to a REAL ledger. Without it the match falls through to the
        // "ambiguous" path (ledger_id null), and mapper.py then falls back to
        // using the place of supply AS the supplier state — which makes every
        // bill look intra-state and propose the wrong split.
        classify: {
          doc_type: "purchase_invoice",
          is_purchase: true,
          supplier_ledger_id: pune,
        },
        input: {
          pages: [{ png_b64: "", width: 1240, height: 1754, dpi: 200 }],
          ocr: {
            lines: [
              ocrLine("TAX INVOICE", 10, 0.99),
              ocrLine(`Invoice No: ${BILL_NO}`, 40, 0.98),
              ocrLine(`Date: ${BILL_DATE}`, 70, 0.98),
              ocrLine(`Supplier: ${PARTIES.pune.name}`, 100, 0.97),
              ocrLine(`GSTIN: ${PARTIES.pune.gstin}`, 130, 0.96),
              // PII the redaction stage must strip before anything leaves the
              // boundary — kept so the Bill Inbox still demonstrates that.
              ocrLine("PAN: AACCP2345Q", 160, 0.96),
              ocrLine("Bank IFSC: HDFC0004321", 190, 0.95),
              ocrLine(`Place of Supply: ${HOME_STATE}`, 220, 0.97),
              ocrLine(
                `${BILL_DESC} HSN ${BILL_HSN} qty ${money(BILL_QTY)} rate ${money(BILL_RATE)} ` +
                  `${money(BILL_TAXABLE)} @ ${money(BILL_RATE_PCT)}%`,
                260,
                0.96,
              ),
            ],
            mean_confidence: 0.967,
          },
          inferred: {
            invoice_no: BILL_NO,
            invoice_date: BILL_DATE,
            // Place of supply for goods is the DESTINATION: our state.
            place_of_supply_state_code: HOME_STATE,
            reverse_charge: false,
            exempt: false,
            line_items: [
              {
                description: BILL_DESC,
                hsn: BILL_HSN,
                qty: money(BILL_QTY),
                rate: money(BILL_RATE),
                taxable_value: money(BILL_TAXABLE),
                gst_rate_pct: money(BILL_RATE_PCT),
                ledger_id: purchases,
                ledger_name: "Raw Material Purchases",
                ledger_confidence: 0.95,
              },
            ],
            totals: {
              taxable: money(BILL_TAXABLE),
              invoice_total: money(
                BILL_TAXABLE + (BILL_TAXABLE * BILL_RATE_PCT) / 100,
              ),
            },
          },
          // Every id here is a REAL ledger of this company, so the draft the
          // inbox shows resolves against these books and can be accepted.
          tenant: {
            company_id: co,
            home_state_code: HOME_STATE,
            ledgers: [
              {
                id: pune,
                name: PARTIES.pune.name,
                gstin_masked:
                  PARTIES.pune.gstin.slice(0, 5) + "\u2026" + PARTIES.pune.gstin.slice(-3),
                // The supplier's OWN state. mapper.py reads this to decide the
                // split, so 27 here vs place-of-supply 29 is what makes the
                // draft propose IGST.
                state_code: PARTIES.pune.state,
                kind: "party",
              },
              { id: purchases, name: "Raw Material Purchases", kind: "purchase" },
              { id: inCgst, name: "Input CGST", kind: "tax" },
              { id: inSgst, name: "Input SGST", kind: "tax" },
              { id: inIgst, name: "Input IGST", kind: "tax" },
            ],
            cgst_ledger_id: inCgst,
            sgst_ledger_id: inSgst,
            igst_ledger_id: inIgst,
          },
        },
        expected: {},
      };

      const draft = await insightd.createDraft(co, {
        source: "upload",
        tenant: fixture.input.tenant as never,
        fixture,
      });

      // Assert the draft actually landed on the inter-state path. A silent
      // CGST+SGST here would mean the demo teaches the wrong split.
      const g = draft.body.gst;
      const igst = draft.body.postings.find((p) => p.ledger_id === inIgst);
      if (!igst) {
        throw new Error(
          `draft proposed no IGST leg — supplier ${g?.supplier_state_code} / POS ` +
            `${g?.place_of_supply_state_code} should be inter-state`,
        );
      }
      console.log(
        `  bill inbox: 1 pending OCR draft — ${PARTIES.pune.name} ${BILL_NO} ` +
          `dated ${BILL_DATE}, IGST ${igst.amount} (27->29 inter-state), ` +
          `${draft.redaction.total} PII fields redacted — Gateway → Bill Inbox`,
      );
    } catch (err) {
      console.log(
        `  bill inbox: SKIPPED — ${err instanceof Error ? err.message : String(err)} (insightd at ${insightdUrl})`,
      );
    }
  }

  // ---- Report the state so the console output is itself the proof ----------
  const tb = await client.getTrialBalance(co);
  const bs = await client.getBalanceSheet(co);
  const os = await client.getOutstandings(co, { kind: "RECEIVABLE" });
  console.log(
    `\n  trial balance: Dr ${tb.total_debit} / Cr ${tb.total_credit}  balanced=${tb.balanced}` +
      `\n  balance sheet: assets ${bs.assets.total} / liabilities ${bs.liabilities.total}  balanced=${bs.balanced}` +
      `\n  receivables:   ${os.parties.length} parties, total ${os.total}`,
  );
  // SELF-CHECK, not a note. Sundry Debtors on the Balance Sheet is computed from
  // ledger_balance; the Outstandings total is computed from bill_allocation.
  // They are two independent paths over the same books and MUST agree to the
  // paisa. They did not before this pass (INV-003's alter restated the postings
  // but not the bill reference, and Outstandings summed superseded versions), so
  // the seed now asserts it rather than printing a disclaimer.
  const debtors = bs.assets.nodes
    .flatMap((n) => n.children ?? [])
    .find((n) => n.name === "Sundry Debtors");
  if (!debtors) throw new Error("Balance Sheet has no Sundry Debtors node");
  if (debtors.amount !== os.total) {
    throw new Error(
      `Balance Sheet Sundry Debtors ${debtors.amount} != Outstandings ${os.total}`,
    );
  }
  console.log(
    `  cross-check:   Sundry Debtors ${debtors.amount} == Outstandings ${os.total}  ✓`,
  );

  console.log(`\n  OPEN IT:  ${uiUrl}/?company=${co}`);
  console.log(`  SIGN IN:  user name "${SEED_SUBJECT}", password "${DEMO_PASSWORD}"\n`);
  console.log("  Then try:  F8/F9 post · Day Book → Enter to drill · Alt+G Trial Balance");
  console.log("             F5 group-wise · F12 configure · Ctrl+F filter · Alt+E export");
  console.log("             F1 help · F11 features · Ctrl+N calculator · Esc to go back\n");
}

main().catch((err: unknown) => {
  console.error("seed-demo FAILED:", err instanceof Error ? err.message : err);
  process.exit(1);
});
