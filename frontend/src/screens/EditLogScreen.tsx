import { useEffect, useState } from "react";
import { ApiError } from "../api";
import type { VoucherHistoryResponse, VoucherVersionView } from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { useLoadedVoucher } from "../app/LoadedVoucher";
import type { ScreenComponentProps } from "../app/screenRegistry";

/**
 * THE EDIT LOG (Alt+L on a voucher in alteration mode).
 *
 * Every create, alter and delete of one logical voucher, oldest first, with who
 * did it and when — and the server's verification of the BLAKE3 hash chain that
 * pins them. This is the MCA audit-trail requirement (RESEARCH.md sec 6) made
 * VISIBLE: the log has existed since migration 0002 and nothing could read it.
 *
 * THREE RULES THIS SCREEN FOLLOWS:
 *
 *  1. **The verification is not optional decoration.** A list of versions
 *     without it is a claim; with it, it is evidence. So the banner renders
 *     first, states the outcome in words, and an UNVERIFIED result is loud —
 *     naming the version at which the chain fails. A viewer that showed the
 *     rows and quietly dropped a failed check would be worse than no viewer.
 *
 *  2. **A verification this client did not get is NOT a pass.** `ok: false`
 *     covers both "the chain is broken" and "nobody checked" (the mock client
 *     computes no hashes), and those read differently — `versions_checked: 0`
 *     is the discriminator. Neither is ever painted as verified.
 *
 *  3. **What changed between versions is computed here, not asserted by the
 *     server.** The diff is derived from the recorded payloads, which are the
 *     bytes the hashes cover — so the "what changed" line cannot disagree with
 *     what was signed.
 */

/** Total of a version's postings on one side, as a fixed-4 string. */
function sideTotal(v: VoucherVersionView, side: "D" | "C"): string {
  const paise = v.postings
    .filter((p) => p.dr_cr.toUpperCase().startsWith(side))
    .reduce((sum, p) => sum + Math.round(parseFloat(p.amount || "0") * 10000), 0);
  return (paise / 10000).toFixed(4);
}

/**
 * What changed from the previous version, in words. Compares only the fields a
 * reader cares about; an ALTER that changed nothing visible says so rather than
 * rendering an empty list (which would read as a rendering bug).
 */
function changesFrom(
  prev: VoucherVersionView | undefined,
  cur: VoucherVersionView,
): string[] {
  if (!prev) return [];
  const out: string[] = [];
  const cmp = (label: string, a: string, b: string) => {
    if (a !== b) out.push(`${label}: ${a || "(empty)"} → ${b || "(empty)"}`);
  };
  cmp("date", prev.voucher_date, cur.voucher_date);
  cmp("number", prev.voucher_no, cur.voucher_no);
  cmp("party", prev.party, cur.party);
  cmp("narration", prev.narration, cur.narration);
  cmp("total debit", sideTotal(prev, "D"), sideTotal(cur, "D"));
  cmp("total credit", sideTotal(prev, "C"), sideTotal(cur, "C"));
  if (prev.postings.length !== cur.postings.length) {
    out.push(`lines: ${prev.postings.length} → ${cur.postings.length}`);
  }
  return out;
}

const OP_LABEL: Record<string, string> = {
  CREATE: "Created",
  ALTER: "Altered",
  DELETE: "Deleted",
};

export function EditLogScreen(_props: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const { loaded } = useLoadedVoucher();
  const voucherID = loaded?.entry.voucher_id ?? "";

  const [data, setData] = useState<VoucherHistoryResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let live = true;
    if (!voucherID) {
      setLoading(false);
      return;
    }
    setLoading(true);
    client
      .getVoucherHistory(companyID, voucherID)
      .then((h) => {
        if (live) {
          setData(h);
          setError(null);
        }
      })
      .catch((e: unknown) => {
        if (!live) return;
        if (e instanceof ApiError) {
          setError(
            e.status === 403
              ? `Not permitted for your security level (403). ${e.message}`
              : `${e.status}: ${e.message}`,
          );
        } else {
          setError(e instanceof Error ? e.message : String(e));
        }
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [client, companyID, voucherID]);

  const v = data?.verification;
  // THREE distinct states, never collapsed into two: verified, BROKEN, and
  // "nobody checked". Painting the third as either of the others is the lie
  // this screen most needs to avoid.
  const checked = (v?.versions_checked ?? 0) > 0;
  const state: "verified" | "broken" | "unchecked" = !v
    ? "unchecked"
    : v.ok
      ? "verified"
      : checked
        ? "broken"
        : "unchecked";

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-1 text-lg font-bold text-tally-title">Edit Log</h1>
      <p className="mb-3 text-sm">
        Every version of this voucher, oldest first. Entries can never be
        changed or removed — the log is append-only and hash-chained. Esc to go
        back.
      </p>

      {!voucherID ? (
        <p data-testid="editlog-no-voucher" className="text-sm">
          No voucher is loaded. Open a voucher from the Day Book (Enter on a
          row), then press Alt+L.
        </p>
      ) : loading ? (
        <p data-testid="editlog-loading" className="text-sm">
          Loading the edit log…
        </p>
      ) : error ? (
        <p data-testid="editlog-error" className="text-sm text-red-400">
          {error}
        </p>
      ) : data ? (
        <>
          <div
            data-testid="editlog-verification"
            className={
              "mb-3 max-w-3xl p-3 text-sm " +
              (state === "verified"
                ? "bg-tally-panel text-tally-title"
                : state === "broken"
                  ? "bg-red-900 text-white"
                  : "bg-tally-panel")
            }
          >
            <strong>
              {state === "verified"
                ? "✓ Chain verified"
                : state === "broken"
                  ? `✗ TAMPERED — the chain fails at version ${v?.broken_at_version}`
                  : "? Not verified"}
            </strong>
            <div>{v?.reason}</div>
            {state === "broken" ? (
              <div className="mt-1">
                This record no longer matches what was signed when it was
                written. Changing it required defeating the append-only
                guard — i.e. direct database access. Treat the affected
                version as untrustworthy and escalate.
              </div>
            ) : null}
          </div>

          {data.is_deleted ? (
            <p data-testid="editlog-deleted" className="mb-3 max-w-3xl text-sm">
              This voucher has been DELETED. Its history survives the deletion —
              that is the point of an edit log.
            </p>
          ) : null}

          <ol data-testid="editlog-versions" className="max-w-3xl">
            {data.versions.map((ver, i) => {
              const changes = changesFrom(data.versions[i - 1], ver);
              return (
                <li
                  key={ver.version_no}
                  data-testid={`editlog-version:${ver.version_no}`}
                  className="mb-3 bg-tally-panel p-3 text-sm"
                >
                  <div className="text-tally-title">
                    v{ver.version_no} · {OP_LABEL[ver.op] ?? ver.op}
                    {ver.created_by ? ` by ${ver.created_by}` : ""} ·{" "}
                    {ver.created_at.slice(0, 19).replace("T", " ")} UTC
                  </div>
                  <div>
                    {ver.class} {ver.voucher_no ? `#${ver.voucher_no}` : ""}
                    {ver.voucher_date ? ` dated ${ver.voucher_date}` : ""}
                    {ver.party ? ` · ${ver.party}` : ""}
                  </div>
                  {ver.narration ? <div>“{ver.narration}”</div> : null}
                  {ver.postings.length > 0 ? (
                    <table className="mt-1 text-xs">
                      <tbody>
                        {ver.postings.map((p, pi) => (
                          <tr key={pi}>
                            <td className="pr-3">
                              {p.dr_cr.toUpperCase().startsWith("D")
                                ? "Dr"
                                : "Cr"}
                            </td>
                            <td className="pr-3">{p.ledger_id}</td>
                            <td className="text-right">{p.amount}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  ) : null}
                  {changes.length > 0 ? (
                    <div
                      data-testid={`editlog-changes:${ver.version_no}`}
                      className="mt-1 text-xs"
                    >
                      Changed from v{ver.version_no - 1}: {changes.join("; ")}
                    </div>
                  ) : i > 0 && ver.op === "ALTER" ? (
                    <div
                      data-testid={`editlog-changes:${ver.version_no}`}
                      className="mt-1 text-xs"
                    >
                      Changed from v{ver.version_no - 1}: nothing visible on
                      this screen changed.
                    </div>
                  ) : null}
                  <div className="mt-1 break-all text-xs opacity-70">
                    hash {ver.hash || "(not recorded by this client)"}
                  </div>
                </li>
              );
            })}
          </ol>
        </>
      ) : null}
    </section>
  );
}
