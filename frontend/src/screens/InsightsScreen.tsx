import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  type Insight,
  type InsightDigest,
  type InsightSuppression,
  type InsightVerdict,
  type VoucherView,
} from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { useLoadedVoucher } from "../app/LoadedVoucher";
import type { ScreenComponentProps } from "../app/screenRegistry";
import { dayBookRowKey } from "./vouchers/voucherClass";

/**
 * THE INSIGHTS INBOX (W4, d125) — what the insight engine found in these books,
 * and the morning digest it made of them.
 *
 * Reached from `Gateway of Tally > Insights` and Alt+G. Every key is on the
 * keyboard; the list and the voucher list each take focus:
 *
 *   ↑ / ↓     highlight an insight; its figures, the action, and its vouchers
 *             render below
 *   U / N / W verdict: Useful · Not useful · Wrong. N and W dismiss it; W is
 *             what teaches the ranker that the type misfires (p_real)
 *   V         re-run its evidence SQL now, read-only: do the books still
 *             reproduce every figure?
 *   S         suppress this TYPE from the digest (the Note is the reason)
 *   L         lift the most recent suppression
 *   D         compose (or show) today's digest
 *   R         run today's detectors now (once per day; the poller does it after
 *             02:00 IST anyway)
 *   Tab       move to the insight's vouchers: ↑ / ↓ highlight, Enter opens the
 *             voucher in its own screen (alteration), exactly as the Day Book does
 *
 * ⛔ EVERY FIGURE IS THE SERVER'S STRING. Nothing here adds, rounds or reformats
 * a payload value: the evidence gate proved the server's "59384.00", not a
 * number this screen recomputed. `summary` is the deterministic template; only
 * the digest's narrative may be model text, and the screen says which.
 *
 * ⛔ NOTHING HERE WRITES TO THE BOOKS. Acting on an insight happens in the
 * screen it names (the voucher, the e-way bill, the challan).
 */
export function InsightsScreen({ onSelect }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const { setLoaded } = useLoadedVoucher();

  const [rows, setRows] = useState<Insight[] | null>(null);
  const [owed, setOwed] = useState<{ type: string; reason: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [digest, setDigest] = useState<InsightDigest | null>(null);
  const [vouchers, setVouchers] = useState<VoucherView[]>([]);
  const [vActive, setVActive] = useState(0);
  const [evidence, setEvidence] = useState<Record<string, string>>({});
  const [evidenceSQL, setEvidenceSQL] = useState<Record<string, string>>({});
  const [suppressions, setSuppressions] = useState<InsightSuppression[]>([]);
  const listRef = useRef<HTMLUListElement>(null);
  const vRef = useRef<HTMLUListElement>(null);
  const busy = useRef(false);

  const load = useCallback(async () => {
    setError(null);
    try {
      const res = await client.listInsights(companyID);
      setRows(res.insights ?? []);
      setOwed(res.owed ?? []);
    } catch (e) {
      setRows([]);
      setError(
        e instanceof ApiError && e.status === 403
          ? "Your security level may not see insights (display insights is refused). An Owner can grant it under Users & Roles."
          : `Insights could not be read: ${e instanceof Error ? e.message : String(e)}`,
      );
    }
    try {
      setSuppressions((await client.listInsightSuppressions(companyID)).suppressions ?? []);
    } catch {
      setSuppressions([]);
    }
    try {
      setDigest(await client.getInsightDigest(companyID));
    } catch {
      setDigest(null); // 404: not composed yet today — D composes it
    }
  }, [client, companyID]);

  useEffect(() => {
    void load();
  }, [load]);

  const list = rows ?? [];
  const idx = list.length === 0 ? -1 : Math.min(active, list.length - 1);
  const current = idx >= 0 ? (list[idx] ?? null) : null;
  const currentID = current?.id ?? null;

  // The vouchers behind the highlighted insight (first 20), read by id.
  useEffect(() => {
    setVouchers([]);
    setVActive(0);
    if (!current) return;
    let live = true;
    void Promise.all(
      current.voucher_ids.slice(0, 20).map((id) => client.getVoucher(companyID, id).catch(() => null)),
    ).then((vs) => {
      if (live) setVouchers(vs.filter((v): v is VoucherView => v !== null));
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentID, client, companyID]);

  // PUBLISH THE HIGHLIGHTED VOUCHER as a Day Book row, so Enter drills into it
  // through the same machine arm and the same LoadedVoucher slot the Day Book
  // uses. The Day Book entry is read for the voucher's own date.
  const vCurrent = vouchers.length > 0 ? vouchers[Math.min(vActive, vouchers.length - 1)] : undefined;
  useEffect(() => {
    if (!vCurrent) return;
    let live = true;
    void client
      .getDayBook(companyID, { from: vCurrent.voucher_date, to: vCurrent.voucher_date })
      .then((db) => {
        const entry = db.entries.find((e) => e.voucher_id === vCurrent.voucher_id);
        if (!live || !entry) return;
        setLoaded({ entry });
        onSelect(dayBookRowKey(entry));
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vCurrent?.voucher_id]);

  const run = useCallback(
    (what: string, fn: () => Promise<string>) => {
      if (busy.current) return;
      busy.current = true;
      setNotice(`${what}…`);
      void fn()
        .then((msg) => setNotice(msg))
        .catch((e: unknown) =>
          setNotice(`Error: ${e instanceof ApiError ? `${e.status} ${e.message}` : String(e)}`),
        )
        .finally(() => {
          busy.current = false;
        });
    },
    [],
  );

  const verdict = (v: InsightVerdict) => {
    if (!current) return;
    const label = v === "useful" ? "useful" : v === "not_useful" ? "not useful" : "wrong";
    run("Recording your verdict", async () => {
      await client.insightFeedback(companyID, current.id, v, note.trim() || undefined);
      setNote("");
      await load();
      return v === "useful"
        ? `Marked useful: ${current.title}. It stays open until the books clear it.`
        : `Marked ${label} and dismissed: ${current.title}.`;
    });
  };

  const onListKey = (ev: React.KeyboardEvent<HTMLUListElement>) => {
    const k = ev.key.toLowerCase();
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      setActive((a) => Math.min(a + 1, Math.max(list.length - 1, 0)));
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (ev.key === "Tab" && vouchers.length > 0) {
      ev.preventDefault();
      vRef.current?.focus();
    } else if (ev.ctrlKey || ev.altKey || ev.metaKey) {
      return;
    } else if (k === "u") {
      ev.preventDefault();
      verdict("useful");
    } else if (k === "n") {
      ev.preventDefault();
      verdict("not_useful");
    } else if (k === "w") {
      ev.preventDefault();
      verdict("wrong");
    } else if (k === "v" && current) {
      ev.preventDefault();
      run("Re-running the evidence", async () => {
        // The stored row is re-read so the SQL shown is the one the server
        // holds, not the copy this list was loaded with.
        const [fresh, r] = await Promise.all([
          client.getInsight(companyID, current.id),
          client.verifyInsight(companyID, current.id),
        ]);
        setEvidenceSQL((m) => ({ ...m, [current.id]: fresh.evidence_sql }));
        const msg = r.reproduces
          ? "The books still reproduce every figure, to the paisa."
          : `The books have moved since this was found: ${r.detail ?? ""}`;
        setEvidence((m) => ({ ...m, [current.id]: msg }));
        return msg;
      });
    } else if (k === "s" && current) {
      ev.preventDefault();
      const reason = note.trim();
      if (!reason) {
        setNotice("Suppress needs a reason: type it in the Note field, then press S.");
        return;
      }
      run("Suppressing", async () => {
        await client.suppressInsight(companyID, { type: current.type, reason });
        setNote("");
        await load();
        return `“${current.title}” will not reach the digest until you lift it.`;
      });
    } else if (k === "l") {
      ev.preventDefault();
      const last = suppressions[suppressions.length - 1];
      if (!last) {
        setNotice("No suppression is active.");
        return;
      }
      run("Lifting the suppression", async () => {
        await client.unsuppressInsight(companyID, last.id);
        await load();
        return `Lifted: ${last.type} may reach the digest again.`;
      });
    } else if (k === "d") {
      ev.preventDefault();
      run("Composing today's digest", async () => {
        const d = await client.composeInsightDigest(companyID);
        setDigest(d);
        return `Digest for ${d.digest_date}: ${d.items.length} item(s).`;
      });
    } else if (k === "r") {
      ev.preventDefault();
      run("Running today's detectors", async () => {
        const r = await client.runInsightsNow(companyID);
        await load();
        return r.nightly.already_ran
          ? `Today's night already ran (${r.nightly.as_of}); ${r.events.events} new voucher event(s) processed.`
          : `Ran ${r.nightly.as_of}: ${r.events.events} voucher event(s), ${Object.keys(r.nightly.by_type).length} type(s) with findings.`;
      });
    }
  };

  const onVoucherKey = (ev: React.KeyboardEvent<HTMLUListElement>) => {
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      setVActive((a) => Math.min(a + 1, Math.max(vouchers.length - 1, 0)));
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      setVActive((a) => Math.max(a - 1, 0));
    } else if (ev.key === "Tab") {
      ev.preventDefault();
      listRef.current?.focus();
    }
    // Enter is NOT handled here: the shell's Enter drills the published row.
  };

  useEffect(() => {
    listRef.current?.focus({ preventScroll: true });
  }, [rows === null]);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-1 text-lg font-bold text-tally-title">Insights</h1>
      <p className="mb-3 text-sm">
        What the books say needs attention. ↑/↓ highlight · U useful · N not useful · W wrong ·
        V re-check the evidence · S suppress the type · L lift · D today&apos;s digest · R run now · Tab to
        its vouchers, Enter opens one. Every figure is reproduced from the books by a read-only
        query before it is shown.
      </p>

      {notice ? (
        <p className="mb-2 text-sm text-tally-active" role="status" data-testid="insights-notice">
          {notice}
        </p>
      ) : null}
      {error ? (
        <p className="mb-2 text-sm text-red-400" data-testid="insights-error">
          {error}
        </p>
      ) : null}

      {digest ? (
        <div className="mb-3 max-w-3xl bg-tally-panel p-2 text-sm" data-testid="insights-digest">
          <p className="font-bold">
            Morning digest, {digest.digest_date} ·{" "}
            {digest.narrator === "model"
              ? `narrated by ${digest.model_id || "the model"} (every figure checked against the findings)`
              : "fixed template"}
            {digest.narration_rejected ? ` · model narration refused: ${digest.narration_rejected}` : ""}
          </p>
          <ol className="ml-4 list-decimal">
            {digest.items.map((it) => (
              <li key={it.insight_id} data-testid="digest-item">
                {it.text}
              </li>
            ))}
          </ol>
        </div>
      ) : null}

      {rows === null ? (
        <p data-testid="insights-loading">Loading insights…</p>
      ) : list.length === 0 && !error ? (
        <p data-testid="insights-empty">
          Nothing needs attention. The detectors run on every voucher and nightly after 02:00 IST (R runs them now).
        </p>
      ) : (
        <ul
          ref={listRef}
          role="listbox"
          aria-label="Open insights"
          tabIndex={0}
          onKeyDown={onListKey}
          className="mb-3 max-w-3xl bg-tally-panel p-2 outline-none"
          data-testid="insights-list"
        >
          {list.map((r, i) => (
            <li
              key={r.id}
              role="option"
              aria-selected={i === idx}
              data-testid="insights-row"
              className={i === idx ? "bg-tally-active text-white" : ""}
              onClick={() => setActive(i)}
            >
              {r.title} · {r.entity_name} · Rs {r.impact_inr}
            </li>
          ))}
        </ul>
      )}

      {current ? (
        <div className="max-w-3xl bg-tally-panel p-3 text-sm" data-testid="insights-detail">
          <p className="font-bold">{current.summary}</p>
          <table className="mt-2">
            <tbody>
              {current.fields.map((f) => (
                <tr key={f.key}>
                  <td className="pr-4">{f.label}</td>
                  <td data-testid={`insight-field-${f.key}`}>{f.value}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-2">Action: {current.action}</p>
          <p className="mt-1 text-xs">
            {current.cadence === "realtime" ? "Found when a voucher was saved" : "Found by the nightly run"} ·
            as of {current.as_of} · verdicts so far: {current.verdicts.useful ?? 0} useful,{" "}
            {current.verdicts.not_useful ?? 0} not useful, {current.verdicts.wrong ?? 0} wrong
          </p>
          {evidence[current.id] ? (
            <p className="mt-1" data-testid="insights-evidence">
              {evidence[current.id]}
            </p>
          ) : null}
          {evidenceSQL[current.id] ? (
            <details className="mt-1 text-xs" data-testid="insights-evidence-sql">
              <summary>The read-only query behind these figures</summary>
              <pre className="whitespace-pre-wrap">{evidenceSQL[current.id]}</pre>
            </details>
          ) : null}
          {vouchers.length > 0 ? (
            <ul
              ref={vRef}
              role="listbox"
              aria-label="Vouchers behind this insight"
              tabIndex={0}
              onKeyDown={onVoucherKey}
              className="mt-2 outline-none"
              data-testid="insights-vouchers"
            >
              {vouchers.map((v, i) => (
                <li
                  key={v.voucher_id}
                  role="option"
                  aria-selected={i === vActive}
                  className={i === vActive ? "bg-tally-active text-white" : ""}
                  data-testid="insights-voucher"
                >
                  {v.voucher_date} · {v.class} {v.voucher_no} · {v.party}
                </li>
              ))}
            </ul>
          ) : current.voucher_ids.length === 0 ? (
            <p className="mt-2 text-xs">
              No voucher list: this figure spans the whole book (open the report it names instead).
            </p>
          ) : null}
          <label className="mt-2 block">
            Note (reason for S; optional with a verdict)
            <input
              aria-label="Note"
              data-testid="insights-note"
              className="ml-2 w-96 bg-tally-field px-1 text-tally-fieldText outline-none"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              onKeyDown={(e) => {
                // Typing a note must not trigger a verdict or a drill.
                e.stopPropagation();
              }}
            />
          </label>
        </div>
      ) : null}

      {suppressions.length > 0 ? (
        <p className="mt-3 max-w-3xl text-xs" data-testid="insights-suppressions">
          Suppressed from the digest (L lifts the last):{" "}
          {suppressions.map((x) => `${x.type}${x.entity_key ? ` (${x.entity_key})` : ""} — ${x.reason}`).join("; ")}
        </p>
      ) : null}

      {owed.length > 0 ? (
        <details className="mt-3 max-w-3xl text-xs" data-testid="insights-owed">
          <summary>Not detected by this build ({owed.length})</summary>
          <ul className="ml-4 list-disc">
            {owed.map((o) => (
              <li key={o.type}>
                {o.type}: {o.reason}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}
