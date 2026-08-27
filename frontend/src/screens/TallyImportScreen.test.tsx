import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createDefaultRegistry } from "../app/defaultScreens";
import { createMockClient, ApiError } from "../api";
import type { CoredClient, ImportTallyXMLResponse } from "../api";

afterEach(cleanup);

const COMPANY = "company-demo";

/**
 * THE FIXTURES ARE READ FROM `corpora/` WITH `node:fs`, NOT IMPORTED.
 *
 * They live outside `frontend/`, so a static import would need a vite `fs.allow`
 * entry; `src/api/contract/corpus.ts` reads its corpus the same way and for the
 * same reason.
 *
 * ⛔ AND THE BYTES ARE READ AS BYTES. `readFileSync(path)` with no encoding
 * returns a Buffer. Passing "utf8" here would corrupt the UTF-16 fixture inside
 * the TEST, which would make the test agree with the bug it exists to catch —
 * the oracle would then be unable to see the defect (HANDOFF.md §1.16).
 */
function fixtureBytes(name: string): Uint8Array<ArrayBuffer> {
  const here = dirname(fileURLToPath(import.meta.url));
  const buf = readFileSync(join(here, "..", "..", "..", "corpora", "migration", name));
  // ⚠️ COPIED INTO A Uint8Array OVER AN EXPLICIT ArrayBuffer, not handed over as
  // the node Buffer. Neither a Buffer nor a `Uint8Array<ArrayBufferLike>` is
  // assignable to `BlobPart` under this tsconfig (ArrayBufferLike admits
  // SharedArrayBuffer, which a Blob cannot take), and `tsc` is a SEPARATE gate
  // from `make verify` — this file was green in vitest and red in tsc.
  const out = new Uint8Array(new ArrayBuffer(buf.length));
  out.set(buf);
  return out;
}

/**
 * The single captured payload, or a loud failure.
 *
 * ⛔ NOT `sent[0]!`. Under `noUncheckedIndexedAccess` the assertion would
 * silence a real case: a screen that never called the client at all would give
 * `undefined.xml` and a TypeError from deep inside wireBytes, which reads as a
 * broken oracle rather than as "the import never fired".
 */
function onlyPayload(sent: { xml: unknown }[]): unknown {
  if (sent.length !== 1) {
    throw new Error(`expected exactly one import call, saw ${sent.length}`);
  }
  const first = sent[0];
  if (!first) throw new Error("the capture array holds no call");
  return first.xml;
}

/** Byte equality, stated once. */
function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/** The first two bytes, as plain numbers — a UTF-16LE BOM is FF FE. */
function bom(a: Uint8Array): number[] {
  return Array.from(a.slice(0, 2));
}

/**
 * ⛔ THE UTF-16LE FIXTURE IS THE ONLY ONE THAT CAN SEE THIS TRACK'S DEFECT.
 *
 * `file -b corpora/migration/06-tallyprime-real-shapes.xml` reports "Unicode
 * text, UTF-16, little-endian, with CRLF" — which is what EVERY genuine
 * TallyPrime export is. `01-opening-trading.xml` is ASCII, like everything that
 * was hand-authored before a real file arrived, and it round-trips through
 * `File.text()` unharmed. So a probe built on 01 passes whether or not the
 * encoding is handled, and the pair below is what makes the fixture choice
 * load-bearing rather than incidental.
 */
const REAL_FIXTURE = "06-tallyprime-real-shapes.xml";
const ASCII_FIXTURE = "01-opening-trading.xml";

/**
 * Read a Blob's bytes back out.
 *
 * ⚠️ VIA `FileReader`, BECAUSE jsdom's `Blob` HAS NO `arrayBuffer()`. That is
 * worth a line rather than a workaround note: the missing method is why it is
 * tempting to assert something cheaper here — `typeof body !== "string"`, or the
 * blob's `size` — and neither of those can see a transcoding that preserved the
 * length. The bytes are the property; this is just the only way to get at them
 * in this environment.
 */
function blobBytes(b: Blob): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onerror = () => reject(fr.error ?? new Error("FileReader failed"));
    fr.onload = () => resolve(new Uint8Array(fr.result as ArrayBuffer));
    fr.readAsArrayBuffer(b);
  });
}

/**
 * ⛔⛔ THE BYTES THAT WOULD GO ON THE WIRE, FOR EITHER SHAPE OF PAYLOAD. THIS
 * FUNCTION IS WHAT MAKES THE ASCII FIXTURE A REAL CONTROL.
 *
 * `postRaw` takes `BodyInit`. A `Blob` travels as its own bytes; a `string`
 * is encoded by `fetch` as UTF-8. So modelling BOTH branches is not leniency —
 * it is the only way to state the property once and have it be TRUE for ASCII
 * and FALSE for UTF-16 under the `await file.text()` bug. Assert `instanceof
 * Blob` in both fixtures instead and both go red under that mutation, which
 * proves nothing about why the fixture choice matters: it would look exactly the
 * same if the ASCII file were equally destroyed, and it is not.
 */
async function wireBytes(payload: unknown): Promise<Uint8Array> {
  if (payload instanceof Blob) return blobBytes(payload);
  if (typeof payload === "string") return new TextEncoder().encode(payload);
  throw new Error(`the screen sent neither a Blob nor a string: ${typeof payload}`);
}

/** The event a real Mac sends for Option+O: the COMPOSED key plus the code. */
function altMacO(target: Element): void {
  // ⚠️ macOS COMPOSES Option+O INTO "ø" — normalize.test.ts:34 already pins that
  // exact mapping for this exact combo. A test firing { key: "o", altKey: true }
  // proves the binding works on the machines nobody develops on, which is how
  // every Alt binding in this product was once found dead.
  fireEvent.keyDown(target, { key: "ø", code: "KeyO", altKey: true });
}

const EMPTY_RESULT: ImportTallyXMLResponse = {
  source_company: "Grain Traders",
  vouchers_posted: 0,
  vouchers_altered: 0,
  vouchers_deleted: 0,
  vouchers_skipped: 0,
  groups_ensured: [],
  ledgers_ensured: [],
  warnings: [],
  posted: [],
  stock_lines: 0,
  opening_positions: 0,
  stock_items_ensured: [],
  units_ensured: [],
  godowns_ensured: [],
  batches_ensured: [],
};

/**
 * A stub client that CAPTURES what the screen handed it and answers with a
 * canned result — the pattern DataScreen.test.tsx uses.
 *
 * ⛔ THE MOCK IS NOT TAUGHT TO PARSE TALLY XML. `createMockClient()`'s own
 * `importTallyXML` returns honest zeroes and says in a comment that it does not
 * parse XML at all; a second XML parser in TypeScript would be unvalidated bytes
 * a test could then pin. So a test that needs a populated report injects one.
 */
function capturingClient(
  result: Partial<ImportTallyXMLResponse> | Error = {},
): { client: CoredClient; sent: { xml: unknown }[] } {
  const sent: { xml: unknown }[] = [];
  const base = createMockClient();
  const client: CoredClient = {
    ...base,
    async importTallyXML(_companyID: string, xml: string | Blob) {
      sent.push({ xml });
      if (result instanceof Error) throw result;
      return { ...EMPTY_RESULT, ...result };
    },
  };
  return { client, sent };
}

function renderShell(client: CoredClient = createMockClient()) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell registry={createDefaultRegistry()} />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/** Open the import screen with Tally's own key and hand it a file. */
function pick(file: File): void {
  const input = screen.getByTestId("tally-import-file") as HTMLInputElement;
  fireEvent.change(input, { target: { files: [file] } });
}

// ===========================================================================
// SLICE 0 — THE PROPERTY THIS WHOLE TRACK TURNS ON
// ===========================================================================

/**
 * ⛔⛔ THE PROPERTY IS "THE BYTES THAT LEFT THE BROWSER ARE THE BYTES ON DISK",
 * NOT "THE IMPORT RETURNED 200".
 *
 * `CoredClient.importTallyXML` used to declare its body as `xml: string`, and the
 * obvious screen code for a picked file is `await file.text()`. That decodes as
 * UTF-8 unconditionally. Handed UTF-16LE bytes it yields replacement characters
 * interleaved with NULs, `fetch` re-encodes THAT as UTF-8, and what arrives at
 * the server has no recognisable BOM — so `decodeTallyBytes` never fires and the
 * parse fails on the only file shape a real customer will ever send.
 *
 * A test asserting "the import succeeded" cannot see that, because the stub
 * client succeeds either way. This one reads the bytes back out.
 */
describe("the export leaves the browser as BYTES, byte-identical to the file", () => {
  it("sends the UTF-16LE file unchanged — BOM intact, every byte equal", async () => {
    const bytes = fixtureBytes(REAL_FIXTURE);
    // THE CONTROL ON THE FIXTURE ITSELF. If this ever fails, the corpus file was
    // re-saved as UTF-8 and every assertion below became vacuous — the fixture
    // would round-trip through .text() unharmed and the probe would pass against
    // the broken build.
    expect(bom(bytes)).toEqual([0xff, 0xfe]);

    const { client, sent } = capturingClient();
    const app = renderShell(client);
    altMacO(app);
    pick(new File([bytes], REAL_FIXTURE, { type: "text/xml" }));
    fireEvent.click(screen.getByTestId("tally-import-button"));

    await waitFor(() => expect(sent).toHaveLength(1));
    // ⛔ WHAT WOULD REACH THE SERVER, not what the screen happens to hold. Under
    // `await file.text()` this is UTF-8-encoded mojibake: the BOM is gone, so
    // decodeTallyBytes never fires and the parse fails on the only file shape a
    // real customer sends.
    const out = await wireBytes(onlyPayload(sent));
    expect(bom(out)).toEqual([0xff, 0xfe]);
    expect(out.length).toBe(bytes.length);
    // Byte-for-byte. A length check alone would pass a transcoding that happened
    // to preserve the size, and a BOM check alone would pass a file whose BOM
    // survived while its body did not.
    expect(bytesEqual(out, bytes)).toBe(true);
  });

  /**
   * ⛔ THE OTHER HALF OF THE MUTATION PROOF, AND IT IS NOT DECORATION.
   *
   * This is the same assertion on the ASCII fixture. Under the `await
   * file.text()` mutation the UTF-16 test above goes RED and THIS ONE STAYS
   * GREEN — which is precisely what proves the fixture choice is load-bearing.
   * Had the suite only ever driven 01-05, it would have been green against a
   * screen that destroys every real export.
   */
  it("also sends an ASCII fixture unchanged — the half that stays GREEN under the bug", async () => {
    const bytes = fixtureBytes(ASCII_FIXTURE);
    // The control, inverted: this fixture must NOT have a UTF-16 BOM, or it is
    // not the ASCII half of the pair any more.
    expect(bom(bytes)).not.toEqual([0xff, 0xfe]);

    const { client, sent } = capturingClient();
    const app = renderShell(client);
    altMacO(app);
    pick(new File([bytes], ASCII_FIXTURE, { type: "text/xml" }));
    fireEvent.click(screen.getByTestId("tally-import-button"));

    await waitFor(() => expect(sent).toHaveLength(1));
    // The SAME property as the test above, on a file whose encoding survives the
    // bug. This is the half that must STAY GREEN under `await file.text()`.
    const out = await wireBytes(onlyPayload(sent));
    expect(bytesEqual(out, bytes)).toBe(true);
  });

  /**
   * THE MECHANISM, stated separately from the property.
   *
   * The property above is what matters and it is encoding-aware. This one pins
   * HOW it is achieved — the `File` object itself goes to `postRaw`, so the
   * server's own sniffer does the decoding, which is the side that has been
   * tested against real exports. It is a separate test because it fails for a
   * different reason than a corrupted byte, and a reader who sees only this one
   * go red should not conclude the bytes were wrong.
   */
  it("hands postRaw the FILE, never a decoded string", async () => {
    const { client, sent } = capturingClient();
    const app = renderShell(client);
    altMacO(app);
    pick(new File([fixtureBytes(REAL_FIXTURE)], REAL_FIXTURE));
    fireEvent.click(screen.getByTestId("tally-import-button"));
    await waitFor(() => expect(sent).toHaveLength(1));
    const payload = onlyPayload(sent);
    expect(payload instanceof Blob).toBe(true);
    expect(typeof payload).not.toBe("string");
  });

  it("never reads the file itself — no client-side parse, no pre-validation", async () => {
    // The screen must not decide anything about the XML. The cheap proof that it
    // does not is that a file which is not XML at all still reaches the server:
    // the encoding sniffing, the envelope nesting and the GUID routing are all
    // server-side and hard-won, and a client-side "fail fast" would be a second
    // implementation that disagrees.
    const { client, sent } = capturingClient();
    const app = renderShell(client);
    altMacO(app);
    pick(new File([new TextEncoder().encode("this is not xml")], "notes.xml"));
    fireEvent.click(screen.getByTestId("tally-import-button"));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(screen.queryByTestId("tally-import-error")).toBeNull();
  });
});

// ===========================================================================
// THE DOORS — three registration points ship the key completely dead
// ===========================================================================

describe("Alt+O opens the MIGRATE-FROM-TALLY screen", () => {
  it("is ABSENT before the key — the control that makes the next test mean something", () => {
    renderShell();
    expect(screen.queryByTestId("tally-import-screen")).toBeNull();
  });

  it("opens on the macOS-composed Option+O, not only on a Linux Alt+o", () => {
    const app = renderShell();
    altMacO(app);
    expect(screen.getByTestId("tally-import-screen")).toBeTruthy();
  });

  it("titles the frame 'Import from Tally' and not the default 'Tally'", () => {
    const app = renderShell();
    altMacO(app);
    // Without AppShell's screenTitle case the chrome reads "Tally" over a screen
    // that is about to post a whole book into the open company.
    expect(screen.getAllByText("Import from Tally").length).toBeGreaterThan(0);
  });

  it("does not stack a second copy when Alt+O is pressed twice", () => {
    const app = renderShell();
    altMacO(app);
    altMacO(app);
    expect(screen.getAllByTestId("tally-import-screen")).toHaveLength(1);
  });

  it("is reachable from the GATEWAY, which is Tally's own path", async () => {
    // ⛔ Alt+G Go To IS NOT A DOOR and cannot be made one: that picker is
    // generated from ["Vouchers", ...REPORT_NAMES] and this is not a report
    // name. Tally's documented path is Gateway of Tally > Import
    // (RESEARCH.md:1221), so the Gateway entry is the second real door.
    const app = renderShell();
    fireEvent.mouseDown(screen.getByText("Import from Tally"));
    fireEvent.keyDown(app, { key: "Enter" });
    expect(await screen.findByTestId("tally-import-screen")).toBeTruthy();
  });
});

/**
 * ⛔⛔ THE ONE THING THIS TRACK COULD HAVE BROKEN, AND THE ONLY TEST THAT SEES IT.
 *
 * Alt+O was ALREADY BOUND — to `importBankStatement`, in RAW_SCREEN_BINDINGS for
 * `bank-reconciliation`. Adding a GLOBAL Alt+O is safe only because
 * `resolveAction` consults `screenBindings[screen]` BEFORE the global table. That
 * is a claim about precedence, and precedence is exactly the kind of thing that
 * is true when written and untrue three refactors later — at which point Alt+O
 * on the reconciliation screen would open a company-wide XML importer over a
 * bank statement, both of which are called "import".
 */
describe("the global Alt+O does NOT shadow the bank-statement import", () => {
  it("Alt+O on the reconciliation screen still opens the STATEMENT panel", async () => {
    const app = renderShell();
    fireEvent.mouseDown(screen.getByText("Bank Reconciliation"));
    fireEvent.keyDown(app, { key: "Enter" });
    // Await the account load: before it resolves, Alt+O correctly answers
    // "select a bank account first" and we would be testing the refusal.
    await screen.findByText(/Bank Reconciliation — HDFC Bank/);

    altMacO(app);

    expect(screen.getByTestId("stmt-import-panel")).toBeTruthy();
    // AND the screen-scoped meaning WON: no Tally importer opened on top of it.
    expect(screen.queryByTestId("tally-import-screen")).toBeNull();
  });
});

// ===========================================================================
// THE REPORT — vouchers_posted alone is the failure mode this screen prevents
// ===========================================================================

describe("the report shows THE GOODS beside the accounting", () => {
  async function importWith(
    result: Partial<ImportTallyXMLResponse>,
  ): Promise<void> {
    const { client } = capturingClient(result);
    const app = renderShell(client);
    altMacO(app);
    pick(new File([fixtureBytes(REAL_FIXTURE)], REAL_FIXTURE));
    fireEvent.click(screen.getByTestId("tally-import-button"));
    await screen.findByTestId("tally-import-report");
  }

  it("renders stock_lines and opening_positions, not only the voucher count", async () => {
    await importWith({
      vouchers_posted: 121,
      stock_lines: 340,
      opening_positions: 12,
    });
    expect(screen.getByTestId("tally-import-posted").textContent).toContain("121");
    // ⛔ THESE TWO ARE THE ONLY FIGURES ON THE WIRE THAT DISTINGUISH "imported
    // the accounting" from "imported the trade". Quantity is invisible in a
    // trial balance, so an import that read every stock line and dropped it
    // answers 200 with books that balance and an empty Stock Summary.
    expect(screen.getByTestId("tally-import-stock-lines").textContent).toContain(
      "340",
    );
    expect(
      screen.getByTestId("tally-import-opening-positions").textContent,
    ).toContain("12");
  });

  it("calls out ZERO stock movements as the tell, not as a clean result", async () => {
    await importWith({ vouchers_posted: 121, stock_lines: 0 });
    const warn = screen.getByTestId("tally-import-no-stock").textContent ?? "";
    expect(warn).toMatch(/no stock movements/i);
    expect(warn).toMatch(/stock summary/i);
  });

  it("treats zero opening positions ALONGSIDE real movement as a WARNING", async () => {
    await importWith({ stock_lines: 340, opening_positions: 0 });
    const warn =
      screen.getByTestId("tally-import-opening-gap").textContent ?? "";
    expect(warn).toMatch(/no opening positions/i);
    expect(warn).toMatch(/warning, not a clean result/i);
    // And the zero-movement warning must NOT also be up: stock DID move here.
    expect(screen.queryByTestId("tally-import-no-stock")).toBeNull();
  });

  it("does not warn about opening positions when there was no stock at all", async () => {
    // A pure-accounting export (no inventory anywhere) is not an opening-stock
    // gap, and saying so would train an operator to ignore the warning.
    await importWith({ stock_lines: 0, opening_positions: 0 });
    expect(screen.queryByTestId("tally-import-opening-gap")).toBeNull();
  });

  it("renders every warning IN FULL, never a count", async () => {
    const warnings = [
      "opening bill-wise breakup for Sundry Debtors does not sum to its balance",
      'ledger "Freight Inward" has no GROUP in the file; placed under Indirect Expenses',
    ];
    await importWith({ vouchers_posted: 121, warnings });
    const list = screen.getByTestId("tally-import-warnings").textContent ?? "";
    // ⛔ The importer carries on past what it cannot represent exactly. An
    // operator told only "121 vouchers posted" will believe the migration was
    // lossless when parts of it were not.
    for (const w of warnings) expect(list).toContain(w);
  });

  it("says what a CLEAN-looking report still does not prove", async () => {
    await importWith({ vouchers_posted: 121, stock_lines: 5, warnings: [] });
    const caveat = screen.getByTestId("tally-import-caveat").textContent ?? "";
    expect(caveat).toMatch(/empty warning list/i);
    expect(caveat).toMatch(/balanced trial balance/i);
    expect(caveat).toMatch(/stock summary/i);
  });

  it("lists the masters it CREATED, by name", async () => {
    await importWith({
      stock_items_ensured: ["WHEAT", "RICE"],
      units_ensured: ["BAG"],
    });
    expect(
      screen.getByTestId("tally-import-stock-items").textContent,
    ).toContain("WHEAT");
    expect(screen.getByTestId("tally-import-units").textContent).toContain("BAG");
  });
});

// ===========================================================================
// THE REFUSALS — each is a real answer, not an error state
// ===========================================================================

describe("the SERVER'S sentence is what an operator reads", () => {
  async function refuseWith(e: Error): Promise<string> {
    const { client } = capturingClient(e);
    const app = renderShell(client);
    altMacO(app);
    pick(new File([fixtureBytes(REAL_FIXTURE)], REAL_FIXTURE));
    fireEvent.click(screen.getByTestId("tally-import-button"));
    const el = await screen.findByTestId("tally-import-error");
    return el.textContent ?? "";
  }

  it("renders the whole-import abort verbatim, naming both ways forward", async () => {
    // The one failure a real migration meets FIRST: an export with no STOCKITEM
    // masters carries no opening stock, so the first sale of goods bought before
    // the export window has nothing to issue and the WHOLE import is refused.
    const sentence =
      "the export declares no stock items, so it carries no opening stock: " +
      'voucher SAL/1 issues 100.00 BAG of "WHEAT" and there is nothing to issue. ' +
      "Export the Masters from Tally as well, or allow negative stock on this company first.";
    expect(await refuseWith(new ApiError(422, sentence))).toContain(sentence);
  });

  it("renders a 403 as the DECISION it is", async () => {
    // No seeded level names the `import` resource and Data Entry's default
    // access is 'none', so the route is default-DENIED for every seeded
    // non-Owner level. That is not a bug to be smoothed over.
    const sentence = "this security level is not allowed to create import";
    expect(await refuseWith(new ApiError(403, sentence))).toContain(sentence);
  });

  it("renders the back-dated cutoff refusal", async () => {
    // An import is a WRITE, so it inherits the cutoff. A file containing a
    // voucher before a closed period is refused, and which voucher matters.
    const sentence =
      "voucher dated 2025-03-31 is before this level's back-dated entry cutoff of 2026-04-01";
    expect(await refuseWith(new ApiError(422, sentence))).toContain(sentence);
  });

  it("renders a 413 rather than substituting 'import failed'", async () => {
    const sentence = "request body exceeds the 128 MiB limit for this route";
    expect(await refuseWith(new ApiError(413, sentence))).toContain(sentence);
  });

  it("lets the operator re-pick the SAME file after a refusal", async () => {
    // After a refusal, re-picking the file the server just refused is the most
    // natural thing an operator does. The input clears itself so the change
    // event fires again — DataScreen's restore input does the same.
    const { client, sent } = capturingClient(new ApiError(403, "not allowed"));
    const app = renderShell(client);
    altMacO(app);
    const file = new File([fixtureBytes(REAL_FIXTURE)], REAL_FIXTURE);
    pick(file);
    fireEvent.click(screen.getByTestId("tally-import-button"));
    await screen.findByTestId("tally-import-error");

    pick(file);
    fireEvent.click(screen.getByTestId("tally-import-button"));
    await waitFor(() => expect(sent).toHaveLength(2));
  });
});

/**
 * ⛔⛔ THIS TEST EXISTS BECAUSE A MUTATION PROOF FOUND MY OWN SUITE BLIND.
 *
 * Deleting `"tally-import"` from AppShell's COMPANY_SCOPED_SCREENS broke NOTHING:
 * all 25 tests stayed green. That set is only consulted when `company === null`
 * (`needsCompany && !companyReady` chooses the "Preparing company…" placeholder),
 * and every test above hands the provider an `initialCompany` — so the
 * registration point was real, load-bearing, and completely unobserved.
 *
 * Without the entry the screen renders with no company and `useCompanyID()`
 * THROWS during render, taking the whole shell down rather than showing a
 * placeholder. `CompanyProvider` does not bootstrap unless asked, so a provider
 * with no `initialCompany` is a deterministic company-less shell.
 *
 * ⚠️ NOTE FOR WHOEVER ADDS THE NEXT COMPANY-SCOPED SCREEN: nothing in this repo
 * covered the company-less case for ANY of the fifteen screens in that set
 * before this test. It is not `tally-import`-specific — the same hole is open
 * for the other fourteen.
 */
describe("the screen is COMPANY-SCOPED, and the company-less shell survives it", () => {
  it("shows the placeholder rather than throwing when no company is open", () => {
    render(
      <CompanyProvider client={createMockClient()}>
        <AppShell registry={createDefaultRegistry()} />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    altMacO(app);
    // The placeholder, not the screen — and above all not a thrown
    // "no active company selected" that unmounts the application.
    expect(screen.getByRole("status").textContent).toMatch(/preparing company/i);
    expect(screen.queryByTestId("tally-import-screen")).toBeNull();
    // The frame is still titled, so the operator can see WHERE they are while
    // the company loads. Asserted on the chrome's own aria-label rather than on
    // page text, because the screen deliberately has NOT rendered here — its
    // <h1> is exactly what is missing, so a text match would be asserting the
    // thing this test is proving does not happen.
    expect(
      screen.getByRole("banner").getAttribute("aria-label"),
    ).toContain("Import from Tally");
  });
});

describe("the screen says the consequences BEFORE the operator acts", () => {
  it("names the company the export will be posted INTO, and that it cannot be undone", () => {
    const app = renderShell();
    altMacO(app);
    const target = screen.getByTestId("tally-import-target").textContent ?? "";
    // An import posts into whichever company is open. Discovering that from a
    // Trial Balance afterwards costs the book: the ledger path is immutable, so
    // nothing can remove the vouchers it wrote.
    expect(target).toContain("Demo Traders");
    expect(target).toMatch(/no way to undo/i);
  });

  it("offers no import until a file has been chosen", () => {
    const app = renderShell();
    altMacO(app);
    expect(
      (screen.getByTestId("tally-import-button") as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it("does not offer Ctrl+A — there is nothing to 'accept' here", () => {
    const app = renderShell();
    altMacO(app);
    // The one action is an import that cannot be undone, behind a named button.
    // A generic Accept would fire an irreversible write from the key an operator
    // presses reflexively on every form in the product.
    expect(screen.queryByRole("button", { name: /Ctrl\+A/ })).toBeNull();
  });
});
