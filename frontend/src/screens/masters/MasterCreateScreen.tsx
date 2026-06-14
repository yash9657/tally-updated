import { useCallback, useRef, useState } from "react";
import { SelectionList } from "../SelectionList";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { MasterKind } from "../../keystroke/types";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { LEDGER_PARENT_GROUPS, GROUP_PARENT_OPTIONS } from "./groups";

/**
 * Inline / standalone master-create screen, wired to the LIVE backend. Reached
 * via Alt+C from a voucher field (inline) or a Gateway create-master menu
 * (standalone). On Ctrl+A it calls the kind-correct CoredClient create method,
 * and — so the originating voucher field pre-fills with the new master — sets
 * the machine's pending selection (detailOf) to the created name via onSelect
 * BEFORE the accept transition runs (the AppShell fires this handler first).
 *
 * The created master's id is surfaced via onCreated so the voucher screen can
 * reference party_ledger_id / posting ledger_id by id (the masters agent's
 * consumer wires that; here we publish it on the window-free callback).
 */

const PARENT_OPTIONS: Record<MasterKind, readonly string[]> = {
  ledger: LEDGER_PARENT_GROUPS,
  group: GROUP_PARENT_OPTIONS,
  "voucher-type": ["Sales", "Purchase", "Receipt", "Payment", "Contra", "Journal"],
  unit: [],
  "stock-item": [],
};

export function MasterCreateScreen({
  snapshot,
  onSelect,
  onCreated,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
  /** Optional sink for the created master's id (voucher field id wiring). */
  onCreated?: (result: { name: string; id: string }) => void;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const kind = (snapshot.masterKind ?? "ledger") as MasterKind;

  const [name, setName] = useState("");
  const [parent, setParent] = useState("");
  const [status, setStatus] = useState<string | null>(null);

  const nameRef = useRef(name);
  nameRef.current = name;
  const parentRef = useRef(parent);
  parentRef.current = parent;

  // Surface the typed/selected master name to the machine as the pending pick so
  // the accept reducer can return it to the originating voucher field.
  const publishName = useCallback(
    (n: string) => {
      if (n) onSelect(n);
    },
    [onSelect],
  );

  const create = useCallback(
    async (n: string, p: string): Promise<{ id: string } | null> => {
      switch (kind) {
        case "ledger": {
          const r = await client.createLedger(companyID, {
            name: n,
            group_name: p,
          });
          return { id: r.ledger_id };
        }
        case "group": {
          const r = await client.createGroup(companyID, {
            name: n,
            parent_name: p || undefined,
          });
          return { id: r.group_id };
        }
        case "voucher-type": {
          const r = await client.createVoucherType(companyID, {
            name: n,
            parent_class: p || "Sales",
          });
          return { id: r.voucher_type_id };
        }
        case "unit": {
          const r = await client.createUnit(companyID, { symbol: n });
          return { id: r.unit_id };
        }
        case "stock-item": {
          const r = await client.createStockItem(companyID, {
            name: n,
            unit_symbol: p || "Nos",
          });
          return { id: r.stock_item_id };
        }
        default:
          return null;
      }
    },
    [client, companyID, kind],
  );

  const onAccept = useCallback(() => {
    const n = nameRef.current.trim();
    const p = parentRef.current.trim();
    if (!n) {
      setStatus("Name is required");
      return;
    }
    // Set the machine's pending pick synchronously so the accept reducer (which
    // runs immediately after this handler) returns it to the parent voucher.
    onSelect(n);
    create(n, p)
      .then((res) => {
        setStatus(`Created ${n}`);
        if (res && onCreated) onCreated({ name: n, id: res.id });
      })
      .catch((err: unknown) => {
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      });
  }, [create, onSelect, onCreated]);

  useRegisterScreenAction("accept", onAccept);

  const parentOptions = PARENT_OPTIONS[kind];

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        Create {kind.replace("-", " ")}
      </h1>
      <div className="mt-3 max-w-md bg-tally-panel p-3">
        <label className="block text-tally-field">
          Name
          <input
            aria-label="Name"
            className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
            onBlur={() => publishName(nameRef.current)}
            autoFocus={snapshot.field === "name"}
          />
        </label>
        {parentOptions.length > 0 ? (
          <div className="mt-2">
            <SelectionList
              label={kind === "group" ? "Under (parent)" : "Under (group)"}
              options={parentOptions}
              onSelect={(item) => setParent(item)}
              autoFocus={snapshot.field === "group"}
            />
          </div>
        ) : null}
        {parent ? (
          <p className="mt-1 text-xs">Under: {parent}</p>
        ) : null}
        {status ? (
          <p className="mt-2 text-xs text-tally-active" role="status">
            {status}
          </p>
        ) : null}
        <p className="mt-2 text-xs">Ctrl+A to accept and return; Esc to cancel.</p>
      </div>
    </section>
  );
}
