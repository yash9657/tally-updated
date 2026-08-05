import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  act,
} from "@testing-library/react";

import { TDSChallansScreen } from "./TDSChallansScreen";
import { FeaturesScreen } from "./FeaturesScreen";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../app/ScreenActions";
import { createMockClient, MOCK_TDS_DEDUCTIONS } from "../api/mock";
import { ApiError } from "../api";
import type {
  AllocatableDeduction,
  CoredClient,
  CreateTDSChallanRequest,
  TDSChallan,
  TDSChallanAllocation,
  TDSChallanListResponse,
} from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * THE TDS CHALLAN SCREEN (T6).
 *
 * ⚠️ WHICH TESTS PROVE WHAT, because this file deliberately contains two kinds
 * and only ONE of them could have caught the Alt+O class of bug:
 *
 *  - The suites below the divider marked "THROUGH THE REAL SHELL" render
 *    `AppShell` and press the REAL COMBO. They are the only tests here that
 *    exercise AppShell's `SCREEN_ACTIONS` map — the thirteenth registration
 *    point, which is not in HANDOFF-FEATURE-LOCK.md §5.2's list of twelve and
 *    which shipped missing for Alt+O with 459 green tests. Delete the
 *    `recordTDSChallan: "recordTDSChallan"` line from that map and these fail;
 *    nothing else in the repo does.
 *  - Every other suite calls the screen-action dispatcher DIRECTLY, exactly as
 *    the eleven tests that missed Alt+O did. They prove the screen; they prove
 *    nothing about routing. That is stated here so nobody reads a green run as
 *    evidence that the key works.
 *
 * ⚠️ AND HOW REFUSAL TEXT IS ASSERTED. The mock's sentences are HAND COPIES of
 * the server's, and a hand copy is what went stale for two days on the register.
 * So no refusal is retyped here: the test CAPTURES the `ApiError` the client
 * throws for the same request and looks for THAT message in the DOM. A wording
 * change moves both together, and the only way to fail is to stop rendering the
 * server's sentence — which is the thing worth catching.
 *
 * ⛔ WHAT THIS FILE USED TO CERTIFY AND MUST NOT AGAIN. The deposit form had
 * free-text boxes for voucher_id and leg_seq, and these tests filled them from
 * MOCK_TDS_DEDUCTIONS — whose ids were NOT uuids, so every request they proved
 * would have been refused by cored with "allocation 1: invalid voucher_id". No
 * screen in this product displays a voucher UUID or a leg ordinal, so no operator
 * could have filled those boxes at all. The boxes are gone; the fixtures carry
 * uuids; and the tests below assert the WIRE — that the identifiers and the
 * amount are the SERVED ones and nothing was typed.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

const SNAP = {
  screen: "tds-challans",
  screenStackDepth: 2,
  canReturnToVoucher: false,
} as unknown as NavSnapshot;

/** The mock fixtures, which are also what the mock serves as allocatable. */
const P1 = MOCK_TDS_DEDUCTIONS[0]!; // 194J, leg 0, 20000.0000
const P2 = MOCK_TDS_DEDUCTIONS[1]!; // 194C, leg 0, 3333.3000 — the paise case
const P4 = MOCK_TDS_DEDUCTIONS[3]!; // 194H, LEG 1, 5000.0000 — the non-zero leg

function allocationView(
  over: Partial<TDSChallanAllocation> = {},
): TDSChallanAllocation {
  return {
    voucher_id: "voucher-1",
    voucher_no: "P1",
    section: "194J",
    leg_seq: 0,
    deductee_name: "Chitra Consulting",
    allocated_rupees: "20000.0000",
    deduction_on_record: true,
    deduction_tax_rupees: "20000.0000",
    broken_by_alter: false,
    ...over,
  };
}

function challanView(over: Partial<TDSChallan> = {}): TDSChallan {
  return {
    id: "ch-1",
    bsr_code: "0004329",
    deposit_date: "2026-05-07",
    challan_no: "05122",
    by_book_entry: false,
    tax_rupees: "20000.0000",
    interest_rupees: "0.0000",
    fee_rupees: "0.0000",
    penalty_rupees: "0.0000",
    total_deposited_rupees: "20000.0000",
    minor_head: "400",
    minor_head_description: "a demand raised on assessment",
    tax_year_start: 2026,
    tax_year_label: "2026-27",
    allocation_count: 1,
    allocated_total_rupees: "20000.0000",
    unallocated_tax_rupees: "0.0000",
    allocations: [allocationView()],
    recorded_by: "asha",
    recorded_at: "2026-05-07T10:00:00Z",
    ...over,
  };
}

/** One served allocatable row. The UUID and the leg are never displayed. */
function allocatableView(
  over: Partial<AllocatableDeduction> = {},
): AllocatableDeduction {
  return {
    voucher_id: "5c3e9d2a-1b4f-4d6e-8a90-11c2d3e4f501",
    leg_seq: 0,
    voucher_no: "P1",
    deductee_name: "Chitra Consulting",
    section: "194J",
    deduction_date: "2026-05-05",
    tax_rupees: "20000.0000",
    already_paid: false,
    ...over,
  };
}

function listView(
  over: Partial<TDSChallanListResponse> = {},
): TDSChallanListResponse {
  return {
    tax_year_start: 2026,
    tax_year_label: "2026-27",
    challans: [],
    minor_heads: [
      { code: "200", description: "tax the deductor pays over of its own motion" },
      { code: "400", description: "a demand raised on assessment" },
      { code: "100", description: "advance tax" },
    ],
    allocatable_deductions: [allocatableView()],
    ...over,
  };
}

/**
 * Render the screen with a dispatcher that fires its registered actions the way
 * AppShell would — which also proves the actions are registered at all, and
 * nothing more than that (see the header).
 */
function renderScreen(client: CoredClient) {
  let fire: (
    kind: "recordTDSChallan" | "accept",
  ) => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const res = dispatch(kind as any, SNAP);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <TDSChallansScreen />;
  }
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <ScreenActionsProvider>
        <Harness />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  return {
    record: () => act(async () => void (await fire("recordTDSChallan"))),
    accept: () => act(async () => void (await fire("accept"))),
    lastMessage: async (kind: "recordTDSChallan" | "accept") => {
      let out: string | void = undefined;
      await act(async () => {
        out = await fire(kind);
      });
      return out as string | undefined;
    },
  };
}

/** A client that serves one hand-built list response (and counts the calls). */
function listingClient(res: TDSChallanListResponse): CoredClient {
  return {
    ...createMockClient({ seed: false }),
    listTDSChallans: vi.fn(async () => res),
  } as unknown as CoredClient;
}

/** Ask the screen for a year, the way an operator does. */
async function askYear(year = "2026") {
  const input = screen.getByLabelText("Tax year begins in");
  fireEvent.change(input, { target: { value: year } });
  fireEvent.blur(input);
  await screen.findByText(/Deposits recorded for/);
}

/**
 * The picker's checkbox for a served deduction, found BY THE FIVE FACTS a human
 * recognises it by. Never a voucher id and never a leg — if either were needed to
 * find the control, no operator could have found it either.
 */
function payLabel(d: {
  voucher_no: string;
  section: string;
  deductee_name: string;
  deduction_date: string;
  tax_rupees: string;
}): string {
  return `Pay the ${d.tax_rupees} deducted on voucher ${d.voucher_no} under ${d.section} from ${d.deductee_name} on ${d.deduction_date}`;
}

function tick(d: Parameters<typeof payLabel>[0]) {
  fireEvent.click(screen.getByLabelText(payLabel(d)));
}

/**
 * Fill the whole deposit form. Every value is TEXT; nothing here is a number —
 * and NOTHING here keys an allocation, because the screen no longer has a box
 * for one. `pay` names the deductions to TICK in the served picker.
 */
function fillForm(over: {
  bsr?: string;
  date?: string;
  serial?: string;
  tax?: string;
  minorHead?: string;
  year?: string;
  recordedBy?: string;
  pay?: Parameters<typeof payLabel>[0][];
}) {
  const set = (label: string, value: string) =>
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
  set("Deposit date", over.date ?? "2026-05-07");
  set("BSR code", over.bsr ?? "0004329");
  set("Challan serial number", over.serial ?? "05122");
  set("Tax", over.tax ?? "20000");
  set("Tax year this deposit belongs to", over.year ?? "2026");
  set("Recorded by", over.recordedBy ?? "asha");
  fireEvent.click(screen.getByLabelText(`Minor head ${over.minorHead ?? "200"}`));
  for (const d of over.pay ?? [P1]) tick(d);
}

/**
 * The sentence THE CLIENT throws for a request — captured, never retyped.
 *
 * Safe to call before driving the same request through the screen: every refusal
 * used below is raised BEFORE anything is stored, so the capture cannot change
 * what the screen then meets.
 */
async function refusalFor(
  client: CoredClient,
  req: CreateTDSChallanRequest,
): Promise<string> {
  try {
    await client.createTDSChallan(COMPANY, req);
  } catch (e) {
    if (e instanceof ApiError) return e.message;
    throw e;
  }
  throw new Error("expected the client to refuse this request");
}

/** The request the form below builds, so the capture and the screen agree. */
function requestFor(
  over: Partial<CreateTDSChallanRequest> = {},
): CreateTDSChallanRequest {
  return {
    bsr_code: "0004329",
    deposit_date: "2026-05-07",
    challan_no: "05122",
    by_book_entry: false,
    tax_rupees: "20000",
    interest_rupees: "",
    fee_rupees: "",
    penalty_rupees: "",
    minor_head: "200",
    tax_year_start: 2026,
    allocations: [
      {
        voucher_id: P1.voucher_id,
        section: P1.section,
        leg_seq: P1.leg_seq,
        amount_rupees: P1.tax_rupees,
      },
    ],
    recorded_by: "asha",
    ...over,
  };
}

describe("the tax year is ASKED FOR, never derived from a date", () => {
  it("shows no register and makes NO request until the year is given", () => {
    const client = listingClient(listView());
    renderScreen(client);
    expect(screen.getByTestId("tds-challans-year-prompt")).toBeTruthy();
    // A defaulted year would answer a question the operator did not ask while
    // looking exactly like the one they did — a challan paid on 7 April belongs
    // to the year that ended a week earlier.
    expect(client.listTDSChallans).not.toHaveBeenCalled();
  });

  it("asks the server for the year the operator typed", async () => {
    const client = listingClient(listView());
    renderScreen(client);
    await askYear("2026");
    expect(client.listTDSChallans).toHaveBeenCalledWith(COMPANY, 2026);
    // The label comes from the SERVER's response, not from arithmetic here.
    expect(screen.getByText(/Deposits recorded for 2026-27/)).toBeTruthy();
  });

  /**
   * ⛔ THE FIX FOR "a garbage tax year silently renders an empty register".
   *
   * This screen used to send `Number.isNaN(year) ? 0 : year` on the strength of a
   * comment claiming "the server refuses one outside 2025–2200 with its own
   * sentence". THAT IS FALSE FOR THE GET: `handleListTDSChallans` refuses only an
   * EMPTY or non-numeric param, `strconv.Atoi("0")` succeeds, and ListTDSChallans
   * has no range check at all — the 2025–2200 rule is on the WRITE path. So "not
   * a year" answered 200 with zero rows and this screen printed "No deposit has
   * been recorded against 0-01", which is precisely the confident wrong answer
   * its own header refuses.
   *
   * Revert the guard and this test fails on the FIRST assertion: the request goes
   * out. (Against the mock it used to fail on the third instead, because the mock
   * INVENTED a 400 for year 0 that the server does not make — a test pinned to
   * that refusal certified behaviour that does not exist. The mock no longer
   * invents it.)
   */
  it("refuses a year it knows is not one, LOCALLY, and sends nothing", async () => {
    const client = listingClient(listView());
    renderScreen(client);
    const input = screen.getByLabelText("Tax year begins in");
    fireEvent.change(input, { target: { value: "not a year" } });
    fireEvent.blur(input);

    expect(client.listTDSChallans).not.toHaveBeenCalled();
    const said = screen.getByTestId("tds-challans-year-refusal").textContent ?? "";
    // It NAMES the reason and what to type instead.
    expect(said).toContain("not a year");
    expect(said).toMatch(/2026 for 2026-27/);
    // And no register — least of all an empty one for a year nobody asked for.
    expect(screen.queryByText(/Deposits recorded for/)).toBeNull();
    expect(screen.queryByTestId("tds-challans-empty")).toBeNull();
  });

  it("refuses a year outside the range a challan can be recorded against", async () => {
    const client = listingClient(listView());
    renderScreen(client);
    const input = screen.getByLabelText("Tax year begins in");
    fireEvent.change(input, { target: { value: "1999" } });
    fireEvent.blur(input);
    // The GET would answer 200 and an empty list for 1999. No challan can exist
    // there (0069's CHECK), so an empty register would be a true sentence that
    // reads as an answer to a question the operator did not mean to ask.
    expect(client.listTDSChallans).not.toHaveBeenCalled();
    expect(
      screen.getByTestId("tds-challans-year-refusal").textContent,
    ).toMatch(/2025–2200/);
  });

  it("renders the SERVER's sentence when the server is the one refusing", async () => {
    // The screen's local guard only covers what it can know. Everything else is
    // still the server's sentence, rendered verbatim.
    const client = {
      ...createMockClient({ seed: false }),
      listTDSChallans: vi
        .fn()
        .mockRejectedValue(new ApiError(400, "masters: read challans: boom")),
    } as unknown as CoredClient;
    renderScreen(client);
    await askYearRaw("2026");
    expect(
      (await screen.findByTestId("tds-challans-error")).textContent,
    ).toContain("masters: read challans: boom");
  });
});

/** Type a year and blur, without waiting for a register that may never come. */
async function askYearRaw(year: string) {
  const input = screen.getByLabelText("Tax year begins in");
  fireEvent.change(input, { target: { value: year } });
  await act(async () => {
    fireEvent.blur(input);
  });
}

describe("the register shows what the server sent", () => {
  it("renders the CIN parts, the total, and the minor head WITH its description", async () => {
    renderScreen(listingClient(listView({ challans: [challanView()] })));
    await askYear();
    const card = screen.getByTestId("tds-challan:05122");
    // TEXT, with leading zeros intact: 0004329 is not 4329 and 05122 is not 5122.
    expect(card.textContent).toContain("0004329");
    expect(card.textContent).toContain("05122");
    expect(card.textContent).toContain("2026-05-07");
    // The total is the SERVER's derived figure, not a sum computed here.
    expect(
      screen.getByTestId("tds-challan-total:05122").textContent,
    ).toContain("20,000.00");
    // The description travels with the code precisely so a row does not read as
    // three bare numbers, whichever is listed first.
    expect(card.textContent).toContain("400");
    expect(card.textContent).toContain("a demand raised on assessment");
    // And its allocations are on the same card.
    expect(screen.getByTestId("tds-allocation:P1:194J:0")).toBeTruthy();
    expect(card.textContent).toContain("Chitra Consulting");
  });

  it("prints ONE whole-rupee figure ONE way on a card", async () => {
    // The card used to print `tax_rupees` as "20,000.00" in the deposit line and
    // "20000.0000" four lines later in "Allocated … of … tax": one figure, two
    // renderings, four lines apart, reading as two different numbers. The four
    // challan boxes are WHOLE RUPEES by the format's rule, so they are formatted
    // everywhere — while the 4-dp figures whose paise are the subject (the
    // allocation columns, the allocated total and the unallocated remainder) stay
    // exact. A card where the two differ is the only way to tell them apart.
    renderScreen(
      listingClient(
        listView({
          challans: [
            challanView({
              tax_rupees: "20000.0000",
              total_deposited_rupees: "20000.0000",
              allocated_total_rupees: "19999.5000",
              unallocated_tax_rupees: "0.5000",
              allocations: [
                allocationView({
                  allocated_rupees: "19999.5000",
                  deduction_tax_rupees: "19999.5000",
                }),
              ],
            }),
          ],
        }),
      ),
    );
    await askYear();
    const card = screen.getByTestId("tds-challan:05122");
    // The challan's tax box: formatted, and NOWHERE raw.
    expect(card.textContent).toContain("20,000.00");
    expect(card.textContent).not.toContain("20000.0000");
    // The paise figures: raw, and never rounded to two decimals — rounding is
    // what the remainder is reporting.
    expect(card.textContent).toContain("19999.5000");
    expect(
      screen.getByTestId("tds-challan-unallocated:05122").textContent,
    ).toBe("0.5000");
  });

  it("prints a paise remainder EXACTLY and does not call it an error", async () => {
    // ₹3,333.30 of tax paid with a whole-rupee ₹3,334 deposit: the ordinary case.
    renderScreen(
      listingClient(
        listView({
          challans: [
            challanView({
              challan_no: "05123",
              tax_rupees: "3334.0000",
              total_deposited_rupees: "3334.0000",
              allocated_total_rupees: "3333.3000",
              unallocated_tax_rupees: "0.7000",
              allocations: [
                allocationView({
                  voucher_no: "P2",
                  section: "194C",
                  allocated_rupees: "3333.3000",
                  deduction_tax_rupees: "3333.3000",
                }),
              ],
            }),
          ],
        }),
      ),
    );
    await askYear();
    // The exact figure, unrounded: rounding is the very thing being reported —
    // which is why `unallocated_tax_rupees` and the 4-dp allocation columns are
    // the ONE place raw amounts stay raw.
    expect(
      screen.getByTestId("tds-challan-unallocated:05123").textContent,
    ).toBe("0.7000");
    expect(
      screen.getByTestId("tds-challan:05123").textContent,
    ).toContain("3333.3000");
    expect(
      screen.getByText(/A non-zero unallocated figure is not an error/),
    ).toBeTruthy();
  });

  it("shows an allocation the books have outgrown, and never calls the return fine", async () => {
    // The mock cannot produce these flags (no mock voucher can remove a
    // deduction leg), so this is a hand-built response by necessity.
    renderScreen(
      listingClient(
        listView({
          challans: [
            challanView({
              allocations: [
                allocationView({
                  deductee_name: "",
                  deduction_on_record: false,
                  deduction_tax_rupees: "0.0000",
                  broken_by_alter: true,
                }),
              ],
            }),
          ],
        }),
      ),
    );
    await askYear();
    const flag = screen.getByTestId("tds-allocation-flag:P1:194J:0");
    // deduction_on_record false is SHOWN — the payment stands and the allocation
    // records it. An empty deductee cell would read as an unnamed deductee.
    expect(flag.textContent).toMatch(/ALTER removed this deduction/);
    // broken_by_alter is ADVISORY and the quarter-level refusal is the authority.
    expect(flag.textContent).toMatch(/REFUSES THE WHOLE QUARTER/);
    expect(flag.textContent).toMatch(/absence of a flag is not a clearance/);
  });

  it("states an empty register instead of implying nothing was deducted", async () => {
    renderScreen(listingClient(listView()));
    await askYear();
    const empty = screen.getByTestId("tds-challans-empty").textContent ?? "";
    expect(empty).toMatch(/does not mean no tax was deducted/);
  });
});

describe("the deposit form", () => {
  it("PRE-SELECTS NO MINOR HEAD, and renders the served list", async () => {
    // A fourth head the migration does not have: if this renders, the picker is
    // built from the payload and there is no TypeScript copy of the vocabulary.
    const served = listView({
      minor_heads: [
        ...listView().minor_heads,
        { code: "777", description: "a head no client could know" },
      ],
    });
    const h = renderScreen(listingClient(served));
    await askYear();
    await h.record();

    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
    expect(screen.getByLabelText("Minor head 777")).toBeTruthy();
    const radios = screen.getAllByRole("radio") as HTMLInputElement[];
    expect(radios.length).toBe(4);
    // Nothing chosen. Defaulting to 200 would assert that the deposit was made
    // of the company's own motion rather than against a demand.
    for (const r of radios) expect(r.checked).toBe(false);
  });

  it("refuses to open over no register, naming the reason", async () => {
    const h = renderScreen(listingClient(listView()));
    const msg = await h.lastMessage("recordTDSChallan");
    expect(msg).toMatch(/Ask for the tax year first/);
    expect(screen.queryByTestId("tds-challan-form")).toBeNull();
  });

  it("records a deposit and shows what was stored, including the remainder", async () => {
    const client = createMockClient({ seed: false });
    const h = renderScreen(client);
    await askYear();
    await h.record();
    // The paise fixture: ₹3,333.30 deducted, paid with a whole-rupee ₹3,334.
    // TICKED, not typed — and the amount is not asked for at all.
    fillForm({ serial: "05199", tax: "3334", pay: [P2] });
    await h.accept();

    // The form closes and the register is re-read for the year STORED ON the
    // challan (which is not necessarily the one on screen).
    expect(screen.queryByTestId("tds-challan-form")).toBeNull();
    expect(screen.getByTestId("tds-challan:05199")).toBeTruthy();
    expect(
      screen.getByTestId("tds-challan-unallocated:05199").textContent,
    ).toBe("0.7000");
  });

  it("pays several deductions with one deposit — the normal case", async () => {
    const client = createMockClient({ seed: false });
    const h = renderScreen(client);
    await askYear();
    await h.record();
    // One deposit covering a month of deductions: 20000 + 5000 = 25000.
    fillForm({ serial: "05201", tax: "25000", pay: [P1, P4] });
    await h.accept();
    const card = screen.getByTestId("tds-challan:05201");
    expect(card.textContent).toContain("Chitra Consulting");
    expect(card.textContent).toContain("Iyer Brokers");
    expect(
      screen.getByTestId("tds-challan-unallocated:05201").textContent,
    ).toBe("0.0000");
  });
});

/**
 * ⛔ THE BLOCKER: the grid asked for identifiers no screen displays.
 *
 * These tests assert THE WIRE, because that is where the defect lived: the form
 * looked fine and the request it built was refused by cored.
 */
describe("the allocation is CHOSEN, and the wire carries the served facts", () => {
  function spyClient() {
    const base = createMockClient({ seed: false });
    const createTDSChallan = vi.fn(base.createTDSChallan);
    return {
      client: { ...base, createTDSChallan } as unknown as CoredClient,
      createTDSChallan,
    };
  }

  it("asks for NO voucher id, NO leg and NO amount", async () => {
    const h = renderScreen(createMockClient({ seed: false }));
    await askYear();
    await h.record();
    // The three boxes that could not be filled by a human. If any of them comes
    // back, so does the blocker: a UUID and a 0-based ordinal appear on no screen
    // in this product, and the amount can only be the tax that was deducted.
    expect(screen.queryByLabelText("Voucher for allocation 1")).toBeNull();
    expect(screen.queryByLabelText("Leg for allocation 1")).toBeNull();
    expect(
      screen.queryByLabelText("Amount allocated on allocation 1"),
    ).toBeNull();
    // What IS on screen is what a human recognises.
    const row = screen.getByTestId(`tds-allocatable:P1:194J:0`);
    expect(row.textContent).toContain("P1");
    expect(row.textContent).toContain("Chitra Consulting");
    expect(row.textContent).toContain("194J");
    expect(row.textContent).toContain("2026-05-05");
    expect(row.textContent).toContain("20000.0000");
    // And the identifiers are NOT.
    expect(row.textContent).not.toContain(P1.voucher_id);
  });

  it("sends the served uuid, the served LEG and the served amount, untouched", async () => {
    const { client, createTDSChallan } = spyClient();
    const h = renderScreen(client);
    await askYear();
    await h.record();
    // P4 is on LEG 1. A picker that quietly sent leg 0 for every row would pass
    // on every other fixture and fail here — which is why P4 exists.
    fillForm({ serial: "05177", tax: "5000", pay: [P4] });
    await h.accept();

    expect(createTDSChallan).toHaveBeenCalledTimes(1);
    const req = createTDSChallan.mock.calls[0]![1] as CreateTDSChallanRequest;
    expect(req.allocations).toEqual([
      {
        voucher_id: P4.voucher_id,
        section: P4.section,
        leg_seq: P4.leg_seq,
        amount_rupees: P4.tax_rupees,
      },
    ]);
    // A UUID, because that is what the real server parses. The old fixtures were
    // "voucher-tds-fixture-p4" and cored refuses that as an invalid voucher_id —
    // which is how a green suite hid an unusable form.
    expect(req.allocations[0]!.voucher_id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    // The leg is the served one and NOT a default: leg 0 is a real leg.
    expect(req.allocations[0]!.leg_seq).toBe(1);
    // The amount is the deduction's tax EXACTLY, so the inequality refusal is
    // unreachable from this form. It is not an input, so there is nothing to
    // mis-key: the server refuses any inequality and this screen cannot produce
    // one.
    expect(req.allocations[0]!.amount_rupees).toBe(P4.tax_rupees);
  });

  it("shows an already-deposited deduction and will NOT let it be ticked", async () => {
    const client = createMockClient({ seed: false });
    // Deposit P1's tax, then come back for a second slip.
    await client.createTDSChallan(COMPANY, requestFor());
    const h = renderScreen(client);
    await askYear();
    await h.record();

    // SHOWN — served rather than filtered, so an operator who cannot find a
    // deduction can tell "not deducted in this year" from "already deposited".
    expect(screen.getByTestId("tds-allocatable-paid:P1:194J:0")).toBeTruthy();
    expect(
      screen.getByTestId("tds-allocatable:P1:194J:0").textContent,
    ).toContain("already deposited");
    // AND NOT SELECTABLE: ticking it could only earn a 409.
    expect(screen.queryByLabelText(payLabel(P1))).toBeNull();
    // The others are still offered.
    expect(screen.getByLabelText(payLabel(P2))).toBeTruthy();
  });

  it("EXPLAINS an empty list instead of rendering an empty picker", async () => {
    const h = renderScreen(listingClient(listView({ allocatable_deductions: [] })));
    await askYear();
    await h.record();
    const said = screen.getByTestId("tds-allocatable-empty").textContent ?? "";
    // WHICH case it is, and what to do about it.
    expect(said).toMatch(/carries any tax/);
    expect(said).toMatch(/nothing a deposit could pay for/);
    expect(said).toMatch(/key the voucher first/);
    // And it does not pretend the year has no deductions at all: a sub-threshold
    // deduction is real and is reported under the return's nil challan.
    expect(said).toMatch(/nil challan/);
  });

  it("says the picker is unavailable AND WHY, and still renders the register", async () => {
    const h = renderScreen(
      listingClient(
        listView({
          challans: [challanView()],
          allocatable_deductions: [],
          allocatable_error: "masters: read allocatable deductions: boom",
        }),
      ),
    );
    await askYear();
    // THE REGISTER IS STILL THERE. An operator came to look at recorded deposits
    // and that still works — which is why the server does not fail the request.
    expect(screen.getByTestId("tds-challan:05122")).toBeTruthy();
    const why = screen.getByTestId("tds-allocatable-error").textContent ?? "";
    expect(why).toContain("masters: read allocatable deductions: boom");
    expect(why).toMatch(/could not be read/);
    // And the form says the same thing where the picker would have been, rather
    // than showing the "no deduction carries tax" sentence, which would be false.
    await h.record();
    expect(screen.getByTestId("tds-allocatable-unavailable")).toBeTruthy();
    expect(screen.queryByTestId("tds-allocatable-empty")).toBeNull();
  });
});

describe("every refusal is the SERVER's sentence, rendered", () => {
  it("a duplicate CIN is a 409 that names the slip in the operator's hand", async () => {
    const client = createMockClient({ seed: false });
    // Record it once, then ask for the same CIN again. The second call refuses
    // before storing anything, so capturing the sentence is safe.
    await client.createTDSChallan(COMPANY, requestFor());
    const expected = await refusalFor(client, requestFor());
    expect(expected).toMatch(/409|already recorded/);

    const h = renderScreen(client);
    await askYear();
    await h.record();
    // P1 is already deposited by now and the picker will not offer it (that rule
    // is proved above), so a DIFFERENT deduction is ticked. The CIN is checked
    // before the allocations on purpose: re-posting an identical slip must name
    // the thing in the operator's hand, not its consequence.
    fillForm({ pay: [P4] });
    await h.accept();

    expect(screen.getByTestId("tds-challan-refusal").textContent).toBe(expected);
    // The form STAYS OPEN with the keyed values, because the operator has to
    // correct one field and not re-key a counterfoil.
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
  });

  it("a deposit of tax that names no deduction is refused BY THE SERVER", async () => {
    const client = createMockClient({ seed: false });
    const expected = await refusalFor(
      client,
      requestFor({ challan_no: "05188", allocations: [] }),
    );
    const h = renderScreen(client);
    await askYear();
    await h.record();
    // Everything keyed, nothing ticked. This screen does NOT invent a local
    // refusal: only the server knows that a zero-tax challan may name none.
    fillForm({ serial: "05188", pay: [] });
    await h.accept();
    expect(screen.getByTestId("tds-challan-refusal").textContent).toBe(expected);
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
  });

  it("renders a 403 on the READ as the DECISION it is", async () => {
    const denied = {
      ...createMockClient({ seed: false }),
      listTDSChallans: vi.fn().mockRejectedValue(new ApiError(403, "forbidden")),
    } as unknown as CoredClient;
    const h = renderScreen(denied);
    const input = screen.getByLabelText("Tax year begins in");
    fireEvent.change(input, { target: { value: "2026" } });
    fireEvent.blur(input);

    // Data Entry is refused these routes BY DESIGN (a bare `tds-challan` token
    // against default_access 'none'), so a blank register would be a false
    // statement about this company's deposits.
    const panel = await screen.findByTestId("tds-challans-denied");
    expect(panel.textContent).toMatch(/do not have permission/);
    expect(panel.textContent).toMatch(/allow tds-challan/);
    // And Alt+C answers the same decision rather than opening a form that
    // could only be refused on save.
    const msg = await h.lastMessage("recordTDSChallan");
    expect(msg).toMatch(/not permitted/);
  });

  it("renders a 403 on the WRITE with the SAME framing, naming the create rule", async () => {
    // Display and Create are separate ACL actions on ONE token, so "allow display
    // tds-challan" without create is a real configuration — and that operator
    // keys a whole counterfoil before meeting it. A bare ACL sentence with no
    // statement that it is a DECISION is what this replaces.
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      createTDSChallan: vi
        .fn()
        .mockRejectedValue(
          new ApiError(403, "forbidden: create tds-challan is not allowed"),
        ),
    } as unknown as CoredClient;
    const h = renderScreen(client);
    await askYear();
    await h.record();
    fillForm({});
    await h.accept();

    // THE SERVER'S SENTENCE, still verbatim and never replaced.
    expect(screen.getByTestId("tds-challan-refusal").textContent).toBe(
      "forbidden: create tds-challan is not allowed",
    );
    const framing =
      screen.getByTestId("tds-challan-create-denied").textContent ?? "";
    expect(framing).toMatch(/decision, not a fault/);
    expect(framing).toMatch(/allow create tds-challan/);
    // Nothing keyed is lost: the form is still up.
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
  });
});

/**
 * ⛔ THE ALT+O SIGNATURE, IN THE OTHER DIRECTION: a status line that reports
 * success while nothing appears on screen.
 *
 * Alt+C used to gate on a SEPARATE `loadedYear` while the form rendered on
 * `recording && data !== null`, and a failed load set `data = null` WITHOUT
 * clearing `loadedYear`. So a transient 500 on a re-read left the screen able to
 * answer "Record a TDS deposit (challan). Ctrl+A accepts it." with NO FORM.
 */
describe("the status line cannot claim a key worked when nothing happened", () => {
  it("Alt+C after a failed re-read refuses honestly instead of reporting success", async () => {
    const listTDSChallans = vi
      .fn()
      .mockResolvedValueOnce(listView())
      .mockRejectedValue(new ApiError(500, "masters: read challans: boom"));
    const client = {
      ...createMockClient({ seed: false }),
      listTDSChallans,
    } as unknown as CoredClient;
    const h = renderScreen(client);

    // The register loads once, so the old `loadedYear` is set.
    await askYear("2026");
    // Then the operator retypes the year and the re-read fails.
    await askYearRaw("2027");
    expect(
      (await screen.findByTestId("tds-challans-error")).textContent,
    ).toContain("boom");

    const msg = (await h.lastMessage("recordTDSChallan")) ?? "";
    // EITHER a form appears OR the message is an honest refusal. Never a success
    // line with no form — which is exactly what this used to answer.
    const formUp = screen.queryByTestId("tds-challan-form") !== null;
    if (!formUp) {
      expect(msg).not.toMatch(/Ctrl\+A accepts it/);
      expect(msg).toMatch(/no register on screen/);
    }
    // Belt and braces, stated as the invariant rather than the branch: the
    // success sentence and an absent form cannot co-occur.
    expect(formUp || !/Ctrl\+A accepts it/.test(msg)).toBe(true);
  });

  it("says so when the register goes away under an open form", async () => {
    const listTDSChallans = vi
      .fn()
      .mockResolvedValueOnce(listView())
      .mockRejectedValue(new ApiError(500, "masters: read challans: boom"));
    const client = {
      ...createMockClient({ seed: false }),
      listTDSChallans,
    } as unknown as CoredClient;
    const h = renderScreen(client);
    await askYear("2026");
    await h.record();
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();

    await askYearRaw("2027");
    // The form is built from the register's response (the minor heads and the
    // allocatable deductions both arrive in it), so it cannot stand without one.
    // It closing SILENTLY would be the same defect in miniature.
    expect(screen.queryByTestId("tds-challan-form")).toBeNull();
    expect(
      screen.getByTestId("tds-challan-form-lost").textContent,
    ).toMatch(/has been closed/);
  });
});

/**
 * ⛔ THE F11 HONESTY SURFACE MUST NOT DENY THIS SCREEN.
 *
 * The TDS / TCS row on F11 (Features) listed as missing "(2) NO CHALLAN REGISTER,
 * so nothing tracks what has actually been DEPOSITED" — in the commit that ADDS
 * the challan register — and said nothing at all about the deductor return
 * profile that ships beside it. An honesty surface is not allowed to be wrong in
 * the understating direction either, and nothing in the repo was checking it.
 *
 * This test lives HERE, next to the feature it protects, because the note is a
 * claim about THIS screen. It asserts the claims, not the wording, and it does
 * NOT assert the row's `state`: whether the row flips is a judgement made with
 * the shell driven in a browser, which no unit test holds.
 */
describe("the F11 feature note tells the truth about this screen", () => {
  async function featureNoteText(): Promise<string> {
    // Wrapped, because the screen's three real panels fetch on mount and the
    // note is static prose beside them.
    await act(async () => {
      render(
        <CompanyProvider
          client={createMockClient()}
          initialCompany={{ id: COMPANY, name: "Demo Traders" }}
        >
          <FeaturesScreen />
        </CompanyProvider>,
      );
    });
    // The row's note is the text of the table row that names the feature.
    const row = screen.getByText("TDS / TCS").closest("tr");
    return row?.textContent ?? "";
  }

  it("does not deny the challan register, and names what it does", async () => {
    const note = await featureNoteText();
    expect(note).not.toMatch(/NO CHALLAN REGISTER/);
    expect(note).toMatch(/CHALLAN REGISTER/);
    // What now exists: recording a deposit with its allocations from a key, and
    // the deductor return profile on that same screen.
    expect(note).toMatch(/Alt\+C/);
    expect(note).toMatch(/DEDUCTOR RETURN PROFILE/);
    // Both refused to Data Entry by default — a decision, stated.
    expect(note).toMatch(/Data Entry/);
  });

  it("still names what does NOT exist, in full", async () => {
    const note = await featureNoteText();
    // No return FILE: no route serves the bytes and the header needs four facts.
    expect(note).toMatch(/NO RETURN FILE IS EMITTED/);
    expect(note).toMatch(/four facts/);
    // Nothing validated by the FVU, which needs a JRE this environment lacks.
    expect(note).toMatch(/FVU/);
    expect(note).toMatch(/Java runtime/);
    // No correction or re-allocation of a recorded challan.
    expect(note).toMatch(/CANNOT BE CORRECTED OR RE-ALLOCATED/);
    // TCS: no seller-liability test, and a credit note reversal is not handled.
    expect(note).toMatch(/seller-liability test/);
    expect(note).toMatch(/credit note/);
  });
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL — the only tests here that prove the KEY works.
//
// ⛔ THESE ARE THE TESTS THAT CATCH THE THIRTEENTH REGISTRATION POINT. Alt+O
// shipped completely dead with the keymap binding, the ScreenActionKind, the
// navMachine case, the button-bar item and the screen's own handler ALL present
// and correct, because AppShell's SCREEN_ACTIONS map had no entry: the shell
// resolved the combo, found no screen action, and forwarded the key to a machine
// that deliberately does nothing. No error, no status line, nothing. Every test
// that missed it called the dispatcher directly (as the suites above do) and
// buttons.test.ts only asserted that the combo RESOLVES.
//
// Each test below carries a CONTROL asserting the form is ABSENT first, so it
// cannot pass on a screen that renders the form from the start.
// ---------------------------------------------------------------------------
describe("a screen-owned key is actually ROUTED by the shell", () => {
  function renderShell() {
    render(
      <CompanyProvider
        client={createMockClient()}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    return screen.getByRole("application");
  }

  /** Gateway -> TDS Challans, with a year asked for so the register is up. */
  async function openRegister() {
    const app = renderShell();
    fireEvent.mouseDown(screen.getByText("TDS Challans"));
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByTestId("tds-challans-year-prompt");
    await askYear();
    return app;
  }

  it("Alt+C opens the deposit form on the challan screen", async () => {
    const app = await openRegister();
    // THE CONTROL: we are on the register and the form is NOT up.
    expect(screen.queryByTestId("tds-challan-form")).toBeNull();
    expect(
      screen.getByRole("button", { name: /Alt\+C Record Challan/ }),
    ).toBeTruthy();

    fireEvent.keyDown(app, { key: "c", altKey: true });

    // If SCREEN_ACTIONS is missing its `recordTDSChallan` entry, THIS is the
    // line that fails — and it is the only kind of test that does. Note what
    // else would still be green: Alt+C resolves, the action has a label, the
    // button is on the bar, and the screen's handler is registered.
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
    // And the picker the form is unusable without is populated from the same
    // response the register came in.
    expect(screen.getByTestId("tds-allocatable:P1:194J:0")).toBeTruthy();
  });

  it("the Alt+C BUTTON does the same thing as the key", async () => {
    await openRegister();
    expect(screen.queryByTestId("tds-challan-form")).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: /Alt\+C Record Challan/ }),
    );
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
  });

  it("F5 does NOT open a Payment voucher over the form", async () => {
    // An unbound key is not inert. Without the screen's own binding, the GLOBAL
    // F5 pushes a Payment voucher on top of a half-keyed counterfoil and the
    // frame that held it is gone. F9 and F8 would do the same with Purchase and
    // Sales.
    const app = await openRegister();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();

    fireEvent.keyDown(app, { key: "F5" });
    expect(screen.queryByText(/Payment Voucher/)).toBeNull();
    // Still on the form, with what was keyed still on screen.
    expect(screen.getByTestId("tds-challan-form")).toBeTruthy();
  });
});
