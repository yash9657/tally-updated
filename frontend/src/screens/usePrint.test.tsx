import { describe, it, expect, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { CompanyProvider } from "../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../app/ScreenActions";
import { usePrintAction, type PdfSink } from "./usePrint";
import { createMockClient } from "../api/mock";
import type { CoredClient } from "../api";
import { ApiError } from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";
import type { PrintVoucherContext } from "./printTarget";

/**
 * Ctrl+P, WIRED. Invariants I-5 (a refusal makes no HTTP call) and the
 * message-fidelity rules of docs/design/t1t4-ui-model.md §1.2.
 *
 * printTarget.test.ts already pins the DECISION. What this suite pins is the
 * EFFECT — that a local refusal never reaches the network, and that the server's
 * own sentence is what an operator reads.
 */

/**
 * Mount usePrintAction and expose a fire() that dispatches the print action the
 * way AppShell does, returning whatever the shell would show in its status line.
 */
function harness(
  clientOver: Partial<CoredClient>,
  snapshot: NavSnapshot,
  voucher?: PrintVoucherContext,
  sink: PdfSink = () => {},
) {
  const base = createMockClient({ seed: false });
  const client = { ...base, ...clientOver } as CoredClient;
  let fire: () => Promise<string | void> = async () => {};

  function Screen() {
    usePrintAction({ voucher, sink });
    const dispatch = useScreenActionDispatcher();
    fire = async () => {
      const res = dispatch("print", snapshot);
      // The shell awaits `pending` so an async refusal still reaches the user.
      if (res.pending) return await res.pending;
      return res.message;
    };
    return null;
  }

  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <ScreenActionsProvider>
        <Screen />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );

  return { fire: () => fire(), client };
}

const VOUCHER_SNAP: NavSnapshot = {
  screen: "voucher",
  voucherType: "sales",
  mode: "create",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

describe("a refusal never touches the network (I-5)", () => {
  it("makes NO request for an unsaved voucher", async () => {
    // A refusal that still hits the network is a 404 in somebody's log — and on
    // the print routes, a 404 that looks exactly like an id probe.
    const printVoucher = vi.fn();
    const { fire } = harness({ printVoucher } as Partial<CoredClient>, VOUCHER_SNAP, {
      voucherID: null,
    });
    const msg = await fire();
    expect(msg).toContain("Ctrl+A");
    expect(printVoucher).not.toHaveBeenCalled();
  });

  it("makes NO request for a class that prints nothing", async () => {
    const printVoucher = vi.fn();
    const { fire } = harness(
      { printVoucher } as Partial<CoredClient>,
      { ...VOUCHER_SNAP, voucherType: "purchase", mode: "alter" },
      { voucherID: "v-1" },
    );
    const msg = await fire();
    expect(msg).toMatch(/supplier/i);
    expect(printVoucher).not.toHaveBeenCalled();
  });

  it("makes NO request on the reconciliation screen (I-10)", async () => {
    const printReport = vi.fn();
    const { fire } = harness({ printReport } as Partial<CoredClient>, {
      screen: "bank-reconciliation",
      screenStackDepth: 2,
      canReturnToVoucher: false,
    });
    const msg = await fire();
    expect(msg).toMatch(/ledger id/i);
    expect(printReport).not.toHaveBeenCalled();
  });
});

describe("printing a document", () => {
  it("fetches the voucher and hands the PDF to the sink", async () => {
    const blob = new Blob(["%PDF-1.4"], { type: "application/pdf" });
    const printVoucher = vi.fn(async () => ({
      blob,
      filename: "Invoice-INV-0042.pdf",
    }));
    const sink = vi.fn();
    const { fire } = harness(
      { printVoucher } as Partial<CoredClient>,
      { ...VOUCHER_SNAP, mode: "alter" },
      { voucherID: "v-42" },
      sink,
    );
    const msg = await fire();
    await waitFor(() => expect(sink).toHaveBeenCalled());
    expect(printVoucher).toHaveBeenCalledWith("co-1", "sales", "v-42");
    // The server's filename is honoured — it names the document.
    expect(sink).toHaveBeenCalledWith(blob, "Invoice-INV-0042.pdf");
    expect(msg).toContain("Invoice-INV-0042.pdf");
  });

  it("prints a voucher JUST SAVED in create mode", async () => {
    // §2.1 — the handoff's actual complaint: "an operator cannot print the
    // invoice from the screen they keyed it on."
    const printVoucher = vi.fn(async () => ({ blob: new Blob([]), filename: null }));
    const { fire } = harness({ printVoucher } as Partial<CoredClient>, VOUCHER_SNAP, {
      voucherID: "fresh-1",
      justSaved: true,
    });
    await fire();
    expect(printVoucher).toHaveBeenCalledWith("co-1", "sales", "fresh-1");
  });

  it("fetches a report by its server slug", async () => {
    const printReport = vi.fn(async () => ({ blob: new Blob([]), filename: null }));
    const { fire } = harness({ printReport } as Partial<CoredClient>, {
      screen: "report",
      reportName: "Profit & Loss A/c",
      screenStackDepth: 2,
      canReturnToVoucher: false,
    });
    await fire();
    expect(printReport).toHaveBeenCalledWith("co-1", "profit-and-loss");
  });
});

describe("the server's message reaches the operator unaltered", () => {
  it("passes a class-mismatch 404 through WITHOUT explaining it", async () => {
    // The print handler answers 404 with a deliberately class-neutral body: a 403
    // there would confirm that a voucher with that id exists in this company,
    // which is the fact the refusal protects. A client that rewrote this to
    // "wrong voucher class" would leak precisely that.
    const body = "no printable voucher with that id in this company";
    const { fire } = harness(
      {
        printVoucher: async () => {
          throw new ApiError(404, body);
        },
      } as Partial<CoredClient>,
      { ...VOUCHER_SNAP, mode: "alter" },
      { voucherID: "v-1" },
    );
    const msg = await fire();
    expect(msg).toContain(body);
    expect(msg).not.toMatch(/class/i);
  });

  it("passes a 501 through for a report that does not print", async () => {
    const { fire } = harness(
      {
        printReport: async () => {
          throw new ApiError(501, "printing this report is not implemented");
        },
      } as Partial<CoredClient>,
      {
        screen: "report",
        reportName: "Day Book",
        screenStackDepth: 2,
        canReturnToVoucher: false,
      },
    );
    expect(await fire()).toContain("not implemented");
  });

  it("passes an ACL 403 through, naming nothing of its own", async () => {
    // The seeded Data Entry level is allowed ZERO `print report:NAME` rules
    // (handoff §5 item 7), so every report print is refused for it. This change
    // does not alter the ACL — it stops the refusal being invisible.
    const { fire } = harness(
      {
        printReport: async () => {
          throw new ApiError(403, "forbidden: print report:day-book");
        },
      } as Partial<CoredClient>,
      {
        screen: "report",
        reportName: "Day Book",
        screenStackDepth: 2,
        canReturnToVoucher: false,
      },
    );
    const msg = await fire();
    expect(msg).toContain("print report:day-book");
  });

  it("does not swallow a transport failure", async () => {
    // Printing is a live GET; the offline plane is sync-only. It must fail with a
    // stated error rather than a silent nothing.
    const { fire } = harness(
      {
        printReport: async () => {
          throw new Error("Failed to fetch");
        },
      } as Partial<CoredClient>,
      {
        screen: "report",
        reportName: "Trial Balance",
        screenStackDepth: 2,
        canReturnToVoucher: false,
      },
    );
    expect(await fire()).toContain("Failed to fetch");
  });
});
