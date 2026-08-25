import { useCallback, useEffect, useRef, useState } from "react";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import type { ScreenComponentProps } from "../../app/screenRegistry";
import { ApiError } from "../../api";
import type {
  EmployeeView,
  PayrollVocabularyResponse,
  SalaryComponentView,
  StatutoryElectionView,
} from "../../api";
import { EMPLOYEE_ROW_PREFIX } from "../../keystroke/navMachine";
import { formatMoney } from "../reports/money";
import { usePrintAction } from "../usePrint";

/**
 * THE EMPLOYEE MASTER (Track 3) — and the two effective-dated children that are
 * the difference between a record and a payable person.
 *
 * ===========================================================================
 * ⛔ WHY ALT+S IS ON THIS SCREEN AND NOT ON ONE OF ITS OWN
 * ===========================================================================
 * An employee created with `POST /employees` and nothing else CANNOT BE PAID.
 * cored refuses the whole pay run — not that person's slip, the WHOLE run — with
 *
 *	"no salary structure is in force on <date>; there is nothing to pay and
 *	 nothing to contribute on"
 *	"no statutory election is in force for wage month <month>; the PF
 *	 contribution base and the tax regime are elections and there is no safe
 *	 default for the first of them"
 *
 * and that refusal arrives on the RUN — days later, at the one moment an
 * operator cannot afford to be blocked. Until this wave neither child had an
 * HTTP route at all, so the refusal was unavoidable from any client; the routes
 * exist now (`handler_payroll_structure.go`), and putting them on a screen
 * nobody is required to visit would leave the same failure a click further away.
 *
 * So the list says, per person, whether they can be paid — and Alt+S is what
 * fixes it. The check costs one extra pair of requests per employee, which is
 * stated below rather than hidden.
 *
 * ===========================================================================
 * WHAT THIS SCREEN WILL NOT DO
 * ===========================================================================
 *  - It does not compute anything. Every amount is a fixed-4 string rendered as
 *    received, and the `is_wages` flag beside a component is the SERVER'S
 *    classification (Code on Social Security s.2(88)), never a lookup done here.
 *  - It does not offer a component type of its own. The vocabulary is FETCHED —
 *    a form built from a hardcoded list can offer a type the server refuses, and
 *    `is_wages` decides whether PF is contributed at all.
 *  - It does not default the PF base or pre-select a tax regime. CEILING and
 *    ACTUAL differ for anybody paid above the EPF wage ceiling, and an unstated
 *    regime is "no election" — a different fact from electing the default, which
 *    the server stores as given precisely so the two stay distinguishable.
 *  - It does not hide the identifiers. PAN, UAN, the ESIC IP number and the bank
 *    IFSC are on the master because an operator who may reach it must be able to
 *    correct a wrong one — a malformed PAN is a Form 138 record the department
 *    rejects. WHO may reach it is the ACL's decision, and migration 0035 refuses
 *    the `employee` token to the seeded Data Entry level by default, which is why
 *    the 403 panel below is a NORMAL answer and is rendered as the decision it is.
 */

/** How many employees this screen will check the payability of. See the header. */
const PAYABILITY_CHECK_LIMIT = 200;

/** One employee's two children, once fetched. */
interface EmployeePay {
  components: SalaryComponentView[];
  elections: StatutoryElectionView[];
}

export function EmployeesScreen({ snapshot, onSelect }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const [employees, setEmployees] = useState<EmployeeView[] | null>(null);
  const [includeInactive, setIncludeInactive] = useState(false);
  const [pay, setPay] = useState<Map<string, EmployeePay>>(new Map());
  /**
   * True when the list was too long to check. NOT the same as "everybody is
   * payable", and rendered as its own sentence — an unchecked list shown as a
   * clean one is the failure this whole screen exists to prevent.
   */
  const [payabilityUnchecked, setPayabilityUnchecked] = useState(false);
  const [denied, setDenied] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [vocabulary, setVocabulary] = useState<PayrollVocabularyResponse | null>(
    null,
  );

  /** Which panel is open: the create form, or the pay panel for one employee. */
  const [creating, setCreating] = useState(false);
  const [payFor, setPayFor] = useState<EmployeeView | null>(null);

  const highlighted = (snapshot.selectedItem ?? "").startsWith(
    EMPLOYEE_ROW_PREFIX,
  )
    ? (snapshot.selectedItem ?? "").slice(EMPLOYEE_ROW_PREFIX.length)
    : "";

  // ---- The list, and the payability check.
  const load = useCallback(async () => {
    setBusy(true);
    try {
      const res = await client.listEmployees(companyID, includeInactive);
      setEmployees(res.employees);
      setDenied(false);
      setError("");

      if (res.employees.length > PAYABILITY_CHECK_LIMIT) {
        // Say so rather than checking a prefix and letting the rest read as
        // fine. A partial check rendered as a complete one is worse than none.
        setPayabilityUnchecked(true);
        setPay(new Map());
        return;
      }
      setPayabilityUnchecked(false);
      // ⚠️ TWO REQUESTS PER EMPLOYEE. There is no bulk route, and the honest
      // alternative — not checking — means the list cannot tell an operator who
      // cannot be paid until the pay run refuses the whole month. `allSettled`
      // so ONE employee whose children fail to load does not blank the column
      // for everybody else.
      const settled = await Promise.allSettled(
        res.employees.map(async (e) => {
          const [components, elections] = await Promise.all([
            client.listSalaryComponents(companyID, e.id),
            client.listStatutoryElections(companyID, e.id),
          ]);
          return [
            e.id,
            {
              components: components.salary_components,
              elections: elections.statutory_elections,
            },
          ] as const;
        }),
      );
      const next = new Map<string, EmployeePay>();
      for (const s of settled) {
        if (s.status === "fulfilled") next.set(s.value[0], s.value[1]);
      }
      setPay(next);
    } catch (e: unknown) {
      setEmployees(null);
      if (e instanceof ApiError && e.status === 403) {
        setDenied(true);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [client, companyID, includeInactive]);

  useEffect(() => {
    void load();
  }, [load]);

  // The vocabulary. Fetched once — it is the statute's and not the tenant's, so
  // it does not change with the list. A failure here is NOT fatal to the screen:
  // the master is still readable and creatable; only the pay panel needs it, and
  // that panel says so rather than offering a picker built from a guess.
  useEffect(() => {
    let live = true;
    client
      .payrollVocabulary(companyID)
      .then((v) => {
        if (live) setVocabulary(v);
      })
      .catch(() => {
        if (live) setVocabulary(null);
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  // Ctrl+P. Registered BEFORE any conditional return — a hook may not be called
  // conditionally, and a 403 must not change this component's hook count.
  // printTarget answers it with a sentence: a list of people is not a report.
  usePrintAction();

  // ---- Alt+C: the create form.
  useRegisterScreenAction(
    "createEmployee",
    useCallback((): string => {
      setPayFor(null);
      setCreating(true);
      setNotice("");
      return "Create an employee.";
    }, []),
  );

  // ---- Alt+S: set the highlighted employee's pay.
  useRegisterScreenAction(
    "setEmployeePay",
    useCallback((): string => {
      const match = employees?.find((e) => e.code === highlighted);
      if (!match) {
        // Refuses BEFORE opening rather than opening onto nothing — the same
        // rule Alt+O on the reconciliation screen follows. A pay panel with no
        // employee could only ask which one, and the list is right there.
        return "Highlight an employee first — a salary structure and a statutory election always belong to one person, and setting them against the wrong one is somebody else's pay.";
      }
      setCreating(false);
      setPayFor(match);
      setNotice("");
      return `Set ${match.code} ${match.name}'s salary and statutory election.`;
    }, [employees, highlighted]),
  );

  const firstFieldRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (creating || payFor) firstFieldRef.current?.focus();
  }, [creating, payFor]);

  if (denied) {
    return (
      <section
        data-testid="employees-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">Employees</h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read or create this company&apos;s
          employees. An employee record carries a PAN, a UAN, an ESIC IP number
          and a bank IFSC, so it is served under its own{" "}
          <span className="text-tally-title">employee</span> permission rather
          than the general master one — and it is refused to the Data Entry level
          by default. That is a decision, not a fault. An owner can grant it by
          adding an <span className="text-tally-title">allow employee</span> rule
          in Users &amp; Roles.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Employees</h1>
      <p className="mt-1 max-w-3xl text-xs">
        The people on the payroll. ⛔ An employee is not payable until they have
        BOTH a salary structure and a statutory election — without either, the
        whole pay run is refused, not just their payslip.{" "}
        <strong>Alt+C</strong> creates a person; <strong>Alt+S</strong> sets the
        pay of the one highlighted below.
      </p>

      <div className="mt-3 flex flex-wrap items-center gap-3 text-xs">
        <label className="flex items-center gap-1">
          <input
            type="checkbox"
            data-testid="employees-include-inactive"
            checked={includeInactive}
            onChange={(e) => setIncludeInactive(e.target.checked)}
          />
          {/* "Left the company" and not "inactive": an operator reads the second
              as a setting and the first as a fact about a person. */}
          Show people who have left
        </label>
        {busy ? <span>working…</span> : null}
      </div>

      {error !== "" ? (
        <p
          data-testid="employees-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {error}
        </p>
      ) : null}

      {notice !== "" ? (
        <p
          data-testid="employees-notice"
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      {creating ? (
        <EmployeeForm
          firstFieldRef={firstFieldRef}
          busy={busy}
          onCancel={() => setCreating(false)}
          onSubmit={async (req) => {
            setBusy(true);
            try {
              const res = await client.createEmployee(companyID, req);
              setCreating(false);
              // ⛔ THE CONFIRMATION SAYS THE RECORD IS NOT FINISHED. A "created"
              // message on its own is true and misleading: the operator leaves
              // believing they have added somebody to the payroll, and finds out
              // otherwise when the pay run refuses the whole month.
              setNotice(
                `${req.code} ${req.name} created. They CANNOT BE PAID YET — highlight them and press Alt+S to set a salary structure and a statutory election. Until both exist, a pay run for any month is refused outright.`,
              );
              await load();
              return res.employee_id;
            } finally {
              setBusy(false);
            }
          }}
          onError={(message, isDenied) => {
            setError(message);
            if (isDenied) setDenied(true);
          }}
        />
      ) : null}

      {payFor ? (
        <EmployeePayPanel
          employee={payFor}
          vocabulary={vocabulary}
          existing={pay.get(payFor.id)}
          firstFieldRef={firstFieldRef}
          busy={busy}
          onClose={() => setPayFor(null)}
          onSaved={async (message) => {
            setNotice(message);
            await load();
          }}
          onError={(message) => setError(message)}
          client={client}
          companyID={companyID}
        />
      ) : null}

      {payabilityUnchecked ? (
        <p
          data-testid="employees-payability-unchecked"
          className="mt-3 max-w-3xl border border-tally-active bg-tally-panel p-2 text-sm"
        >
          This company has more than {PAYABILITY_CHECK_LIMIT} employees, so the
          &ldquo;can be paid&rdquo; column has NOT been checked — it would be two
          more requests per person. Nothing below says anybody is payable or
          unpayable; it says nothing at all. Alt+S still shows one person&apos;s
          salary structure and election.
        </p>
      ) : null}

      {employees === null ? (
        <p className="mt-4 text-sm">Loading…</p>
      ) : employees.length === 0 ? (
        <p data-testid="employees-empty" className="mt-4 max-w-3xl text-sm">
          {includeInactive
            ? "This company has no employees at all."
            : "This company has no employees on the payroll. (People who have left are hidden — tick the box above to see them.) Press Alt+C to add one."}
        </p>
      ) : (
        <table className="mt-4 w-full text-sm" data-testid="employees-table">
          <thead>
            <tr className="border-b border-tally-panel text-left text-xs">
              <th className="py-1 pr-2">Code</th>
              <th className="py-1 pr-2">Name</th>
              <th className="py-1 pr-2">Designation</th>
              <th className="py-1 pr-2">Joined</th>
              <th className="py-1 pr-2">Left</th>
              <th className="py-1 pr-2">PAN</th>
              <th className="py-1 pr-2">UAN</th>
              <th className="py-1 pr-2">ESIC IP</th>
              <th className="py-1 pr-2">Can be paid?</th>
            </tr>
          </thead>
          <tbody>
            {employees.map((e) => {
              const p = pay.get(e.id);
              const rowKey = `${EMPLOYEE_ROW_PREFIX}${e.code}`;
              return (
                <tr
                  key={e.id}
                  data-testid={`employee-row:${e.code}`}
                  className={
                    highlighted === e.code
                      ? "bg-tally-panel"
                      : e.is_active
                        ? undefined
                        : "opacity-60"
                  }
                  onClick={() => onSelect(rowKey)}
                >
                  <td className="py-0.5 pr-2">{e.code}</td>
                  <td className="py-0.5 pr-2">{e.name}</td>
                  <td className="py-0.5 pr-2">{e.designation || "—"}</td>
                  <td className="py-0.5 pr-2">{e.doj || "—"}</td>
                  {/* An empty date of leaving means STILL EMPLOYED, which is the
                      usual case — rendered as a dash and never as a blank cell
                      beside a labelled column. */}
                  <td className="py-0.5 pr-2">{e.dol || "—"}</td>
                  {/* "not recorded" rather than a blank: a blank under a labelled
                      column reads as "this person has no PAN", which is a claim. */}
                  <td className="py-0.5 pr-2">{e.pan || "not recorded"}</td>
                  <td className="py-0.5 pr-2">{e.uan || "not recorded"}</td>
                  <td className="py-0.5 pr-2">
                    {e.esic_ip_number || "not recorded"}
                  </td>
                  <td className="py-0.5 pr-2">
                    <PayabilityCell
                      code={e.code}
                      pay={p}
                      unchecked={payabilityUnchecked}
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      <p className="mt-4 max-w-3xl text-xs">
        The identifiers above are shown because a wrong one is a rejected
        statutory filing and somebody has to be able to correct it — a malformed
        PAN is a Form 138 deductee record the department refuses, and a missing
        UAN stops that person&apos;s whole ECR line. Only the last four digits of
        a bank account are ever stored.
      </p>
    </section>
  );
}

/**
 * The "can be paid?" cell.
 *
 * ⛔ IT NAMES WHICH HALF IS MISSING. "Not payable" alone sends an operator to
 * look at both, and the two are set in different parts of the same panel.
 */
function PayabilityCell({
  code,
  pay,
  unchecked,
}: {
  code: string;
  pay: EmployeePay | undefined;
  unchecked: boolean;
}) {
  if (unchecked) return <span className="text-xs">not checked</span>;
  if (!pay) {
    // The children could not be read for THIS person while others were fine.
    // Reported as unknown, never as "no" — an operator who deletes and re-creates
    // an employee over a transient read failure has lost their history.
    return (
      <span data-testid={`employee-payable-unknown:${code}`} className="text-xs">
        could not check
      </span>
    );
  }
  const missing: string[] = [];
  if (pay.components.length === 0) missing.push("a salary structure");
  if (pay.elections.length === 0) missing.push("a statutory election");
  if (missing.length === 0) {
    return (
      <span data-testid={`employee-payable:${code}`} className="text-xs">
        yes
      </span>
    );
  }
  return (
    <span
      data-testid={`employee-unpayable:${code}`}
      className="text-xs font-bold"
    >
      NO — needs {missing.join(" and ")} (Alt+S)
    </span>
  );
}

/**
 * The create form (Alt+C).
 *
 * ⛔ `is_active` IS NOT ON IT. The server's field is a `*bool` whose absence
 * means TRUE, and this form never sends the key: a tickbox here would let
 * somebody create an INACTIVE employee, who is silently left out of the pay run
 * — a missing salary, discovered on pay day. Somebody who has LEFT is recorded
 * with a date of leaving, which is a fact, not a flag.
 */
function EmployeeForm({
  firstFieldRef,
  busy,
  onCancel,
  onSubmit,
  onError,
}: {
  firstFieldRef: React.RefObject<HTMLInputElement>;
  busy: boolean;
  onSubmit: (req: {
    code: string;
    name: string;
    doj: string;
    designation: string;
    pan: string;
    uan: string;
    esic_ip_number: string;
    bank_ifsc: string;
    bank_account_last4: string;
  }) => Promise<string>;
  onCancel: () => void;
  onError: (message: string, denied: boolean) => void;
}) {
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [doj, setDoj] = useState("");
  const [designation, setDesignation] = useState("");
  const [pan, setPan] = useState("");
  const [uan, setUan] = useState("");
  const [ip, setIp] = useState("");
  const [ifsc, setIfsc] = useState("");
  const [last4, setLast4] = useState("");

  const submit = useCallback(async (): Promise<string> => {
    if (code.trim() === "" || name.trim() === "") {
      return "An employee needs a code and a name. The code is what every statutory file identifies them by, and it must be unique in this company.";
    }
    try {
      await onSubmit({
        code: code.trim(),
        name: name.trim(),
        doj: doj.trim(),
        designation: designation.trim(),
        pan: pan.trim().toUpperCase(),
        uan: uan.trim(),
        esic_ip_number: ip.trim(),
        bank_ifsc: ifsc.trim().toUpperCase(),
        bank_account_last4: last4.trim(),
      });
      return `${code.trim()} created.`;
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      onError(message, e instanceof ApiError && e.status === 403);
      return message;
    }
  }, [code, designation, doj, ifsc, ip, last4, name, onError, onSubmit, pan, uan]);

  // Ctrl+A accepts the form. ONE registration, dispatching on which panel is
  // open, is what the challan register learned the hard way — but here the two
  // panels are mutually exclusive components, so each owns its own Ctrl+A and
  // only one is ever mounted.
  useRegisterScreenAction("accept", submit);

  return (
    <div
      data-testid="employee-form"
      className="mt-3 max-w-3xl border border-tally-fieldText p-3"
    >
      <h2 className="text-sm font-bold text-tally-title">New employee</h2>
      <div className="mt-2 grid grid-cols-2 gap-2 text-sm md:grid-cols-3">
        <Field label="Code" testid="employee-code" value={code} onChange={setCode} inputRef={firstFieldRef} />
        <Field label="Name" testid="employee-name" value={name} onChange={setName} />
        <Field label="Date of joining (YYYY-MM-DD)" testid="employee-doj" value={doj} onChange={setDoj} />
        <Field label="Designation" testid="employee-designation" value={designation} onChange={setDesignation} />
        <Field label="PAN" testid="employee-pan" value={pan} onChange={setPan} />
        <Field label="UAN" testid="employee-uan" value={uan} onChange={setUan} />
        <Field label="ESIC IP number" testid="employee-ip" value={ip} onChange={setIp} />
        <Field label="Bank IFSC" testid="employee-ifsc" value={ifsc} onChange={setIfsc} />
        <Field label="Bank a/c last 4" testid="employee-last4" value={last4} onChange={setLast4} />
      </div>
      <p className="mt-2 text-xs">
        Ctrl+A saves. Only the last four digits of the account number are stored
        — the ECR and the ESI return do not carry more, and nothing else in this
        product needs it.
      </p>
      <div className="mt-2 flex gap-2">
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          disabled={busy}
          onClick={() => void submit()}
        >
          Save (Ctrl+A)
        </button>
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          onClick={onCancel}
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

/**
 * The pay panel (Alt+S): what this person is paid, and under which elections.
 *
 * It shows what is ALREADY stored above each form, because both children are
 * effective-DATED and a window is only checkable as a sequence: an operator
 * looking at "Basic 50,000" cannot tell a standing figure from one superseded
 * last month, and the pay run reads the window in force on the FIRST of the wage
 * month — a question about the past for every back-dated run.
 */
function EmployeePayPanel({
  employee,
  vocabulary,
  existing,
  firstFieldRef,
  busy,
  onClose,
  onSaved,
  onError,
  client,
  companyID,
}: {
  employee: EmployeeView;
  vocabulary: PayrollVocabularyResponse | null;
  existing: EmployeePay | undefined;
  firstFieldRef: React.RefObject<HTMLInputElement>;
  busy: boolean;
  onClose: () => void;
  onSaved: (message: string) => Promise<void>;
  onError: (message: string) => void;
  client: ReturnType<typeof useClient>;
  companyID: string;
}) {
  const [from, setFrom] = useState("");
  const [compCode, setCompCode] = useState("");
  const [compType, setCompType] = useState("");
  const [amount, setAmount] = useState("");

  const [elFrom, setElFrom] = useState("");
  const [pfBase, setPfBase] = useState("");
  const [regime, setRegime] = useState("");
  const [epsMember, setEpsMember] = useState(false);
  const [esiApplicable, setEsiApplicable] = useState(false);

  const saveComponent = useCallback(async (): Promise<string> => {
    if (from.trim() === "" || compCode.trim() === "" || compType === "" || amount.trim() === "") {
      return "A salary component needs an effective-from date, a name, a type and an amount. None of the four is defaulted: the date decides which months it applies to, and the type decides whether PF and ESI are contributed on it.";
    }
    try {
      await client.setSalaryComponent(companyID, employee.id, {
        effective_from: from.trim(),
        code: compCode.trim(),
        type: compType,
        // A STRING all the way to the wire. Parsing it here would put a float
        // between the operator's keystroke and the salary.
        amount: amount.trim(),
      });
      const message = `${compCode.trim()} added to ${employee.code} ${employee.name} with effect from ${from.trim()}.`;
      await onSaved(message);
      setCompCode("");
      setAmount("");
      return message;
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      onError(message);
      return message;
    }
  }, [amount, client, companyID, compCode, compType, employee, from, onError, onSaved]);

  const saveElection = useCallback(async (): Promise<string> => {
    if (elFrom.trim() === "" || pfBase === "") {
      return "An election needs an effective-from date and a PF contribution base. CEILING and ACTUAL differ for anybody paid above the EPF wage ceiling, so there is no safe default for the base.";
    }
    try {
      await client.setStatutoryElection(companyID, employee.id, {
        effective_from: elFrom.trim(),
        pf_base: pfBase,
        eps_member: epsMember,
        esi_applicable: esiApplicable,
        // "" is sent as absent: the server stores an empty regime as "no
        // election", which is a different fact from electing the default.
        ...(regime === "" ? {} : { regime }),
      });
      const message = `Statutory election recorded for ${employee.code} ${employee.name} with effect from ${elFrom.trim()}.`;
      await onSaved(message);
      return message;
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      onError(message);
      return message;
    }
  }, [client, companyID, elFrom, employee, epsMember, esiApplicable, onError, onSaved, pfBase, regime]);

  // ⛔ ONE ACCEPT KEY, TWO FORMS. Ctrl+A is registered ONCE and dispatches on
  // which form has been started, exactly as the TDS challan register does.
  // Registering it twice would leave whichever effect ran last owning the key,
  // and the loser would be a Ctrl+A that silently did the other thing.
  useRegisterScreenAction(
    "accept",
    useCallback((): string | Promise<string> => {
      const componentStarted =
        compCode.trim() !== "" || amount.trim() !== "" || compType !== "";
      const electionStarted = pfBase !== "" || regime !== "" || epsMember || esiApplicable;
      if (componentStarted && electionStarted) {
        return "Both forms below have been started. Save one at a time with its own button — Ctrl+A cannot tell which you meant, and guessing would store the half you were not looking at.";
      }
      if (electionStarted) return saveElection();
      return saveComponent();
    }, [amount, compCode, compType, epsMember, esiApplicable, pfBase, regime, saveComponent, saveElection]),
  );

  return (
    <div
      data-testid="employee-pay-panel"
      className="mt-3 max-w-4xl border border-tally-fieldText p-3"
    >
      <h2 className="text-sm font-bold text-tally-title">
        {employee.code} {employee.name} — salary and statutory election
      </h2>
      <p className="mt-1 text-xs">
        Both are effective-DATED over a half-open window: a row applies from its
        date until the next one starts. The pay run reads whatever is in force on
        the FIRST day of the wage month, so back-dating a revision changes months
        that have not been run yet — and changes nothing that has.
      </p>

      {/* ---- WHAT IS ALREADY THERE. ---------------------------------------- */}
      <h3 className="mt-3 text-xs font-bold text-tally-title">
        Salary structure
      </h3>
      {existing && existing.components.length > 0 ? (
        <table className="mt-1 w-full text-sm" data-testid="salary-components">
          <thead>
            <tr className="border-b border-tally-panel text-left text-xs">
              <th className="py-1 pr-2">From</th>
              <th className="py-1 pr-2">To</th>
              <th className="py-1 pr-2">Component</th>
              <th className="py-1 pr-2">Type</th>
              <th className="py-1 pr-2 text-right">Amount</th>
              <th className="py-1 pr-2">PF/ESI contributed on it?</th>
            </tr>
          </thead>
          <tbody>
            {existing.components.map((c) => (
              <tr key={c.id} data-testid={`salary-component:${c.code}`}>
                <td className="py-0.5 pr-2">{c.effective_from}</td>
                <td className="py-0.5 pr-2">{c.effective_to || "still in force"}</td>
                <td className="py-0.5 pr-2">{c.code}</td>
                <td className="py-0.5 pr-2">{c.type}</td>
                <td className="py-0.5 pr-2 text-right">{formatMoney(c.amount)}</td>
                <td className="py-0.5 pr-2 text-xs">
                  {/* THE SERVER'S CLASSIFICATION, rendered as the sentence it is.
                      "excluded" alone would read as "ignored", and the deeming
                      proviso means an excluded component can still be pulled back
                      into wages when it exceeds half of all remuneration. */}
                  {c.is_wages
                    ? "yes — this is wages under s.2(88)"
                    : c.in_deeming_test
                      ? "no — excluded, but counts towards the one-half deeming test"
                      : "no — excluded, and outside the deeming test"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p data-testid="no-salary-components" className="mt-1 text-sm font-bold">
          NOTHING RECORDED. This person cannot be paid: a pay run for any month
          is refused outright — &ldquo;there is nothing to pay and nothing to
          contribute on&rdquo; — and the refusal stops the WHOLE run, not just
          their payslip.
        </p>
      )}

      <div className="mt-2 grid grid-cols-2 gap-2 text-sm md:grid-cols-4">
        <Field
          label="Effective from (YYYY-MM-DD)"
          testid="component-from"
          value={from}
          onChange={setFrom}
          inputRef={firstFieldRef}
        />
        <Field label="Component name" testid="component-code" value={compCode} onChange={setCompCode} />
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">Type</span>
          <select
            aria-label="Component type"
            data-testid="component-type"
            className="bg-tally-field px-1 text-tally-fieldText outline-none"
            value={compType}
            onChange={(e) => setCompType(e.target.value)}
          >
            <option value="">— choose —</option>
            {/* ⛔ THE SERVED VOCABULARY AND NOT A LIST WRITTEN HERE. A hardcoded
                list can offer a type the server refuses, and `is_wages` decides
                whether PF is contributed at all — it carries the holding that a
                universally paid special allowance IS wages, which was the most
                common PF under-contribution there was. */}
            {(vocabulary?.component_types ?? []).map((t) => (
              <option key={t.code} value={t.code}>
                {t.code}
                {t.is_wages ? " (wages)" : " (excluded)"}
              </option>
            ))}
          </select>
        </label>
        <Field label="Amount per month" testid="component-amount" value={amount} onChange={setAmount} />
      </div>
      {vocabulary === null ? (
        <p data-testid="vocabulary-unavailable" className="mt-1 text-xs text-tally-bar">
          The component-type list could not be read from the server, so the picker
          above is empty. It is deliberately not filled in from a list held here:
          whether PF and ESI are contributed on a component is decided by its
          type, and a guess at that list is a guess at somebody&apos;s
          contributions.
        </p>
      ) : null}
      <button
        type="button"
        className="mt-2 border border-tally-fieldText px-2 py-0.5 text-xs"
        disabled={busy}
        onClick={() => void saveComponent()}
      >
        Add this component
      </button>

      {/* ---- THE ELECTION. ------------------------------------------------- */}
      <h3 className="mt-4 text-xs font-bold text-tally-title">
        Statutory election
      </h3>
      {existing && existing.elections.length > 0 ? (
        <table className="mt-1 w-full text-sm" data-testid="statutory-elections">
          <thead>
            <tr className="border-b border-tally-panel text-left text-xs">
              <th className="py-1 pr-2">From</th>
              <th className="py-1 pr-2">To</th>
              <th className="py-1 pr-2">PF base</th>
              <th className="py-1 pr-2">Pension scheme</th>
              <th className="py-1 pr-2">ESI</th>
              <th className="py-1 pr-2">Tax regime</th>
            </tr>
          </thead>
          <tbody>
            {existing.elections.map((el) => (
              <tr key={el.id} data-testid={`statutory-election:${el.effective_from}`}>
                <td className="py-0.5 pr-2">{el.effective_from}</td>
                <td className="py-0.5 pr-2">{el.effective_to || "still in force"}</td>
                <td className="py-0.5 pr-2">{el.pf_base}</td>
                <td className="py-0.5 pr-2">{el.eps_member ? "member" : "not a member"}</td>
                <td className="py-0.5 pr-2">{el.esi_applicable ? "covered" : "not covered"}</td>
                <td className="py-0.5 pr-2">
                  {/* ⛔ "" IS NOT "NEW". An empty regime means no election was
                      made; the server resolves it to the default when it computes
                      and stores it as given so the two stay distinguishable. */}
                  {el.regime === ""
                    ? `no election — the default applies (${vocabulary?.default_regime ?? "the statutory default"})`
                    : el.regime}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p data-testid="no-statutory-elections" className="mt-1 text-sm font-bold">
          NOTHING RECORDED. This person cannot be paid: the PF contribution base
          and the tax regime are ELECTIONS, and there is no safe default for the
          first of them — CEILING and ACTUAL differ for anybody paid above the
          wage ceiling, so the server refuses the whole run rather than choosing.
        </p>
      )}

      <div className="mt-2 grid grid-cols-2 gap-2 text-sm md:grid-cols-4">
        <Field
          label="Effective from (YYYY-MM-DD)"
          testid="election-from"
          value={elFrom}
          onChange={setElFrom}
        />
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">PF contribution base</span>
          <select
            aria-label="PF contribution base"
            data-testid="election-pf-base"
            className="bg-tally-field px-1 text-tally-fieldText outline-none"
            value={pfBase}
            onChange={(e) => setPfBase(e.target.value)}
          >
            {/* No pre-selection. The two answers differ for anybody paid above
                the wage ceiling, so a default here would be a contribution
                figure nobody chose. */}
            <option value="">— choose —</option>
            {(vocabulary?.pf_bases ?? []).map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">Income-tax regime</span>
          <select
            aria-label="Income-tax regime"
            data-testid="election-regime"
            className="bg-tally-field px-1 text-tally-fieldText outline-none"
            value={regime}
            onChange={(e) => setRegime(e.target.value)}
          >
            {/* ⛔ "no election" IS THE FIRST OPTION AND IT IS NOT A BLANK. It is a
                real, recordable answer — most employees make no election — and
                pre-selecting the default instead would record a choice nobody
                made. */}
            <option value="">
              no election — the default applies
              {vocabulary?.default_regime ? ` (${vocabulary.default_regime})` : ""}
            </option>
            {(vocabulary?.regimes ?? []).map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </label>
        <div className="flex flex-col justify-end gap-1 text-xs">
          <label className="flex items-center gap-1">
            <input
              type="checkbox"
              data-testid="election-eps"
              checked={epsMember}
              onChange={(e) => setEpsMember(e.target.checked)}
            />
            Member of the pension scheme (EPS)
          </label>
          <label className="flex items-center gap-1">
            <input
              type="checkbox"
              data-testid="election-esi"
              checked={esiApplicable}
              onChange={(e) => setEsiApplicable(e.target.checked)}
            />
            Covered by ESI
          </label>
        </div>
      </div>
      <p className="mt-1 text-xs">
        Both tickboxes are POSITIVE assertions about this person and start
        unticked, which means &ldquo;not asserted&rdquo;. Neither is safe to
        assume: assuming membership contributes on somebody who is not a member,
        and assuming coverage withholds from somebody who is not covered.
      </p>
      <div className="mt-2 flex gap-2">
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          disabled={busy}
          onClick={() => void saveElection()}
        >
          Record this election
        </button>
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          onClick={onClose}
        >
          Close
        </button>
      </div>
    </div>
  );
}

/** One labelled text field. */
function Field({
  label,
  testid,
  value,
  onChange,
  inputRef,
}: {
  label: string;
  testid: string;
  value: string;
  onChange: (v: string) => void;
  inputRef?: React.RefObject<HTMLInputElement>;
}) {
  return (
    <label className="flex flex-col">
      <span className="text-xs text-tally-title">{label}</span>
      <input
        ref={inputRef}
        aria-label={label}
        data-testid={testid}
        className="bg-tally-field px-1 text-tally-fieldText outline-none"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}
