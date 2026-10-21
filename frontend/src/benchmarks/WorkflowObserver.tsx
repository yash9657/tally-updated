import { useEffect, useRef, useState } from "react";
import { measureWorkflow, type TimingContext } from "./workflowTiming";
import "./workflowObserver.css";

type Measurement = ReturnType<typeof measureWorkflow>;
type Sample = ReturnType<Measurement["finish"]>;

/** Local development pilot only; mounted outside the measured app root so
 * observer controls are not counted as product interactions. No telemetry. */
export function WorkflowObserver({ target }: { target: EventTarget }) {
  const [context, setContext] = useState<TimingContext>({ task: "", industry: "services", bookFingerprint: "", path: "manual_ui", source: "operator_pilot", operator: "" });
  const measurement = useRef<Measurement | null>(null);
  const [running, setRunning] = useState(false);
  const [active, setActive] = useState(false);
  const [finalFingerprint, setFinalFingerprint] = useState("");
  const [verified, setVerified] = useState(false);
  const [deliverable, setDeliverable] = useState<"state_change" | "downloaded_file" | "advice_or_draft">("state_change");
  const [samples, setSamples] = useState<Sample[]>([]);
  useEffect(() => () => {
    measurement.current?.finish({ completed: false, qualityVerified: false, finalFingerprint: null, deliverable: "advice_or_draft" });
    measurement.current = null;
  }, []);
  const finish = (completed: boolean) => {
    const result = measurement.current?.finish({ completed, qualityVerified: verified, finalFingerprint: finalFingerprint || null, deliverable });
    measurement.current = null;
    if (result) setSamples((old) => [...old, result]);
    setRunning(false); setActive(false);
  };
  const download = () => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(samples, null, 2)], { type: "application/json" }));
    const a = document.createElement("a"); a.href = url; a.download = `workflow-pilot-${Date.now()}.json`; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  const field = (key: "task" | "operator" | "bookFingerprint", label: string) => <label style={{ display: "block" }}>{label}<input style={{ width: "100%" }} required value={context[key]} onChange={(e) => setContext({ ...context, [key]: e.target.value })} /></label>;
  return <aside className="workflow-observer" aria-label="Workflow pilot observer" style={{ position: "fixed", bottom: 12, right: 12, zIndex: 10000, width: 340, maxHeight: "90vh", overflow: "auto", background: "#fff", color: "#17212b", padding: 16, border: "1px solid #56616b", borderRadius: 8, boxShadow: "0 2px 12px #0003", font: "14px system-ui" }}>
    <strong>Local workflow pilot</strong>
    <p>Use synthetic books. Mark active reading, thinking, entry and review; pause for system waiting. Observer controls are excluded from click counts.</p>
    <fieldset disabled={running} style={{ display: "grid", gap: 8 }}>
      {field("operator", "Pseudonymous operator code")}
      {field("task", "Task / matched variant")}
      {field("bookFingerprint", "Verified starting book fingerprint")}
      <label>Industry <select value={context.industry} onChange={(e) => setContext({ ...context, industry: e.target.value as TimingContext["industry"] })}>{["retail", "wholesale", "services", "manufacturing"].map((x) => <option key={x}>{x}</option>)}</select></label>
      <label>Path <select value={context.path} onChange={(e) => setContext({ ...context, path: e.target.value as TimingContext["path"] })}><option value="manual_ui">Manual UI</option><option value="agent_ui">Agent UI</option></select></label>
      <label>Source <select value={context.source} onChange={(e) => setContext({ ...context, source: e.target.value as TimingContext["source"] })}><option value="operator_pilot">Observed operator</option><option value="browser_automation">Browser automation</option></select></label>
      <button disabled={!context.task.trim() || !context.operator.trim() || !context.bookFingerprint.trim()} onClick={() => { measurement.current = measureWorkflow(target, { ...context }); setFinalFingerprint(""); setVerified(false); setRunning(true); }}>Start trial</button>
    </fieldset>
    {running && <div style={{ display: "grid", gap: 8, marginTop: 8 }}>
      <button onClick={() => { measurement.current?.setActive(!active); setActive(!active); }}>{active ? "Pause active work" : "Mark active work"}</button>
      <div>{(["approval", "clarification", "correction"] as const).map((kind) => <button key={kind} onClick={() => measurement.current?.intervention(kind)}>Count {kind}</button>)}</div>
      <label>Verified final book / file fingerprint<input style={{ width: "100%" }} value={finalFingerprint} onChange={(e) => setFinalFingerprint(e.target.value)} /></label>
      <label>Deliverable <select value={deliverable} onChange={(e) => setDeliverable(e.target.value as typeof deliverable)}><option value="state_change">Persisted state change</option><option value="downloaded_file">Downloaded file</option><option value="advice_or_draft">Advice / draft only</option></select></label>
      <label><input type="checkbox" checked={verified} onChange={(e) => setVerified(e.target.checked)} /> Independent result check passed</label>
      <button disabled={!verified || !finalFingerprint.trim() || deliverable === "advice_or_draft"} onClick={() => finish(true)}>Finish verified execution</button>
      <button onClick={() => finish(false)}>Record incomplete trial</button>
    </div>}
    <p aria-live="polite">{samples.length} retained trials; {samples.filter((r) => r.completed).length} verified executions. Human savings require matched trials.</p>
    <button disabled={!samples.length || running} onClick={download}>Download trials</button>
  </aside>;
}
