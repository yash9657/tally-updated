import { describe, it, expect, afterEach, beforeEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";
import { ApiError } from "../api/http";
import type { CoredClient } from "../api";
import { UNBUILT_FORMATS } from "./sheet";
import {
  MAILTO_MAX_LENGTH,
  buildMailtoURL,
  draftFor,
  emailHandoffMessage,
} from "./emailDocument";
import type { PrintTarget } from "../screens/printTarget";
import { buttonBarLabels } from "../keystroke/buttonBar";
import type { ScreenKind } from "../keystroke/types";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * B2 — ALT+M E-MAILS A DOCUMENT, AND THE ORDER IS THE PROOF.
 *
 * ✅ THE DECISION: **this product does not send mail.** Alt+M saves the server's
 * PDF and opens the operator's own mail client on a draft. `internal/notify` is
 * untouched — what is missing there is a transport, and we are deliberately not
 * building one.
 *
 * ⛔⛔ WHAT THE CENTRAL TEST IS, AND WHY IT IS AN ORDERING TEST. A web page cannot
 * attach a file to a `mailto:` draft. So the one way this feature fails a customer
 * is a draft that opens with no file saved: the operator types an address, sends,
 * and the invoice e-mail has no invoice on it. Asserting "the PDF was downloaded"
 * and "a draft was opened" SEPARATELY cannot see that — both are true in the
 * broken build too. The assertions here are on the ORDER, from one shared call
 * log, and on the branch where the save FAILS and no draft must open at all.
 */

const COMPANY = "company-demo";

/** One log, both effects, so ORDER is assertable rather than inferred. */
let calls: string[];
let drafts: string[];
let files: { filename: string; type: string; size: number }[];

beforeEach(() => {
  calls = [];
  drafts = [];
  files = [];
});

afterEach(cleanup);

// ===========================================================================
// THE PURE HALF — no React, no client
// ===========================================================================

const invoiceTarget: PrintTarget = {
  kind: "voucher",
  voucherClass: "sales",
  voucherID: "v-1",
  label: "Sales invoice",
};

describe("the draft's wording", () => {
  it("⛔ never claims the document is attached", () => {
    const { body } = draftFor(invoiceTarget, "Demo Traders");
    // ⛔ AT THE MOMENT THIS DRAFT OPENS THERE IS NO FILE ON IT, and there may
    // never be one. Every one of these words asserts an attachment: two of them
    // read as an outright lie to the customer if the operator forgets to attach.
    for (const lie of [
      /attached/i,
      /enclosed/i,
      /please find/i,
      /herewith/i,
      /follows/i,
    ]) {
      expect(body, `the body says "${lie}" about a file that is not there`).not.toMatch(lie);
    }
  });

  it("⛔ never puts the instruction in the MESSAGE — the recipient would read it", () => {
    const { body, subject } = draftFor(invoiceTarget, "Demo Traders");
    // "Please attach the PDF" is addressed to the SENDER. In the body it is a
    // sentence the customer reads, which is nonsense to them and embarrassing to
    // the sender. The instruction belongs in the status line.
    expect(body).not.toMatch(/attach/i);
    expect(subject).not.toMatch(/attach/i);
  });

  it("names the company and the document, so a draft is not blank", () => {
    const { subject, body } = draftFor(invoiceTarget, "Demo Traders");
    expect(subject).toContain("Demo Traders");
    expect(subject).toContain("Sales invoice");
    expect(body).toContain("Demo Traders");
  });

  it("puts nothing load-bearing at the END, because that is what truncates", () => {
    // ⚠️ A CLIENT PAST ITS URL CEILING TRUNCATES RATHER THAN REFUSING, and it
    // truncates the tail. So the tail is a sign-off.
    const { body } = draftFor(invoiceTarget, "Demo Traders");
    const tail = body.split("\r\n").filter((l) => l !== "").slice(-2).join(" ");
    expect(tail).toMatch(/Regards/i);
  });
});

describe("buildMailtoURL", () => {
  it("⛔ has NO recipient, because no party e-mail is stored anywhere", () => {
    // Measured over the whole schema: the only e-mail columns are app_user.email
    // (the operator's login), company_invite.email and company_tds_profile's two.
    // A ledger carries none, so there is nothing to prefill.
    const { url } = buildMailtoURL(draftFor(invoiceTarget, "Demo Traders"));
    expect(url.startsWith("mailto:?")).toBe(true);
    expect(url).not.toMatch(/[?&]to=/);
  });

  it("percent-encodes the subject and body", () => {
    const { url } = buildMailtoURL({ subject: "a b", body: "c d" });
    expect(url).toContain("subject=a%20b");
    expect(url).toContain("body=c%20d");
  });

  it("⛔ encodes a company name containing & so it cannot inject a parameter", () => {
    // encodeURI would leave "&", "?" and "=" alone — so "Smith & Sons" would end
    // the subject early and append a parameter of its own. This is the reason the
    // encoder choice is stated in the module.
    const { url } = buildMailtoURL(draftFor(invoiceTarget, "Smith & Sons"));
    expect(url).not.toMatch(/subject=[^&]*&(?!body=)/);
    expect(url).toContain("%26");
    // Exactly two parameters, ever.
    expect(url.split("&").length).toBe(2);
  });

  it("encodes a line break as %0D%0A, which is what a mail client expects", () => {
    const { url } = buildMailtoURL({ subject: "s", body: "one\r\ntwo" });
    expect(url).toContain("one%0D%0Atwo");
  });

  it("⚠️ trims the BODY (never the subject) to stay under the client ceiling, and SAYS so", () => {
    const long = "x".repeat(5000);
    const { url, draft } = buildMailtoURL({ subject: "keep me", body: long });
    expect(url.length).toBeLessThanOrEqual(MAILTO_MAX_LENGTH);
    expect(url).toContain("subject=keep%20me");
    // ⛔ A SILENTLY TRIMMED BODY IS THE FAILURE THIS FLAG EXISTS TO PREVENT.
    expect(draft.trimmed).toBe(true);
    expect(draft.body.length).toBeLessThan(long.length);
  });

  it("does not claim to have trimmed an ordinary draft", () => {
    const { url, draft } = buildMailtoURL(draftFor(invoiceTarget, "Demo Traders"));
    expect(url.length).toBeLessThanOrEqual(MAILTO_MAX_LENGTH);
    expect(draft.trimmed).toBeUndefined();
  });
});

describe("the handoff message", () => {
  it("⛔ names the file AND both limits, because that is the whole product", () => {
    const msg = emailHandoffMessage("Sales invoice", "sales-invoice.pdf");
    // The file, so the operator knows which one to attach.
    expect(msg).toContain("sales-invoice.pdf");
    // Limit 1: they must attach it themselves.
    expect(msg).toMatch(/ATTACH/);
    expect(msg).toMatch(/cannot put a file on a/i);
    // Limit 2: no recipient, and why.
    expect(msg).toMatch(/no recipient/i);
    expect(msg).toMatch(/does not store a party's e-mail/i);
  });
});

describe("e-mail was removed from the export FORMATS list", () => {
  it("⛔ because it was never a format — it shipped as Alt+M", () => {
    // Tally's own top menu carries `E: Export` and `M: E-mail` as two separate
    // entries (RESEARCH.md:361). Listing e-mail among Alt+E's formats was the
    // framing this implementation rejected, and the F12 panel's "Not built" note
    // is DERIVED from this list — so leaving it here would keep telling operators
    // e-mail is unbuilt after it shipped.
    expect(UNBUILT_FORMATS).not.toContain("e-mail this document");
    // ...and the list still names the two genuine gaps, so this is not a deletion
    // that emptied it.
    expect(UNBUILT_FORMATS).toContain("JPEG");
    expect(UNBUILT_FORMATS).toContain("HTML");
  });
});

// ===========================================================================
// ALT+M, PRESSED ON THE REAL SHELL
// ===========================================================================

/**
 * A client whose print route answers with a real Blob and logs the call.
 *
 * The mock is NOT taught to render a PDF: the bytes begin `%PDF-` so the shape is
 * right, and the LAYOUT is internal/printing's and asserted in Go. What is under
 * test here is the ORDER of the two effects.
 */
function emailingClient(err?: Error): CoredClient {
  const base = createMockClient();
  const answer = async (name: string) => {
    calls.push("fetch");
    if (err) throw err;
    return {
      blob: new Blob(["%PDF-1.4 stub"], { type: "application/pdf" }),
      filename: name,
    };
  };
  return {
    ...base,
    printReport: (_c: string, slug: string) => answer(`${slug}.pdf`),
    printVoucher: (_c: string, cls: string) => answer(`${cls}.pdf`),
  } as CoredClient;
}

/**
 * ⛔ THE SINKS ARE INJECTED THROUGH THE SHELL'S OWN SCREENS, so what is exercised
 * is the real registration path: keymap binding -> resolveAction -> SCREEN_ACTIONS
 * -> the screen's registered handler. A test that called the handler directly
 * would pass with SCREEN_ACTIONS empty, which is exactly how Alt+O once shipped
 * dead with every unit test green.
 *
 * They are installed by monkey-patching the module's default sinks via the two
 * globals the module reads (`window.location.href` and `URL.createObjectURL`),
 * rather than by threading options through 23 screens.
 */
let restore: (() => void)[] = [];

beforeEach(() => {
  restore = [];

  // The DRAFT sink: openMailDraft assigns window.location.href.
  const loc = window.location;
  const spy = { href: "" };
  Object.defineProperty(window, "location", {
    configurable: true,
    get: () => ({
      ...loc,
      set href(v: string) {
        calls.push("draft");
        drafts.push(v);
      },
      get href() {
        return spy.href;
      },
    }),
  });
  restore.push(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: loc,
    });
  });

  // The FILE sink: downloadBlob goes through URL.createObjectURL + an anchor click.
  const prevCreate = (URL as unknown as { createObjectURL?: unknown }).createObjectURL;
  const prevRevoke = (URL as unknown as { revokeObjectURL?: unknown }).revokeObjectURL;
  (URL as unknown as { createObjectURL: unknown }) = URL as unknown as {
    createObjectURL: unknown;
  };
  (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = (
    blob: Blob,
  ) => {
    calls.push("file");
    files.push({ filename: "", type: blob.type, size: blob.size });
    return "blob:stub";
  };
  (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = () => {};
  restore.push(() => {
    (URL as unknown as { createObjectURL: unknown }).createObjectURL = prevCreate;
    (URL as unknown as { revokeObjectURL: unknown }).revokeObjectURL = prevRevoke;
  });
});

afterEach(() => {
  for (const undo of restore) undo();
  restore = [];
});

function renderShell(client: CoredClient) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

function goTo(app: HTMLElement, reportName: string) {
  fireEvent.keyDown(app, { key: "g", altKey: true });
  fireEvent.mouseDown(screen.getByText(reportName));
  fireEvent.keyDown(app, { key: "Enter" });
}

/**
 * ⚠️ macOS COMPOSES Option+M INTO "µ", and this is how every Alt binding in this
 * product was once found dead while every browser test passed. Combos resolve on
 * `code`, so the composed `key` is exactly what a real Mac sends and exactly what
 * this must fire.
 */
function pressAltM(app: HTMLElement) {
  fireEvent.keyDown(app, { key: "µ", code: "KeyM", altKey: true });
}

function statusLine(): string {
  return screen.getByTestId("status-line").textContent ?? "";
}

describe("Alt+M on the real shell", () => {
  it("⛔⛔ SAVES THE PDF BEFORE OPENING THE DRAFT — asserted as an ORDER", async () => {
    const app = renderShell(emailingClient());
    goTo(app, "Trial Balance");
    pressAltM(app);

    await waitFor(() => expect(drafts.length).toBe(1));

    // ⛔ THE ASSERTION THE WHOLE TRACK TURNS ON. Both effects happening is NOT the
    // property — a build that opens the draft first also does both, and it hands
    // the operator a draft with nothing on disk to attach.
    expect(calls).toEqual(["fetch", "file", "draft"]);
    expect(
      calls.indexOf("file"),
      "the draft opened BEFORE the PDF was saved: the operator has a draft and no file",
    ).toBeLessThan(calls.indexOf("draft"));
  });

  it("opens a draft whose subject and body are encoded", async () => {
    const app = renderShell(emailingClient());
    goTo(app, "Trial Balance");
    pressAltM(app);
    await waitFor(() => expect(drafts.length).toBe(1));

    const url = drafts[0] ?? "";
    expect(url.startsWith("mailto:?")).toBe(true);
    expect(url).toMatch(/subject=[^&]+/);
    expect(url).toMatch(/body=[^&]+/);
    // Encoded, not raw: a space never survives as a space in a query value.
    expect(url).not.toMatch(/subject=[^&]* /);
    expect(decodeURIComponent(url)).toContain("Demo Traders");
  });

  it("the status line names the saved file and tells the operator to attach it", async () => {
    const app = renderShell(emailingClient());
    goTo(app, "Trial Balance");
    pressAltM(app);
    await waitFor(() => expect(drafts.length).toBe(1));

    const line = statusLine();
    expect(line).toContain("trial-balance.pdf");
    expect(line).toMatch(/ATTACH/);
    expect(line).toMatch(/no recipient/i);
  });

  it("⛔ opens NO DRAFT when the PDF cannot be saved", async () => {
    // ⚠️ SIMULATED THE WAY `downloadBlob` ACTUALLY FAILS. Its guard is
    // `canSaveAFile()`, which tests `typeof URL.createObjectURL === "function"` —
    // the headless / SSR case it was written for. The first version of this probe
    // made createObjectURL THROW instead, which is a different failure: it
    // escaped the handler entirely, so there was no status line at all and the
    // probe timed out rather than failing on the assertion. That found a real gap
    // (the file step is now wrapped) but it was not this test's subject, so this
    // one now drives the documented path.
    delete (URL as unknown as { createObjectURL?: unknown }).createObjectURL;
    const app = renderShell(emailingClient());
    goTo(app, "Trial Balance");
    pressAltM(app);

    await waitFor(() => expect(statusLine()).toMatch(/could not e-mail/i));
    // ⛔ THE BRANCH THAT MATTERS. A draft here is a set-up to send an empty
    // invoice, and the operator would have no way to know.
    expect(drafts).toEqual([]);
    expect(statusLine()).toMatch(/no draft was opened/i);
  });

  it("⛔ opens NO DRAFT when the server refuses, and shows the server's sentence", async () => {
    const app = renderShell(
      emailingClient(new ApiError(501, "printing the sales-order-book report is not implemented yet")),
    );
    goTo(app, "Trial Balance");
    pressAltM(app);

    await waitFor(() => expect(statusLine()).toMatch(/could not e-mail/i));
    expect(drafts).toEqual([]);
    expect(calls).toEqual(["fetch"]);
    // Verbatim — the rule usePrint and exportAsPdf both follow.
    expect(statusLine()).toContain("is not implemented yet");
  });

  it("⛔ a REFUSAL makes no request, saves no file and opens no draft", async () => {
    const app = renderShell(emailingClient());
    // ⚠️ AN UNSAVED VOUCHER, AND THE FIRST VERSION OF THIS PROBE GOT IT WRONG.
    // It used "Sales Order Book", assuming a report with no layout is refused
    // LOCALLY. It is not: that slug is MAPPED on purpose — so `print
    // report:sales-order-book` is governable and Ctrl+P says the layout does not
    // exist — and the refusal comes from the SERVER as a 501. Asserting "no
    // request was made" there was asserting something false about a correct
    // build, which is rule 5's refusal for the wrong reason.
    //
    // F8 opens a Sales voucher and nothing is saved, so `resolvePrintTarget`
    // refuses with no id — the most realistic operator mistake there is.
    fireEvent.keyDown(app, { key: "F8" });
    pressAltM(app);

    await waitFor(() => expect(statusLine()).toMatch(/not been saved/i));
    // Invariant I-5, extended to this key: a refusal that still hits the network
    // is a 404 in somebody's log — and here it would also open an empty draft
    // about a document that does not exist.
    expect(calls).toEqual([]);
    expect(drafts).toEqual([]);
  });

  it("⛔ Alt+M reaches the screen at all — the SCREEN_ACTIONS registration", async () => {
    // ⛔ THIS IS THE POINT THAT SHIPS A KEY DEAD SILENTLY. Everything else can be
    // correct — the keymap binding, the KeymapAction, the ScreenActionKind, the
    // bar item and the screen's own hook — and the shell still never dispatches
    // it. Driving the key through the real shell is the only thing that sees it.
    const app = renderShell(emailingClient());
    goTo(app, "Trial Balance");
    expect(statusLine()).not.toMatch(/ATTACH/);
    pressAltM(app);
    await waitFor(() => expect(statusLine()).toMatch(/ATTACH/));
  });

  it("Ctrl+P and Alt+M agree about what the document is", async () => {
    // They ask the SAME resolvePrintTarget, and `usePrintAction` registers both
    // from one hook — so a screen cannot offer one and not the other. Here: the
    // report both refuse, refused identically.
    const app = renderShell(emailingClient());
    // The same unsaved Sales voucher: a LOCAL refusal, so both keys return
    // `target.reason` and the two sentences must be byte-identical. On a frame
    // that PRINTS they differ by design — one says "Printed", the other explains
    // the attachment — so the agreement being asserted is about the DOCUMENT
    // resolution, which is the thing they share.
    fireEvent.keyDown(app, { key: "F8" });

    fireEvent.keyDown(app, { key: "p", ctrlKey: true });
    await waitFor(() => expect(statusLine()).not.toBe(""));
    const printRefusal = statusLine();

    pressAltM(app);
    await waitFor(() => expect(statusLine()).not.toBe(""));
    expect(statusLine()).toBe(printRefusal);
    expect(calls).toEqual([]);
  });
});

// ===========================================================================
// THE BAR INVARIANT — and the blind spot that made it necessary
// ===========================================================================

/**
 * ⛔ ALT+M APPEARS ON EXACTLY THE BARS THAT OFFER Ctrl+P, AND NOWHERE ELSE.
 *
 * ⚠️ THIS TEST EXISTS BECAUSE A MUTATION PROOF FOUND `buttons.test.ts` BLIND TO
 * IT. Adding `item("Alt+M", "E-Mail")` to the TDS challan bar — a screen with no
 * printable document at all, which is why it deliberately carries no Ctrl+P —
 * left the whole suite GREEN. That gate ("no advertised button is decorative")
 * checks that a bar's combo RESOLVES to a built action and is a known side
 * effect, and Alt+M is a GLOBAL binding in a side-effect list, so it satisfies
 * both on every screen in the product. Ctrl+P has the identical hole and always
 * has.
 *
 * So the property has to be asserted directly, and it is worth asserting: the two
 * keys ask the SAME `resolvePrintTarget` what the document is, and
 * `usePrintAction` registers both from one hook. A bar offering one without the
 * other advertises a difference that does not exist — and on a screen with no
 * printable document it advertises a key whose only possible answer is a refusal.
 *
 * The screen list is read from `corpora/validate.py`'s KNOWN_SCREENS, which is
 * this repository's canonical set of ScreenKinds (a kind missing from it fails
 * `make verify`). Reading it rather than restating it means a screen added later
 * is covered here automatically, instead of quietly escaping the invariant.
 */
describe("Alt+M is advertised exactly where Ctrl+P is", () => {
  const screens = knownScreens();

  it("reads a non-trivial screen list from the corpora vocabulary", () => {
    // Guard against a parse that silently matched nothing, which would make
    // every assertion below pass vacuously.
    expect(screens.length).toBeGreaterThan(10);
    expect(screens).toContain("voucher");
    expect(screens).toContain("report");
  });

  it("⛔ offers both keys or neither, on every screen the product has", () => {
    const mismatched: string[] = [];
    for (const screen of screens) {
      const labels = buttonBarLabels(screen as ScreenKind);
      const hasPrint = labels.some((l) => l.startsWith("Ctrl+P"));
      const hasEmail = labels.some((l) => l.startsWith("Alt+M"));
      if (hasPrint !== hasEmail) {
        mismatched.push(
          `${screen}: Ctrl+P=${hasPrint} Alt+M=${hasEmail}`,
        );
      }
    }
    expect(
      mismatched,
      "a bar advertises one of the two document keys without the other. They " +
        "resolve the SAME document and are registered by the same hook, so the " +
        "bar is claiming a difference that does not exist",
    ).toEqual([]);
  });

  it("control: at least one screen offers BOTH, and at least one offers NEITHER", () => {
    // Without this the assertion above passes on a build where Alt+M is on no bar
    // at all — which is exactly the state that ships a key nobody can discover.
    const both = screens.filter((s) => {
      const l = buttonBarLabels(s as ScreenKind);
      return l.some((x) => x.startsWith("Ctrl+P")) && l.some((x) => x.startsWith("Alt+M"));
    });
    const neither = screens.filter((s) => {
      const l = buttonBarLabels(s as ScreenKind);
      return !l.some((x) => x.startsWith("Ctrl+P")) && !l.some((x) => x.startsWith("Alt+M"));
    });
    expect(both.length).toBeGreaterThan(0);
    expect(neither.length).toBeGreaterThan(0);
  });
});

/** The canonical ScreenKind set, read from the corpora vocabulary. */
function knownScreens(): string[] {
  const here = dirname(fileURLToPath(import.meta.url));
  const src = readFileSync(
    join(here, "..", "..", "..", "corpora", "validate.py"),
    "utf8",
  );
  const m = /KNOWN_SCREENS = \{(.*?)\n\}/s.exec(src);
  if (!m) throw new Error("could not find KNOWN_SCREENS in corpora/validate.py");
  return [...m[1]!.matchAll(/"([a-z0-9-]+)"/g)].map((x) => x[1]!);
}
