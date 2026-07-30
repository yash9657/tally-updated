# docs/statutory — primary sources, downloaded by a human

**Why this directory exists:** `incometaxindia.gov.in` returns **403 to every automated
request** — with and without a browser user-agent, from more than one fetcher. A human with
a browser gets through instantly. So the primary statutory documents this project must not
guess at are downloaded by hand and committed here.

**What belongs here:** the notified form, the file-format specification, the gazette text —
the things a rate, a field or a code list must be traceable to. **Not** blog articles, and
not summaries.

**What is expected, and why** (see `HANDOFF-CLOSING-THE-GAP.md` §1):

| File | Source | What is blocked without it |
|---|---|---|
| `form-140.pdf` | `https://www.incometaxindia.gov.in/documents/d/guest/fn-140` | The quarterly TDS return's field list and the four-digit payment code per nature of payment. **`docs/design/t6-model.md` invariant I-7 refuses to emit any return file until this is read.** |
| `form-140-faqs.pdf` | `https://www.incometaxindia.gov.in/documents/d/guest/form-140-faqs` | Same. |
| `fvu-file-format.pdf` | Protean/TIN or `incometax.gov.in` — "File Format for Quarterly Return", tax year 2026-27 | Column order, field widths, and the non-deduction / lower-deduction reason codes. |
| `ita-2025-as-amended-fa-2026.pdf` | `https://www.incometaxindia.gov.in/documents/d/guest/income_tax_act_2025_as_amended_by_fa_act_2026-pdf` | Re-verifying `docs/design/t6-model.md` §9's rate table against the **amended** text. §9 was read from the Act **as enacted**, so any Finance Act 2026 change is currently unchecked. |

**A note on what is already verified.** The Income-tax Act 2025 as enacted HAS been read —
sections 392, 393, 394, 395, 397 and 516, verbatim from the Gazette text, via ICAI's mirror
(`resource.cdn.icai.org`). All eight shipped TDS/TCS rates match it. See
`docs/design/t6-model.md` §9. The gap is the FORM and the FILE FORMAT, not the statute.

**The FVU cannot be run in this environment, and the GSTN portal, the NIC e-Way Bill API and
the EPFO/ESIC portals cannot be reached from it. Never claim a validation that was not
performed.**
