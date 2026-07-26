import { useCallback, useEffect, useMemo, useState } from "react";
import type { MeCompany } from "../api";
import { useClient, useCompanyContext } from "../app/CompanyContext";
import { COMPANY_SWITCHED_SENTINEL } from "../keystroke/navMachine";
import type { ScreenComponentProps } from "../app/screenRegistry";
import { SelectionList } from "./SelectionList";

/**
 * F3 — Switch Company.
 *
 * Lists the companies THIS USER may open, from `GET /v1/me` — the server's
 * membership list, not a client-side guess. Each row shows the security level
 * the user holds there, because the same person is routinely an Owner of their
 * own books and a Data Entry operator on a client's.
 *
 * Selecting a company:
 *   1. re-reads that company's master (getCompany) so `homeStateCode` is the
 *      SERVER's value — hydrating it wrongly would make the browser derive the
 *      wrong intra/inter-state GST split and the server would 422 the post;
 *   2. calls setCompany, which re-scopes every screen (the company id is the
 *      tenant scope in every route);
 *   3. fires COMPANY_SWITCHED_SENTINEL, which unwinds the machine to the
 *      Gateway — the frames underneath were rendered against the OLD company's
 *      books, and silently re-pointing them is how an entry lands in the wrong
 *      company.
 *
 * Failure is LOUD and non-destructive: if step 1 fails, the session stays on
 * the current company rather than switching to a half-hydrated one.
 */
export function CompanySelectScreen({ onSelect }: ScreenComponentProps) {
  const client = useClient();
  const { company, setCompany } = useCompanyContext();

  const [companies, setCompanies] = useState<MeCompany[]>([]);
  const [subject, setSubject] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    (async () => {
      try {
        const me = await client.getMe();
        if (!live) return;
        setCompanies(me.companies);
        setSubject(me.subject);
      } catch (e) {
        if (live) setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
    };
  }, [client]);

  // Row labels must be UNIQUE: SelectionList selects by the option string, so
  // two companies with the same name (a real possibility across clients) would
  // otherwise collide and one would be unselectable.
  const rows = useMemo(() => {
    const seen = new Map<string, number>();
    return companies.map((c) => {
      const base = `${c.name} · ${c.security_level_name}${
        c.company_id === company?.id ? " · (current)" : ""
      }`;
      const n = seen.get(base) ?? 0;
      seen.set(base, n + 1);
      return {
        label: n === 0 ? base : `${base} · ${c.company_id.slice(0, 8)}`,
        id: c.company_id,
        name: c.name,
      };
    });
  }, [companies, company?.id]);

  const open = useCallback(
    async (label: string) => {
      const row = rows.find((r) => r.label === label);
      if (!row) return;
      if (row.id === company?.id) {
        // Already here: leave the picker without a pointless round trip.
        onSelect(COMPANY_SWITCHED_SENTINEL);
        return;
      }
      setError(null);
      try {
        const view = await client.getCompany(row.id);
        setCompany({
          id: view.id || row.id,
          name: view.name || row.name,
          // Verbatim, including "": a company with no registered state derives
          // inter-state on BOTH sides, so client and server agree.
          homeStateCode: view.state_code,
        });
        onSelect(COMPANY_SWITCHED_SENTINEL);
      } catch (e) {
        // Do NOT switch on a failed hydrate — a company whose home state is
        // unknown would derive the wrong GST split in the browser.
        setError(
          `Could not open ${row.name}: ${e instanceof Error ? e.message : String(e)}`,
        );
      }
    },
    [rows, company?.id, client, setCompany, onSelect],
  );

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-2 text-lg font-bold text-tally-title">
        Select Company (F3)
      </h1>
      <p className="mb-3 text-sm">
        Companies you are a member of{subject ? `, as ${subject}` : ""}. Selecting
        one re-scopes the whole session and returns to the Gateway. Esc to stay
        where you are.
      </p>

      {error ? (
        <p data-testid="company-select-error" className="mb-2 text-sm text-red-400">
          {error}
        </p>
      ) : null}

      <div className="max-w-xl bg-tally-panel p-3">
        {loading ? (
          <p data-testid="company-select-loading" className="text-sm">
            Loading companies…
          </p>
        ) : rows.length === 0 ? (
          <p data-testid="company-select-empty" className="text-sm">
            You are not a member of any company. Ask an Owner to invite this
            subject, then press F3 again.
          </p>
        ) : (
          <SelectionList
            label="List of Companies"
            options={rows.map((r) => r.label)}
            onSelect={(label) => void open(label)}
            autoFocus
          />
        )}
      </div>
    </section>
  );
}
