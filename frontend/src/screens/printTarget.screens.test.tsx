import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/react";

import { CompanyProvider } from "../app/CompanyContext";
import { LoadedVoucherProvider } from "../app/LoadedVoucher";
import { LoadedDraftProvider } from "../app/LoadedDraft";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
} from "../app/ScreenActions";
import { createDefaultRegistry } from "../app/defaultScreens";
import { buttonBarFor } from "../keystroke/buttonBar";
import { REPORT_NAMES } from "../keystroke/types";
import type { ReportName, VoucherType } from "../keystroke/types";
import type { NavSnapshot } from "../keystroke/navMachine";
import { createMockClient } from "../api";
import type { CoredClient } from "../api";

/**
 * ⛔⛔ THE CLASS, NOT THE CALL SITE: A BAR THAT ADVERTISES A KEY THE SCREEN
 * NEVER REGISTERED.
 *
 * Three defects in the printing register were ONE defect counted three times —
 * the Delivery Note (and the other three stock screens), the TDS/TCS Register,
 * and the drill-down levels. In each case the button bar over the screen carried
 * "Ctrl+P Print", the resolver in printTarget.ts had a real answer for that
 * frame, and the screen COMPONENT registered no `print` handler — so the key
 * fell through to the shell's generic "Ctrl+P — Print does not apply on this
 * screen", made no request at all, and taught the operator that the strip lies.
 *
 * ⛔ WHY THE EXISTING BUTTON-FIDELITY SUITE COULD NOT SEE ANY OF THEM.
 * keystroke/buttons.test.ts walks the bar and proves each key resolves to a
 * KEYMAP action that is not `unavailable` — and `print` sits on its list of
 * "screen-owned side effects", which it must, because printing deliberately does
 * not move the navigation stack. That list is where the three defects hid: an
 * action is called a side effect on the strength of its KIND, and whether any
 * mounted component actually owns it is a question the pure machine cannot ask.
 * This file asks it, by MOUNTING the component the registry resolves for the
 * frame and firing the action through the same bridge the shell fires it
 * through.
 *
 * It is a WALK, not a list, for the reason RULE 2 of the burndown gives: each
 * earlier round of this fix asserted a list of known-bad screens and the next
 * one was off-list. A report registered tomorrow is swept the day it is
 * registered.
 *
 * ⚠️ THE FULL CENSUS WAS RUN — every registered frame was mounted and every
 * screen-owned action fired against it — and it found exactly three holes of this
 * class in the product. TWO ARE NOT THIS LANE'S FILES AND ARE REPORTED, NOT
 * SWEPT, because a test that asserts a defect it may not fix is a red gate that
 * teaches the next reader to ignore red gates:
 *
 *  1. ⛔ `app/defaultScreens.tsx` maps the bare `report` key to
 *     `UnwiredReportScreen` — every report name a menu or the Go To picker can
 *     produce that has no data plane (Cash Flow, GSTR-1, the anomaly list, and
 *     `Alt+G > Vouchers`, which lands there by accident). It registers NOTHING:
 *     REPORT_BAR sells both Ctrl+P and Alt+E over it and neither moves the
 *     machine, so both answer "does not apply on this screen" — while printTarget
 *     already holds the right sentence ("…cannot be printed in this build — it is
 *     a display-only report. The server answers 501 for it rather than printing a
 *     blank page."). The fix is one `usePrintAction()` call in that component,
 *     plus an export refusal.
 *  2. ⛔ VOUCHER_BAR sells "Alt+D Delete" over the four STOCK voucher screens,
 *     which register no `delete` and do not move the machine. The omission is
 *     deliberate and stated in StockVoucherScreen ("wiring them here without a
 *     tested path would be a button that looks live") — but the BAR was never
 *     told, so the key is sold and dead. Fixing it means either a delete path or
 *     a class-scoped bar in `keystroke/buttonBar.ts`.
 *  3. ✅ CLOSED HERE: BANK_RECON_BAR sold "Ctrl+A Accept" over a screen that
 *     registered no `accept` and a frame the machine does not move on. See
 *     BankReconciliationScreen.test.tsx — and note the answer is NOT a save,
 *     because every bank date on that screen is written as it is typed.
 */

afterEach(cleanup);

beforeEach(() => {
  // usePrintAction's default sink calls window.open + createObjectURL; jsdom
  // implements neither usefully, and a screen under sweep may resolve to a real
  // route. Same stubbing StockVoucherScreen.test.tsx does.
  vi.stubGlobal("open", vi.fn(() => ({}) as Window));
  if (!URL.createObjectURL) {
    Object.defineProperty(URL, "createObjectURL", {
      value: vi.fn(() => "blob:x"),
      writable: true,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      value: vi.fn(),
      writable: true,
    });
  } else {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  }
});

/**
 * Mount the component the REGISTRY resolves for a frame, beside a probe that
 * fires screen actions through the shell's own dispatcher.
 *
 * The registry rather than a direct import is the point: what an operator's key
 * reaches is whatever `resolve()` hands the router, so importing the component
 * this file BELIEVES is there would only prove something about this file. A
 * report name registered against the wrong component passes that check and fails
 * in the product.
 */
function mountFrame(
  snapshot: NavSnapshot,
  client: CoredClient,
): { fire: (kind: ScreenActionKind) => boolean } {
  const Screen = createDefaultRegistry().resolve(snapshot);
  expect(
    Screen,
    `no screen is registered for ${JSON.stringify(snapshot)}`,
  ).toBeTruthy();

  const probe: { fire: (kind: ScreenActionKind) => boolean } = {
    fire: () => false,
  };
  function Probe() {
    const dispatch = useScreenActionDispatcher();
    probe.fire = (kind) => dispatch(kind, snapshot).ran;
    return null;
  }

  const Resolved = Screen!;
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "27" }}
    >
      <ScreenActionsProvider>
        <LoadedVoucherProvider>
          <LoadedDraftProvider>
            <Resolved snapshot={snapshot} onSelect={() => {}} />
            <Probe />
          </LoadedDraftProvider>
        </LoadedVoucherProvider>
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  return probe;
}

/** A frame whose button bar sells "Ctrl+P Print". */
interface Door {
  readonly what: string;
  readonly snapshot: NavSnapshot;
}

const PERIOD = { periodFrom: "2026-04-01", periodTo: "2026-06-30" };

const REPORT_DOORS: Door[] = REPORT_NAMES.map((name: ReportName) => ({
  what: `the ${name} report`,
  snapshot: {
    screen: "report",
    reportName: name,
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: PERIOD,
  } as NavSnapshot,
}));

const DRILL_DOORS: Door[] = [
  {
    what: "a GROUP drill-down (Enter on a Trial Balance line)",
    snapshot: {
      screen: "report-detail",
      parentReportName: "Trial Balance",
      reportName: "Trial Balance",
      detailOf: "group:Sundry Debtors",
      screenStackDepth: 3,
      canReturnToVoucher: false,
    } as NavSnapshot,
  },
  {
    what: "a LEDGER drill-down — the level Ctrl+P prints the PARENT of",
    snapshot: {
      screen: "report-detail",
      parentReportName: "Day Book",
      reportName: "Day Book",
      detailOf: "ledger:Nandi Steels",
      screenStackDepth: 3,
      canReturnToVoucher: false,
    } as NavSnapshot,
  },
];

/**
 * EVERY voucher class, and the exhaustiveness is enforced by tsc rather than by
 * a reader counting: the Record below is `Record<VoucherType, true>`, so a class
 * added to the union stops this file compiling until it is swept. An array typed
 * `readonly VoucherType[]` would have accepted eleven of the twelve silently —
 * which is how four stock screens came to be missing the key while every suite
 * in the repo was green.
 */
const ALL_VOUCHER_TYPES: Readonly<Record<VoucherType, true>> = {
  contra: true,
  payment: true,
  receipt: true,
  journal: true,
  sales: true,
  purchase: true,
  "credit-note": true,
  "debit-note": true,
  "delivery-note": true,
  "receipt-note": true,
  "stock-journal": true,
  "physical-stock": true,
};

const VOUCHER_DOORS: Door[] = (
  Object.keys(ALL_VOUCHER_TYPES) as VoucherType[]
).map((voucherType) => ({
  what: `a ${voucherType} voucher`,
  snapshot: {
    screen: "voucher",
    voucherType,
    screenStackDepth: 2,
    canReturnToVoucher: false,
  } as NavSnapshot,
}));

const BANK_DOOR: Door = {
  what: "the Bank Reconciliation Statement",
  snapshot: {
    screen: "bank-reconciliation",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: PERIOD,
  } as NavSnapshot,
};

const DOORS: Door[] = [
  ...REPORT_DOORS,
  ...DRILL_DOORS,
  ...VOUCHER_DOORS,
  BANK_DOOR,
];

describe("every screen whose bar sells Ctrl+P owns the key", () => {
  // The premise, asserted rather than assumed: if a bar stopped advertising the
  // key this whole suite would become vacuous, and a vacuous suite is a green
  // gate that checks nothing.
  it("the four frame kinds under sweep really do advertise Ctrl+P", () => {
    for (const screen of [
      "voucher",
      "report",
      "report-detail",
      "bank-reconciliation",
    ] as const) {
      expect(
        buttonBarFor(screen).some((b) => b.key === "Ctrl+P"),
        `${screen}'s bar no longer sells Ctrl+P — this suite is now vacuous`,
      ).toBe(true);
    }
  });

  for (const door of DOORS) {
    it(`${door.what} answers Ctrl+P itself`, async () => {
      const probe = mountFrame(
        door.snapshot,
        createMockClient({ seed: true }) as CoredClient,
      );
      // Several of these screens register only after their first fetch resolves.
      await waitFor(() => {
        expect(
          probe.fire("print"),
          `${door.what} advertises "Ctrl+P Print" and registers no print handler, so the ` +
            `key falls through to the shell's "does not apply on this screen" and makes NO ` +
            `request — while printTarget.ts already holds the answer for this frame`,
        ).toBe(true);
      });
    });
  }
});

/**
 * THE SECOND HALF OF THE CLASS: a key that IS registered and ignores the
 * screen's own state.
 *
 * A live Ctrl+P that prints the whole book under a screen headed "1-Apr-2026 to
 * 30-Jun-2026" is not better than a dead one — it is worse, because the paper
 * looks right and somebody signs it. The RESOLUTION is unit-tested in
 * printTarget.test.ts; what is asserted here is that the frame's period survives
 * the trip through the MOUNTED screen and reaches the client, which is the half
 * a pure test cannot see.
 */
describe("the request carries the period the screen is showing", () => {
  function printReportSpy() {
    return vi.fn(async () => ({
      blob: new Blob(["%PDF-1.4"], { type: "application/pdf" }),
      filename: "report.pdf",
    }));
  }

  it("the Day Book prints the F2 quarter, not the whole book", async () => {
    const printReport = printReportSpy();
    const client = {
      ...createMockClient({ seed: true }),
      printReport,
    } as unknown as CoredClient;
    const snapshot = REPORT_DOORS.find((d) =>
      d.what.includes("Day Book"),
    )!.snapshot;

    const probe = mountFrame(snapshot, client);
    await waitFor(() => {
      expect(probe.fire("print")).toBe(true);
      expect(printReport).toHaveBeenCalled();
    });
    expect(printReport).toHaveBeenCalledWith("co-1", "day-book", {
      from: "2026-04-01",
      to: "2026-06-30",
    });
  });

  it("a POSITION statement sends only the upper bound, and sends it as as_on", async () => {
    const printReport = printReportSpy();
    const client = {
      ...createMockClient({ seed: true }),
      printReport,
    } as unknown as CoredClient;
    const snapshot = REPORT_DOORS.find((d) =>
      d.what.includes("Trial Balance"),
    )!.snapshot;

    const probe = mountFrame(snapshot, client);
    await waitFor(() => {
      expect(probe.fire("print")).toBe(true);
      expect(printReport).toHaveBeenCalled();
    });
    // ⛔ NO `from`. Bounding a POSITION below drops the entity's earlier history
    // and the statement still foots — a wrong answer that looks right.
    expect(printReport).toHaveBeenCalledWith("co-1", "trial-balance", {
      as_on: "2026-06-30",
    });
  });
});
