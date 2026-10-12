import { useRef, useState, type KeyboardEvent } from "react";
import { ArrowIcon, Brand, ButtonLink, Keycap } from "../design-system/components";
import "./marketing.css";

const previewTabs = ["Day Book", "Stock Summary", "Insights"] as const;
type PreviewTab = typeof previewTabs[number];

function ProductPreview() {
  const [active, setActive] = useState<PreviewTab>("Day Book");
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  function changeTab(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    let next = index;
    if (event.key === "ArrowRight") next = (index + 1) % previewTabs.length;
    else if (event.key === "ArrowLeft") next = (index + previewTabs.length - 1) % previewTabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = previewTabs.length - 1;
    else return;
    event.preventDefault();
    setActive(previewTabs[next]!);
    tabs.current[next]?.focus();
  }
  return <figure className="product-preview" aria-label="Interactive Tallyweb feature preview">
    <div className="preview-top"><span className="preview-brand">tallyweb<span>.</span></span><span>Gateway of Tallyweb</span></div>
    <div className="preview-company"><span>Sample Traders</span><span>FY 2026–27</span></div>
    <div className="preview-tabs" role="tablist" aria-label="Explore the workspace">
      {previewTabs.map((name, i) => <button key={name} type="button" role="tab" id={`preview-tab-${i}`} aria-controls={`preview-panel-${i}`} aria-selected={name === active} tabIndex={name === active ? 0 : -1} ref={el => { tabs.current[i] = el; }} onClick={() => setActive(name)} onKeyDown={event => changeTab(event, i)}>{name}</button>)}
    </div>
    <div className="preview-content" role="tabpanel" id={`preview-panel-${previewTabs.indexOf(active)}`} aria-labelledby={`preview-tab-${previewTabs.indexOf(active)}`} tabIndex={0}>
      {active === "Day Book" && <>
        <div className="preview-heading"><h3>Day Book</h3><span>1 Apr 2026</span></div>
        <table className="preview-table"><thead><tr><th>Particulars</th><th>Type</th><th>Amount ₹</th></tr></thead><tbody>
          <tr className="preview-row-selected"><td>Mehta Enterprises<small>INV-001 · Sales invoice</small></td><td>Sales</td><td>24,800.00</td></tr>
          <tr><td>City Office Supplies<small>PUR-001 · Office supplies</small></td><td>Purchase</td><td>6,400.00</td></tr>
          <tr><td>Mehta Enterprises<small>REC-001 · Bank transfer</small></td><td>Receipt</td><td>24,800.00</td></tr>
          <tr><td>Office Rent<small>PAY-001 · Monthly rent</small></td><td>Payment</td><td>12,000.00</td></tr>
        </tbody></table>
        <div className="preview-summary"><span>Every entry, in its place.</span><span>4 vouchers</span></div>
      </>}
      {active === "Stock Summary" && <>
        <div className="preview-heading"><h3>Stock Summary</h3><span>1 Apr 2026</span></div>
        <table className="preview-table"><thead><tr><th>Stock item</th><th>Quantity</th><th>Value ₹</th></tr></thead><tbody>
          <tr className="preview-row-selected"><td>A4 Copier Paper<small>Office essentials</small></td><td>120 reams</td><td>36,000.00</td></tr>
          <tr><td>Printer Cartridge<small>Office essentials</small></td><td>24 pcs</td><td>28,800.00</td></tr>
          <tr><td>Desk Organiser<small>Accessories</small></td><td>48 pcs</td><td>14,400.00</td></tr>
          <tr><td>Ring Binder<small>Stationery</small></td><td>80 pcs</td><td>12,000.00</td></tr>
        </tbody></table>
        <div className="preview-summary"><span>Closing stock value</span><strong>₹91,200.00</strong></div>
      </>}
      {active === "Insights" && <>
        <div className="preview-heading"><h3>Insights</h3><span>For your review</span></div>
        <div className="preview-insight"><span className="preview-insight-label">Receivables</span><h4>A payment needs a follow-up.</h4><p>Mehta Enterprises has an overdue invoice. Review the outstanding balance before you follow up.</p><dl><div><dt>Invoice</dt><dd>INV-042</dd></div><div><dt>Outstanding</dt><dd>₹18,600.00</dd></div></dl><span className="preview-insight-note">Suggested action · Review outstanding bills</span></div>
        <div className="preview-summary"><span>Insight first. Your decision next.</span></div>
      </>}
    </div>
    <div className="preview-keys" aria-hidden="true"><span><b>F2</b> Date</span><span><b>F8</b> Sales</span><span><b>Alt+G</b> Go To</span><span><b>Esc</b> Back</span></div>
    <figcaption><span className="preview-status" />Interactive preview · Illustrative data</figcaption>
  </figure>;
}

const features = [
  { title: "Accounting, without the detours.", text: "Sales, purchases, receipts, and payments. Keep ledgers and vouchers together, then drill into your Day Book, Balance Sheet, and Profit & Loss." , detail: "Vouchers & financial reports" },
  { title: "Know what’s on the shelf.", text: "Follow stock movements, manage sales and purchase orders, and see closing quantities and values in your Stock Summary.", detail: "Inventory & order management" },
  { title: "Bring the details into balance.", text: "Import bank statements, reconcile transactions, and work through outstanding bills. Keep GST reports and tax workflows close to your books.", detail: "Banking & tax reporting" },
  { title: "Let AI help with the groundwork.", text: "Turn bills into draft vouchers, ask questions about your books, and review insights and agent proposals in dedicated inboxes.", detail: "Bill Inbox, Query & Insights" },
];

export function LandingPage() {
  return <div className="marketing-page">
    <a className="skip-link" href="#main-content">Skip to content</a>
    <header className="site-header page-width">
      <Brand />
      <nav aria-label="Main navigation"><a href="#features">Features</a><a href="#workflow">How it works</a></nav>
      <ButtonLink href="/signin" variant="secondary">Sign in <ArrowIcon /></ButtonLink>
    </header>
    <main id="main-content">
      <section className="hero page-width" aria-labelledby="hero-title">
        <div className="hero-copy">
          <h1 id="hero-title">Familiar keys.<br /><span>A new chapter.</span></h1>
          <p>The accounting rhythm you know. The freedom of the web. Bring your books, inventory, and business insights into one familiar workspace.</p>
          <div className="hero-actions"><ButtonLink href="/signin">Open your workspace <ArrowIcon /></ButtonLink><a className="text-link" href="#features">Explore the features <span aria-hidden="true">↓</span></a></div>
          <div className="hero-footnote"><Keycap>Alt</Keycap><span>+</span><Keycap>G</Keycap><span>Your muscle memory belongs here.</span></div>
        </div>
        <ProductPreview />
      </section>
      <div className="capabilities-strip"><div className="page-width"><span>Built for the way you work</span><ul><li>Keyboard first</li><li>Browser based</li><li>AI assisted</li><li>India focused</li></ul></div></div>
      <section className="features-section" id="features" aria-labelledby="features-title">
        <div className="page-width features-layout">
          <div className="section-intro"><h2 id="features-title">From the first entry<br />to the full picture.</h2><p>The everyday essentials, connected. Less jumping between tools. More clarity in your books.</p><a className="text-link" href="/signin">Find your flow <ArrowIcon /></a></div>
          <div className="feature-list">{features.map(feature => <article className="feature" key={feature.title}><div className="feature-symbol" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M5 4h14v16H5zM9 8h6M9 12h6M9 16h3" /></svg></div><div><h3>{feature.title}</h3><p>{feature.text}</p><span className="feature-detail">{feature.detail}</span></div></article>)}</div>
        </div>
      </section>
      <section id="workflow" className="workflow-section page-width" aria-labelledby="workflow-title">
        <div className="workflow-copy"><h2 id="workflow-title">Stay in your rhythm.<br /><span>Keep your hands on the keys.</span></h2><p>Move from entry to report with familiar shortcuts. Create a voucher, accept it, and go straight to the next thing. A browser workspace that feels like second nature.</p><ButtonLink href="/signin" variant="secondary">Make yourself at home <ArrowIcon /></ButtonLink></div>
        <dl className="shortcut-list"><div><dt><Keycap>F8</Keycap></dt><dd><strong>Start with a sale</strong><span>Open a sales voucher.</span></dd></div><div><dt><Keycap>Ctrl</Keycap><span> + </span><Keycap>A</Keycap></dt><dd><strong>Make the entry count</strong><span>Accept the current entry.</span></dd></div><div><dt><Keycap>Alt</Keycap><span> + </span><Keycap>G</Keycap></dt><dd><strong>Go straight to it</strong><span>Jump to a report or screen.</span></dd></div><div><dt><Keycap>Esc</Keycap></dt><dd><strong>One step back</strong><span>Return to the previous screen.</span></dd></div></dl>
      </section>
      <section className="control-section" aria-labelledby="control-title"><div className="page-width control-layout">
        <div className="review-example" aria-label="Illustrative bill review workflow"><div className="review-example__header"><span>Bill Inbox</span><span>Illustrative workflow</span></div><div className="review-document"><svg width="32" height="40" viewBox="0 0 32 40" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M5 2h14l8 8v28H5zM19 2v9h8M10 19h12M10 25h12M10 31h7" /></svg><div><strong>supplier-invoice.pdf</strong><span>A bill becomes a draft voucher</span></div></div><ol className="review-steps"><li><span>Import</span><p>Bring a supplier bill into the Bill Inbox.</p></li><li><span>Review</span><p>Check the extracted details and ledger mapping.</p></li><li><span>Accept</span><p>Post the entry when it’s ready.</p></li></ol><p className="review-caption">From document to draft, with a review in between.</p></div>
        <div className="section-intro"><h2 id="control-title">A helping hand.<br />You hold the books.</h2><p>Give repetitive work a head start with AI-assisted drafts. Review proposals, manage access by role, and follow changes through the edit log.</p><ul className="control-points"><li>Reviewable drafts and an Approval Inbox</li><li>Company-scoped access and permissions</li><li>A version history for your vouchers</li></ul></div>
      </div></section>
      <section className="faq-section page-width" aria-labelledby="faq-title"><h2 id="faq-title">A few things to know.</h2><div className="faq-list">
        <details><summary>Will the shortcuts feel familiar?</summary><p>Yes. Tallyweb is built around Tally-style keyboard navigation, including F8 for Sales, Ctrl+A to accept, Alt+G for Go To, and Esc to go back. You can also use on-screen controls.</p></details>
        <details><summary>Can I bring my existing books?</summary><p>The workspace includes an Import from Tally flow for supported exports. Review the import results and reconcile your balances before moving your day-to-day work over.</p></details>
        <details><summary>How do I get access?</summary><p>Sign in with your Tallyweb user name and password. If you do not have an account, ask your company administrator to arrange access. There is no public self-service signup at the moment.</p></details>
        <details><summary>Is Tallyweb a Tally Solutions product?</summary><p>No. Tallyweb is an independent accounting project. Tally and TallyPrime are trademarks of their respective owner; Tallyweb is not affiliated with or endorsed by Tally Solutions.</p></details>
      </div></section>
      <section className="closing-section"><div className="page-width"><div><h2>Your next chapter<br />starts with an entry.</h2><p>Your books. Your shortcuts. Your workspace.</p></div><ButtonLink href="/signin">Sign in to Tallyweb <ArrowIcon /></ButtonLink></div></section>
    </main>
    <footer className="site-footer page-width"><div><Brand /><p>Familiar by design. Ready for the web.</p></div><div className="footer-links"><a href="#features">Features</a><a href="#workflow">How it works</a><a href="/signin">Sign in</a></div><p className="footer-note">An independent accounting project. Not affiliated with Tally Solutions.</p></footer>
  </div>;
}
