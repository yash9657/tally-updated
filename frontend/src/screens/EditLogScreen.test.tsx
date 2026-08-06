import { describe, it, expect, afterEach } from "vitest";
import { useEffect } from "react";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { EditLogScreen } from "./EditLogScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { LoadedVoucherProvider, useLoadedVoucher } from "../app/LoadedVoucher";
import { createMockClient, ApiError } from "../api";
import type {
  CoredClient,
  DayBookEntry,
  VoucherHistoryResponse,
} from "../api";

afterEach(cleanup);

/**
 * The Edit Log viewer. The behaviour worth pinning is the VERIFICATION BANNER,
 * because it is the only part a reader must be able to trust:
 *
 *  - a verified chain says so;
 *  - a BROKEN chain is loud and names the version — never a quiet row list;
 *  - and "nobody checked" is its OWN state. The wire carries `ok: false` for
 *    both a broken chain and an unchecked one (the mock client computes no
 *    hashes), so a screen that treated `!ok` as "tampered" would cry wolf, and
 *    one that treated a missing verification as fine would paint an unverified
 *    log as verified. Both are wrong; `versions_checked` is the discriminator.
 */

const SNAPSHOT = {
  screen: "edit-log" as const,
  screenStackDepth: 3,
  canReturnToVoucher: false,
};

const ENTRY: DayBookEntry = {
  voucher_id: "v-1",
  version_no: 3,
  op: "DELETE",
  // The SERVER's label. This fixture said "SALES" because the in-memory mock
  // wrote `cls.toUpperCase()`; the drill now routes on this field, so an
  // upper-cased slug is a class no screen can be found for.
  class: "Sales",
  voucher_type: "sales",
  voucher_no: "INV/0001",
  date: "2026-04-10",
  narration: "",
  party: "Acme Corp",
  debit: "650.0000",
  credit: "650.0000",
  postings: [],
};

/** Three versions: created at 400, altered to 650, then deleted. */
function history(over: Partial<VoucherHistoryResponse> = {}): VoucherHistoryResponse {
  return {
    voucher_id: "v-1",
    is_deleted: true,
    versions: [
      {
        version_no: 1,
        op: "CREATE",
        created_by: "asha",
        created_at: "2026-04-10T09:30:00Z",
        node_id: "cored-0",
        hash: "aaaa1111",
        prev_hash: "",
        class: "sales",
        voucher_no: "INV/0001",
        voucher_date: "2026-04-10",
        narration: "as first entered",
        party: "Acme Corp",
        postings: [
          { ledger_id: "led-debtor", dr_cr: "D", amount: "400.0000" },
          { ledger_id: "led-sales", dr_cr: "C", amount: "400.0000" },
        ],
      },
      {
        version_no: 2,
        op: "ALTER",
        created_by: "bhaskar",
        created_at: "2026-04-12T11:00:00Z",
        node_id: "cored-0",
        hash: "bbbb2222",
        prev_hash: "aaaa1111",
        class: "sales",
        voucher_no: "INV/0001",
        voucher_date: "2026-04-10",
        narration: "corrected after the client called",
        party: "Acme Corp",
        postings: [
          { ledger_id: "led-debtor", dr_cr: "D", amount: "650.0000" },
          { ledger_id: "led-sales", dr_cr: "C", amount: "650.0000" },
        ],
      },
      {
        version_no: 3,
        op: "DELETE",
        created_by: "asha",
        created_at: "2026-04-20T16:45:00Z",
        node_id: "cored-0",
        hash: "cccc3333",
        prev_hash: "bbbb2222",
        class: "sales",
        voucher_no: "INV/0001",
        voucher_date: "2026-04-10",
        narration: "",
        party: "Acme Corp",
        postings: [],
      },
    ],
    verification: {
      ok: true,
      versions_checked: 3,
      broken_at_version: 0,
      reason: "all 3 version(s) verified: every entry hashes to its record",
    },
    ...over,
  };
}

/** Seed the LoadedVoucher slot the way a Day Book drill does, then render. */
function Seed({ entry }: { entry: DayBookEntry | null }) {
  const { setLoaded } = useLoadedVoucher();
  useEffect(() => {
    setLoaded(entry ? { entry } : null);
  }, [entry, setLoaded]);
  return null;
}

function renderLog(client: CoredClient, entry: DayBookEntry | null = ENTRY) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Demo Traders", homeStateCode: "29" }}
    >
      <LoadedVoucherProvider>
        <Seed entry={entry} />
        <EditLogScreen snapshot={SNAPSHOT} onSelect={() => {}} />
      </LoadedVoucherProvider>
    </CompanyProvider>,
  );
}

function withHistory(h: VoucherHistoryResponse): CoredClient {
  return { ...createMockClient({ seed: false }), getVoucherHistory: async () => h };
}

describe("EditLogScreen", () => {
  it("shows every version with who did it, and what it recorded", async () => {
    renderLog(withHistory(history()));

    await waitFor(() =>
      expect(screen.getByTestId("editlog-version:1")).toBeTruthy(),
    );
    const v1 = screen.getByTestId("editlog-version:1").textContent ?? "";
    expect(v1).toContain("Created by asha");
    expect(v1).toContain("400.0000"); // the amount AS FIRST ENTERED

    const v2 = screen.getByTestId("editlog-version:2").textContent ?? "";
    expect(v2).toContain("Altered by bhaskar");
    expect(v2).toContain("650.0000");

    expect(screen.getByTestId("editlog-version:3").textContent).toContain(
      "Deleted by asha",
    );
  });

  it("says what CHANGED between versions", async () => {
    renderLog(withHistory(history()));
    await waitFor(() =>
      expect(screen.getByTestId("editlog-changes:2")).toBeTruthy(),
    );
    const changed = screen.getByTestId("editlog-changes:2").textContent ?? "";
    expect(changed).toContain("total debit: 400.0000 → 650.0000");
    expect(changed).toContain("narration");
  });

  it("reports a VERIFIED chain", async () => {
    renderLog(withHistory(history()));
    await waitFor(() =>
      expect(screen.getByTestId("editlog-verification")).toBeTruthy(),
    );
    const banner = screen.getByTestId("editlog-verification").textContent ?? "";
    expect(banner).toContain("Chain verified");
    expect(banner).not.toMatch(/TAMPERED|Not verified/);
  });

  it("is LOUD about a broken chain and names the version", async () => {
    renderLog(
      withHistory(
        history({
          verification: {
            ok: false,
            versions_checked: 3,
            broken_at_version: 2,
            reason: "version 2 has been altered: its stored contents hash to …",
          },
        }),
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("editlog-verification").textContent).toContain(
        "TAMPERED",
      ),
    );
    const banner = screen.getByTestId("editlog-verification");
    expect(banner.textContent).toContain("version 2");
    // ...and it explains that reaching this state required database access,
    // so it is escalated rather than shrugged at.
    expect(banner.textContent).toContain("append-only");
    expect(banner.className).toContain("red");
  });

  it("does NOT paint an UNCHECKED log as verified — or as tampered", async () => {
    // What the mock client returns: ok=false because it hashes nothing. Calling
    // that "TAMPERED" would cry wolf on every hermetic run; calling it verified
    // would assert the one property this feature exists to prove.
    renderLog(
      withHistory(
        history({
          verification: {
            ok: false,
            versions_checked: 0,
            broken_at_version: 0,
            reason: "mock client: no hash chain is computed in memory",
          },
        }),
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("editlog-verification")).toBeTruthy(),
    );
    const banner = screen.getByTestId("editlog-verification");
    expect(banner.textContent).toContain("Not verified");
    expect(banner.textContent).not.toContain("TAMPERED");
    expect(banner.textContent).not.toContain("Chain verified");
    expect(banner.className).not.toContain("red");
  });

  it("shows a DELETED voucher's history — the deletion does not erase it", async () => {
    renderLog(withHistory(history()));
    await waitFor(() =>
      expect(screen.getByTestId("editlog-deleted")).toBeTruthy(),
    );
    expect(screen.getByTestId("editlog-deleted").textContent).toContain(
      "history survives the deletion",
    );
    // ...and the tombstone is itself an entry.
    expect(screen.getByTestId("editlog-version:3").textContent).toContain(
      "Deleted",
    );
  });

  it("says so when no voucher is loaded, instead of an empty screen", async () => {
    renderLog(withHistory(history()), null);
    await waitFor(() =>
      expect(screen.getByTestId("editlog-no-voucher")).toBeTruthy(),
    );
    expect(screen.getByTestId("editlog-no-voucher").textContent).toContain(
      "Alt+L",
    );
  });

  it("explains a 403 rather than rendering blank", async () => {
    const base = createMockClient({ seed: false });
    const client: CoredClient = {
      ...base,
      getVoucherHistory: async () => {
        throw new ApiError(403, "default access: none", "forbidden");
      },
    };
    renderLog(client);
    await waitFor(() =>
      expect(screen.getByTestId("editlog-error").textContent).toContain(
        "Not permitted for your security level (403)",
      ),
    );
  });

  it("the MOCK client records a real history rather than fabricating one", async () => {
    // The mock restates a voucher in place, so without an explicit log there
    // would be nothing to show. Post -> alter -> and the log must have both,
    // with the amounts each version actually carried.
    const client = createMockClient({ seed: false });
    const led1 = (await client.createLedger("co-1", { name: "Debtor A", group_name: "Sundry Debtors" })).ledger_id;
    const led2 = (await client.createLedger("co-1", { name: "Sales A", group_name: "Sales Accounts" })).ledger_id;
    const posted = await client.postSalesVoucher("co-1", {
      voucher_date: "2026-04-10",
      created_by: "asha",
      postings: [
        { ledger_id: led1, dr_cr: "D", amount: "400.0000" },
        { ledger_id: led2, dr_cr: "C", amount: "400.0000" },
      ],
    });
    await client.alterSalesVoucher("co-1", posted.voucher_id, {
      voucher_date: "2026-04-10",
      created_by: "bhaskar",
      postings: [
        { ledger_id: led1, dr_cr: "D", amount: "650.0000" },
        { ledger_id: led2, dr_cr: "C", amount: "650.0000" },
      ],
    });

    const h = await client.getVoucherHistory("co-1", posted.voucher_id);
    expect(h.versions.map((v) => v.op)).toEqual(["CREATE", "ALTER"]);
    expect(h.versions[0]?.created_by).toBe("asha");
    expect(h.versions[0]?.postings[0]?.amount).toBe("400.0000");
    expect(h.versions[1]?.postings[0]?.amount).toBe("650.0000");
    // And it does NOT claim a verification it never performed.
    expect(h.verification.ok).toBe(false);
    expect(h.verification.versions_checked).toBe(0);
  });
});
