/**
 * D84 · RUNNER A — the corpus against `createMockClient()`.
 *
 * ⛔⛔ WHAT THIS FILE IS FOR. `frontend/src/api/mock.ts` is looser than cored, 61
 * test files build their masters through it, and `App.tsx`'s dev-client switch
 * hands the same mock to a real operator in the dev browser. So every one of
 * those tests — and every dev-browser click — is measured against an engine that
 * enforces less than the server. Three separate lanes shipped a green suite over
 * a body cored refuses.
 *
 * This runner makes that measurable in ONE place. Its twin,
 * `scripts/contract-mock-vs-cored.mts`, drives the identical corpus at live
 * cored; when the two disagree, the mock is wrong until
 * `corpora/mock-vs-cored/masters.json` says otherwise.
 *
 * ⚠️ THE FIRST RUN OF THIS FILE IS EXPECTED TO BE **RED**, AND THAT IS THE
 * DELIVERABLE. The row's oracle is to run both runners BEFORE any fix and watch
 * the divergences print side by side. What you are reading, if it is red, is the
 * list of bodies the mock waves through.
 */
import { describe, it, expect } from "vitest";
import { createMockClient } from "../index";
import { loadCorpus, runRow } from "./corpus";

const corpus = loadCorpus("masters.json");

describe("⛔ D84: api/mock.ts must refuse what cored refuses", () => {
  // ⛔ A FRESH CLIENT PER ROW. The mock's store is per-client, so a shared one
  // would let `ledger-duplicate-name`'s setup leak into the two accept CONTROLs
  // and turn them red for the wrong reason.
  const gapFor = (name: string) =>
    (corpus.mock_gaps ?? []).find((g) => g.row === name);

  for (const row of corpus.rows) {
    const wanted = row.expect.accepted
      ? "is ACCEPTED"
      : `is refused ${row.expect.status} "${row.expect.sentence}"`;
    const gap = gapFor(row.name);

    // ⛔ A PINNED GAP IS ASSERTED TO STILL BE OPEN, NOT SKIPPED. A `.skip` would
    // make the gate green and the gap invisible; asserting the divergence is
    // still there means whoever CLOSES it gets a red test telling them to delete
    // the `mock_gaps` entry. That is the only way a known gap leaves this corpus
    // deliberately rather than by accident — the same reasoning as D82's pin.
    if (gap) {
      it(`⚠️ KNOWN GAP (pinned): ${row.name} — ${gap.current}`, async () => {
        const client = createMockClient({ seed: false });
        await client.createGroup("co-1", {
          name: "Sundry Debtors",
          parent_name: "Primary",
        } as never);
        const outcome = await runRow(client, "co-1", row, "m1");
        expect(
          outcome.divergence,
          `\n  This gap appears to be CLOSED. If you closed it deliberately, delete\n` +
            `  the "${row.name}" entry from mock_gaps in corpora/mock-vs-cored/masters.json\n` +
            `  and this row will be enforced like every other.\n`,
        ).not.toBe("");
      });
      continue;
    }

    it(`${row.name} ${wanted}`, async () => {
      const client = createMockClient({ seed: false });
      // Every row needs the groups it names to exist. The mock resolves a group
      // by name and does not seed the chart, so the corpus's "Sundry Debtors"
      // has to be real here for `ledger-unknown-group` to mean anything.
      await client.createGroup("co-1", {
        name: "Sundry Debtors",
        parent_name: "Primary",
      } as never);

      const outcome = await runRow(client, "co-1", row, "m1");
      expect(
        outcome.divergence,
        `\n  ROW      ${row.name}\n` +
          `  CORED    ${wanted}\n` +
          `  MOCK     ${outcome.got}${outcome.message ? ` — ${outcome.message}` : ""}\n` +
          `  DIVERGES ${outcome.divergence}\n` +
          (row.note ? `  NOTE     ${row.note}\n` : ""),
      ).toBe("");
    });
  }

  it("⛔ the corpus itself is not vacuous", () => {
    // A corpus that lost its rows, or whose rows all became accepts, would make
    // every assertion above pass while asserting nothing — the shape of failure
    // that let D84 exist in the first place (see masterGoto's string-equality
    // guard, D80).
    expect(corpus.rows.length).toBeGreaterThanOrEqual(8);
    const refusals = corpus.rows.filter((r) => !r.expect.accepted);
    expect(refusals.length).toBeGreaterThanOrEqual(6);
    // …and it must keep at least one ACCEPT, or "refuse everything" would pass.
    expect(corpus.rows.some((r) => r.expect.accepted)).toBe(true);
    // Every refusal names a sentence: a status-code-only gate is the thing the
    // handoff calls "never an oracle".
    for (const r of refusals) {
      expect(r.expect.sentence, `${r.name} has no sentence`).toBeTruthy();
    }
  });

  it("⛔ every pinned GAP carries a real reason and names a real row", () => {
    for (const g of corpus.mock_gaps ?? []) {
      expect(
        corpus.rows.some((r) => r.name === g.row),
        `mock_gaps names ${g.row}, which is not a row — the pin guards nothing`,
      ).toBe(true);
      expect(g.current).toBeTruthy();
      expect(g.why.join(" ").length, `${g.row} has no real reason`).toBeGreaterThan(120);
    }
  });

  it("⛔ every deliberate divergence is DECLARED, with a reason", () => {
    // The escape hatch has to be visible, or the honest way to make this gate
    // green is to quietly add rows to it.
    for (const d of corpus.deliberate_divergence) {
      expect(d.what).toBeTruthy();
      expect(d.where).toBeTruthy();
      expect(d.why.length, `${d.what} has no real reason`).toBeGreaterThan(40);
    }
  });
});
