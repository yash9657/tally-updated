/**
 * D84 — the shared reader for `corpora/mock-vs-cored/*.json`.
 *
 * ⛔ ONE READER, TWO RUNNERS. A vitest runner drives `createMockClient()`; a
 * script runner drives `createRestClient()` against live cored. They MUST agree
 * about what a row means, or the gate reports a disagreement between the runners
 * rather than between the engines — so the row semantics live here and nowhere
 * else.
 *
 * ⚠️ THE CORPUS IS READ WITH `node:fs`, NOT IMPORTED. It lives OUTSIDE
 * `frontend/`, so a static `import` would need `resolveJsonModule` plus a vite
 * `fs.allow` entry, and the script runner and the test runner would each need a
 * different one. Both runners execute in node, so reading the file is the one
 * mechanism that works identically for both.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { CoredClient } from "../client";

/** What a row asserts: either a refusal with a status + sentence, or an accept. */
export interface RowExpectation {
  status?: number;
  sentence?: string;
  accepted?: boolean;
}

export interface CorpusCall {
  call: string;
  body: Record<string, unknown>;
}

export interface CorpusRow extends CorpusCall {
  name: string;
  /** Calls to make FIRST, so a row that needs a collision can cause its own. */
  setup?: CorpusCall[];
  expect: RowExpectation;
  note?: string;
}

/**
 * A row cored enforces and the mock does NOT — pinned rather than deleted, so
 * closing it is deliberate. Runner A asserts the gap is STILL OPEN; whoever
 * closes it deletes the entry, which is the only way a known gap leaves this
 * file on purpose instead of by accident.
 */
export interface MockGap {
  row: string;
  current: string;
  why: string[];
}

export interface Corpus {
  mock_gaps?: MockGap[];
  deliberate_divergence: { what: string; where: string; why: string }[];
  rows: CorpusRow[];
}

/**
 * ⛔ THE DISPATCH IS EXPLICIT, NOT `(client as any)[call]`. A typo'd `call` in
 * the JSON would otherwise be `undefined`, throw a TypeError, and the row would
 * be reported as "the engine refused" — a corpus typo that reads as a passing
 * refusal is the exact failure mode this whole row exists to stop.
 */
const DISPATCH: Record<
  string,
  (c: CoredClient, companyID: string, body: Record<string, unknown>) => Promise<unknown>
> = {
  createLedger: (c, co, b) => c.createLedger(co, b as never),
  createGroup: (c, co, b) => c.createGroup(co, b as never),
  createUnit: (c, co, b) => c.createUnit(co, b as never),
  createStockItem: (c, co, b) => c.createStockItem(co, b as never),
};

export function knownCall(call: string): boolean {
  return Object.prototype.hasOwnProperty.call(DISPATCH, call);
}

export function loadCorpus(file = "masters.json"): Corpus {
  const here = dirname(fileURLToPath(import.meta.url));
  const path = join(here, "..", "..", "..", "..", "corpora", "mock-vs-cored", file);
  const parsed = JSON.parse(readFileSync(path, "utf8")) as Corpus;
  if (!Array.isArray(parsed.rows) || parsed.rows.length === 0) {
    throw new Error(`contract corpus ${file} has no rows — the gate would pass vacuously`);
  }
  for (const row of parsed.rows) {
    if (!knownCall(row.call)) {
      throw new Error(
        `contract corpus row ${row.name} names call ${JSON.stringify(row.call)}, ` +
          `which the dispatch does not know. Add it to DISPATCH rather than letting ` +
          `the row report a TypeError as a refusal.`,
      );
    }
  }
  return parsed;
}

/** Substitute `{STAMP}` so a row can be re-run against a persistent database. */
export function stamped(
  body: Record<string, unknown>,
  stamp: string,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(body)) {
    out[k] = typeof v === "string" ? v.replaceAll("{STAMP}", stamp) : v;
  }
  return out;
}

export interface RowOutcome {
  /** "accepted", or "refused <status>". */
  got: string;
  message: string;
  /** Empty when the row held. Otherwise why it did not, in one sentence. */
  divergence: string;
}

/**
 * Run one row against one client and judge it. Shared so the two runners cannot
 * drift on what "held" means.
 */
export async function runRow(
  client: CoredClient,
  companyID: string,
  row: CorpusRow,
  stamp: string,
): Promise<RowOutcome> {
  for (const s of row.setup ?? []) {
    if (!knownCall(s.call)) throw new Error(`unknown setup call ${s.call}`);
    // ⚠️ A FAILED SETUP IS SWALLOWED ON PURPOSE. The only setup in the corpus
    // today creates the row that the row itself then collides with, and against
    // a persistent database a re-run finds it already there. A setup that
    // legitimately cannot succeed shows up as the ROW failing, which is where a
    // reader can act on it.
    try {
      await DISPATCH[s.call]!(client, companyID, stamped(s.body, stamp));
    } catch {
      /* see above */
    }
  }

  const body = stamped(row.body, stamp);
  const want = row.expect;
  try {
    await DISPATCH[row.call]!(client, companyID, body);
    if (want.accepted) return { got: "accepted", message: "", divergence: "" };
    return {
      got: "accepted",
      message: "",
      divergence: `ACCEPTED a body cored refuses with ${want.status} ${JSON.stringify(want.sentence)}`,
    };
  } catch (err) {
    const status = (err as { status?: number }).status ?? -1;
    const message = err instanceof Error ? err.message : String(err);
    if (want.accepted) {
      return {
        got: `refused ${status}`,
        message,
        divergence: `REFUSED (${status} ${message}) a body cored accepts`,
      };
    }
    if (status !== want.status) {
      return {
        got: `refused ${status}`,
        message,
        divergence: `refused with ${status}, cored answers ${want.status} — ${message}`,
      };
    }
    if (want.sentence && !message.includes(want.sentence)) {
      return {
        got: `refused ${status}`,
        message,
        divergence: `right status, WRONG SENTENCE: wanted ${JSON.stringify(want.sentence)}, got ${JSON.stringify(message)}`,
      };
    }
    return { got: `refused ${status}`, message, divergence: "" };
  }
}
