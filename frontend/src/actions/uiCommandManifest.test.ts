import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  SCREEN_KINDS,
  buildUICommandManifest,
  renderUICommandManifest,
} from "./uiCommandManifest";

// ⚠️ `__dirname`, NOT `new URL(..., import.meta.url)`. Vitest transforms this
// module through Vite, so `import.meta.url` is not a file: URL here and
// `fileURLToPath` throws "The URL must be of scheme file" — measured, not
// guessed. The three cost-screen source scans in
// screens/reports/CostCategorySummaryScreen.test.tsx use `__dirname` for the
// same reason.
const MANIFEST_PATH = join(
  __dirname,
  "..",
  "api",
  "goldens",
  "ui-commands.json",
);

/**
 * THE STALENESS GATE — mirrors internal/printing's TestPrintTemplateSurfaceGolden
 * (template_surface_golden_test.go:233-254).
 *
 * ⛔ COMPARE, DO NOT WRITE. There is NO `git diff --exit-code` step anywhere in
 * this repository's CI or Makefile, so a generator that merely writes its
 * artefact and moves on CANNOT FAIL CI ON STALENESS: the file is rewritten in
 * the workspace, the job is green, and the checked-in copy the Go coverage test
 * reads still describes yesterday's UI. A stale manifest is worse than no
 * manifest, because the coverage gate then asserts — confidently — against a
 * keyboard that no longer exists.
 */
describe("the UI-command manifest golden", () => {
  it("matches the checked-in artefact", () => {
    const got = renderUICommandManifest();
    if (process.env.UPDATE_UI_MANIFEST) {
      writeFileSync(MANIFEST_PATH, got);
      return;
    }
    const want = readFileSync(MANIFEST_PATH, "utf8");
    expect(
      got,
      "src/api/goldens/ui-commands.json is STALE — the live keymap, " +
        "SCREEN_ACTIONS table, Gateway menu, screen registry or REST client has " +
        "changed since it was generated. The Go-side W0 D3 coverage test reads " +
        "this file, so a stale copy makes that gate assert yesterday's UI. " +
        "Regenerate with: UPDATE_UI_MANIFEST=1 pnpm run test:run -- uiCommandManifest",
    ).toBe(want);
  });
});

/**
 * THE SHAPE COUNTERWEIGHTS — §0.1 item 4's rule, applied to a generated file.
 *
 * These assert what must be true of ANY valid manifest, so that a generator
 * which quietly stopped walking (an empty keymap, a registry that failed to
 * build, a census that read one screen and called it the board) turns this suite
 * RED instead of writing a smaller golden and passing on the next run.
 *
 * ⚠️ THEY ASSERT PROPERTIES, NOT A LIST. Nothing here names the 54 global
 * bindings or the 37 screens: that is the golden's job, and restating it in
 * TypeScript would recreate the second copy the golden exists to abolish.
 */
describe("the manifest's shape", () => {
  const m = buildUICommandManifest();

  it("is not empty, which is what a generator that scanned nothing would leave", () => {
    // ⛔ THE "SCANNED NOTHING" FATAL every walker in this repo carries
    // (boundaries_test.go:746/:829/:877, route_vocabulary_test.go:120). A census
    // of zero commands satisfies every per-command assertion below vacuously.
    expect(m.commands.length).toBeGreaterThan(200);
    expect(m.counts.global_bindings).toBeGreaterThan(40);
    expect(m.counts.screen_scoped_bindings).toBeGreaterThan(150);
    expect(m.counts.gateway_entries).toBeGreaterThan(10);
    expect(m.counts.client_routes).toBeGreaterThan(100);
    // A generated file with no note is a hand-edited one.
    expect(m.note).toContain("GENERATED");
    expect(m.note).toContain("UPDATE_UI_MANIFEST=1");
  });

  it("counts agree with the arrays they summarise", () => {
    // The counts are what the Go side reads first; a count that disagreed with
    // its own array would be a manifest that lies cheaply and is believed.
    expect(m.counts.commands).toBe(m.commands.length);
    expect(m.counts.screen_kinds).toBe(m.screen_kinds.length);
    expect(m.counts.route_keys).toBe(m.route_keys.length);
    expect(m.counts.client_routes).toBe(m.client_routes.length);
    expect(
      m.counts.global_bindings +
        m.counts.screen_scoped_bindings +
        m.counts.gateway_entries,
    ).toBe(m.commands.length);
  });

  it("gives every ScreenKind a route key", () => {
    const keys = new Set(m.route_keys.map((r) => r.key));
    for (const kind of SCREEN_KINDS) {
      expect(
        keys.has(kind),
        `${kind} has no route key, so the registry walk missed it and every ` +
          `binding scoped to that screen is being censused against a screen ` +
          `the manifest does not believe exists`,
      ).toBe(true);
    }
  });

  it("keeps the grid RAGGED — some screens override keys, some do not", () => {
    // ⛔ THE CONTROL (the insightd precedent, tests/test_pii_persistence.py:183-195).
    // A manifest where EVERY screen carries the same overrides, or where NO screen
    // does, is a manifest built from a bug — the first means the per-screen table
    // was flattened into the global one, the second means `screenBindings` was
    // never read at all. Both produce a large, plausible, wrong artefact that
    // every other assertion here would accept.
    const scoped = new Set(
      m.commands.filter((c) => c.surface === "keymap-screen").map((c) => c.scope),
    );
    expect(scoped.size).toBeGreaterThan(1);
    expect(scoped.size).toBeLessThan(SCREEN_KINDS.length);
    const withNone = SCREEN_KINDS.filter((k) => !scoped.has(k));
    expect(withNone.length).toBeGreaterThan(0);
  });

  it("gives every command a distinct id", () => {
    // Two commands under one id silently collapse into one on the Go side, and
    // the coverage gate then reports a key it never checked as covered.
    const ids = m.commands.map((c) => c.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("normalises every combo, so the Go side never re-derives one", () => {
    // ⛔ THE CONTROL FOR normalizeCombo. KeyCombo is `= string` (types.ts:766),
    // not a union, so an un-normalised "ctrl+a" would type-check, land in the
    // golden, and be compared against a canonical "Ctrl+A" on the Go side —
    // producing a coverage miss that looks like a missing feature.
    for (const c of m.commands) {
      if (c.combo === null) continue;
      const mods = c.combo.split("+").slice(0, -1);
      for (const mod of mods) {
        expect(
          ["Ctrl", "Alt", "Shift", "Meta"].includes(mod),
          `${c.id} carries the un-normalised modifier "${mod}"`,
        ).toBe(true);
      }
    }
  });
});

/**
 * THE DEAD-KEY CENSUS — the reason this manifest is worth generating at all.
 *
 * A binding is DEAD when the shell resolves it, finds no SCREEN_ACTIONS entry,
 * forwards it to a navigation machine with no case for it, sees an unchanged
 * stack and tells the operator the key "does not apply on this screen". Every
 * other registration can be present and correct — keymap binding, KeymapAction,
 * actionLabel, ScreenActionKind, a registered screen handler — and the key still
 * does nothing, silently, because unit tests call the dispatcher directly and
 * bypass the shell's map (AppShell.tsx's own comment on `importBankStatement`).
 *
 * ⛔ THIS TEST FOUND A LIVE ONE: `selectFinancialYear` (F4 on the GSTR-9 screen)
 * had all six other registrations and no SCREEN_ACTIONS entry, so the financial
 * year picker could not be reopened once a year had been chosen. Fixed in the
 * same change that added this census.
 */
/**
 * THE DEAD KEYS THIS LANE FOUND AND COULD NOT FIX FROM ITS OWN FILES.
 *
 * ⛔ IT IS AN EXACT-EQUALITY LEDGER, NOT AN ALLOWLIST, AND THE DIFFERENCE IS THE
 * WHOLE POINT. A `not.toContain` style suppression goes green for a dead key
 * nobody has ever looked at; this list must EQUAL the census's finding, so a new
 * dead key fails the suite AND fixing a booked one fails it too, until the row
 * is deleted. A ledger that cannot go stale in the green direction is the only
 * kind worth having — see authz/vocabulary.go:24-57 on the token that outlived
 * its route.
 *
 * ⚠️ BOTH ROWS ARE THE SAME DEFECT, on the same screen, and NEITHER can be fixed
 * from this lane's files. The Bank Reconciliation screen overrides Alt+F5 to
 * `detailed` and Ctrl+H to `changeView` (keymaps/tallyprime.ts:417-418), but both
 * reducer arms begin `if (!isReportScreen) return {}` (navMachine.ts:1514-1529),
 * and `isReportScreen` is exactly `screen === "report" || screen ===
 * "report-detail"` (:921). `bank-reconciliation` is a ScreenKind and not a report
 * FRAME — deliberately, because the statement is per bank ledger — so both keys
 * resolve, dispatch to nothing, and print "does not apply on this screen" on the
 * one screen that claims them.
 *
 * The fix is one of two lines, in files this lane must not touch:
 *   (a) keymaps/tallyprime.ts — rebind both to `unavailable` with a note, the
 *       way F5 on the same screen already is (:412-416). Honest and cheap: it
 *       tells the operator the toggle is not built rather than staying mute.
 *   (b) navMachine.ts — widen `isReportScreen`, or give the two arms a
 *       bank-reconciliation case. This is the larger claim: it asserts the
 *       statement HAS a detailed/condensed and a horizontal/vertical form, and
 *       the screen would then have to render them.
 * (a) is the smaller true statement and should be taken first.
 */
const KNOWN_DEAD: readonly string[] = [
  "key:bank-reconciliation:Alt+F5",
  "key:bank-reconciliation:Ctrl+H",
];

describe("the dead-key census", () => {
  const m = buildUICommandManifest();

  it("finds no dead key the ledger below has not already booked", () => {
    const dead = m.commands.map((c) => (c.dispatch === "dead" ? c.id : "")).filter(Boolean);
    expect(
      dead.sort(),
      "the set of BOUND-AND-DEAD keys has changed. A dead key is one the shell " +
        "resolves, no SCREEN_ACTIONS entry claims, and the navigation machine " +
        "does not move for on any stack — the operator is told it does not " +
        "apply. If a NEW id appeared: fix it by adding the SCREEN_ACTIONS entry " +
        "(the usual cause), by giving the machine a case, or by binding the " +
        "combo to an explicit `unavailable` action that says the feature is not " +
        "in this edition. If an id DISAPPEARED because you fixed it: delete its " +
        "row from KNOWN_DEAD and its paragraph above",
    ).toEqual([...KNOWN_DEAD].sort());
  });

  it("still classifies most keys as live, so an all-dead census would be visible", () => {
    // ⛔ THE CONTROL FOR THE TEST ABOVE. `expect([]).toEqual([])` also passes on
    // a classifier that returns "unavailable" for everything, or on a walk that
    // produced no commands at all. This asserts the classifier discriminates:
    // real dispatch of each kind must be present in quantity.
    const by = (d: string) => m.commands.filter((c) => c.dispatch === d).length;
    expect(by("screen-action")).toBeGreaterThan(5);
    expect(by("machine")).toBeGreaterThan(20);
    expect(by("unavailable")).toBeGreaterThan(50);
    expect(m.counts.dead_commands).toBe(by("dead"));
  });

  it("routes GSTR-9's F4 to the financial-year picker", () => {
    // ⛔ THE REGRESSION PIN for the defect this census surfaced. It is written
    // out rather than left to the "no dead keys" assertion above because that
    // one would go green again the moment somebody deleted the binding instead
    // of fixing it — and a deleted F4 on GSTR-9 is the same dead key with less
    // evidence.
    const f4 = m.commands.find((c) => c.id === "key:gstr9:F4");
    expect(f4?.action).toBe("selectFinancialYear");
    expect(f4?.screen_action).toBe("selectFinancialYear");
    expect(f4?.dispatch).toBe("screen-action");
  });
});

/**
 * THE CLIENT-CALL CENSUS — the half of W0's coverage gate the Go registry joins
 * on.
 *
 * The Go side walks the chi router and produces an ActionSpec per mounted
 * (method, route); this produces a (verb, path) per method the shipped REST
 * client actually calls, by CALLING each one against a recording transport. The
 * gate compares them in both directions: a client call the router does not mount
 * is a finding, and a mounted route no client call reaches is an action no human
 * can drive, which must be declared with a reason on the Go side.
 */
describe("the client-call census", () => {
  const m = buildUICommandManifest();

  it("records one call per client method, with no gaps", () => {
    // ⛔ THE SCANNED-NOTHING FLOOR. Every assertion below is vacuous over an
    // empty list, and an empty list is exactly what a recorder wired to the
    // wrong seam would produce — while the Go registry, reading the same file,
    // reported 230 unreachable actions and blamed the product.
    expect(m.client_calls.length).toBeGreaterThan(100);
    expect(m.counts.client_calls).toBe(m.client_calls.length);
    // ⛔ EVERY CoredClient METHOD MUST APPEAR. A method that made no HTTP call
    // is one the registry is blind to — whatever route it hits is invisible to
    // the coverage gate — and the census throws rather than dropping it, so this
    // is the belt to that braces.
    const methods = new Set(m.client_calls.map((c) => c.method));
    for (const name of m.client_routes) {
      expect(methods.has(name), `${name} recorded no HTTP call`).toBe(true);
    }
    // And there are MORE than CoredClient's, because CoredClient is not the only
    // thing in this app that talks to cored: the drafts surface is a separate
    // client plus three lifecycle POSTs built inside the accept flow. Counting
    // only the big client reported the six /drafts routes as unreachable while
    // the Bill Inbox drives them on every open.
    expect(methods.size).toBeGreaterThan(m.client_routes.length);
    expect(methods.has("listDrafts")).toBe(true);
    expect(methods.has("recordAccepted")).toBe(true);
  });

  it("gives every call a resolvable shape", () => {
    for (const c of m.client_calls) {
      expect(["GET", "POST", "PUT", "DELETE"], `${c.method} verb`).toContain(
        c.verb,
      );
      expect(c.path.startsWith("/v1/"), `${c.method}: ${c.path}`).toBe(true);
      // ⛔ NO `undefined` AND NO `[object Object]`. Both are what a census that
      // called each method with junk arguments would produce, and both resolve
      // to nothing on the Go side — where they would be reported as routes the
      // router does not mount, i.e. as a product defect rather than as a broken
      // census.
      expect(c.path, `${c.method} interpolated a real argument`).not.toContain(
        "undefined",
      );
      expect(c.path).not.toContain("[object");
    }
  });

  it("interpolates NAMED placeholders the Go side can resolve", () => {
    // ⛔ THE CONTROL FOR parameterNames. If `fn.toString()` ever stopped
    // yielding parameter names — a minifying transform, a decorator — every
    // path would still look plausible while carrying names no resolver knows,
    // and the join would collapse to nothing. Company-scoped paths must name
    // the company parameter, because that is the one the Go side substitutes
    // the walk's own company id into.
    const scoped = m.client_calls.filter((c) =>
      c.path.startsWith("/v1/companies/{"),
    );
    expect(scoped.length).toBeGreaterThan(100);
    for (const c of scoped) {
      expect(
        c.path.toLowerCase().startsWith("/v1/companies/{company"),
        `${c.method}: ${c.path} does not name a company parameter`,
      ).toBe(true);
    }
  });

  it("keeps `used` DISCRIMINATING in both directions", () => {
    // ⛔ THE CONTROL (the insightd precedent). A scan that marked everything
    // used — the failure mode of a regex that matched too much — would hand the
    // Go registry a clean bill of health it did not earn; one that marked
    // nothing used would turn every action into a declared orphan. Both are
    // caught by requiring the split to be lopsided but non-empty.
    const unused = m.client_calls.filter((c) => !c.used);
    expect(unused.length).toBeGreaterThan(0);
    expect(unused.length).toBeLessThan(m.client_calls.length / 4);
    expect(m.counts.unused_client_methods).toBe(
      new Set(unused.map((c) => c.method)).size,
    );
    // A method the app plainly uses must be marked used, and one it plainly
    // does not must not: two named rows, so the property is not just a ratio.
    expect(m.client_calls.find((c) => c.method === "getDayBook")?.used).toBe(
      true,
    );
    expect(m.client_calls.find((c) => c.method === "alterBudget")?.used).toBe(
      false,
    );
  });
});
