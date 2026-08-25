# HANDOFF — T32 MET A REAL TALLY FILE, AND SEVEN THINGS WERE WRONG

**Session date:** 2026-08-24 · **Register:** T32 (the commercial blocker) ·
**State at HEAD before this session:** `POST /import/tally-xml` existed and had never met
a genuine export. Every handoff since `HANDOFF-FEATURE-LOCK.md` said so, and §6.1's
answer — "no public corpus exists, install TallyPrime Educational" — was still unspent.

---

## WHAT HAPPENED, IN ONE PARAGRAPH

The user supplied **a real TallyPrime export from a live install**: `Master.xml` (84 MB,
3,706 ledgers, 37 account groups) and `Transactions.xml` (8 MB, 121 vouchers), from a
grain-trading company, month of August 2026. The importer did not read it. Seven defects
were found, **all seven fixed**, and the file now imports end to end with the Trial
Balance reconciling to the paisa against an independent recomputation from the XML:
**3,706 ledgers, Dr ₹2,59,86,501.88 = Cr ₹2,59,86,501.88.** Three of the seven were
SILENT — the worst parsed the whole 84 MB without error and imported nothing.

⛔ **The file is not in the repo and must not be.** It is a real company's ledger: 3,706
named counter-parties, their GSTINs, their addresses, what they owe. The end-to-end gate
is opt-in by path; an anonymised fixture carries the shapes into CI.

---

## THE SEVEN, IN THE ORDER THEY BITE

Each was proved against the real file before it was fixed, and each is now gated.

| # | Defect | Evidence from the real file | Severity |
|---|---|---|---|
| **1** | **Tally writes UTF-16LE**, with no `<?xml?>` declaring it. Read as UTF-8 | `XML syntax error on line 1: invalid UTF-8` | LOUD — total |
| **2** | **U+0004 in ordinary field values** (`<CSTFORMISSUETYPE>&#4; Not Applicable</…>`). XML 1.0 FORBIDS a numeric reference to a C0 control | `illegal character code U+0004`; **32,464 occurrences** across the two files | LOUD — total |
| **3** | ⛔ **The envelope is `BODY > IMPORTDATA > REQUESTDESC / REQUESTDATA`**, not `BODY > DESC / DATA`. Only the flat shape was modelled | **Parsed clean. Zero messages. "Successful" import of nothing** | ⛔ SILENT |
| **4** | ⛔ **An item invoice keeps its revenue lines inside `ALLINVENTORYENTRIES.LIST > ACCOUNTINGALLOCATIONS.LIST`**, never at the top level. `Entries()` returned one top-level list *or* the other and never looked inside | **63 of 121 vouchers** unbalanced by exactly the goods value → refused by the double-entry trigger | LOUD — most vouchers |
| **5** | ⛔ **`ISDEEMEDPOSITIVE` is not the side; the `AMOUNT` SIGN is.** A round-off says `No` (credit) and carries `-0.12` (debit) | **25 lines** disagreed. By sign: **121/121 balanced**. By flag: **96/121** | LOUD — some vouchers |
| **6** | **User-defined account GROUPS were never imported**, and Tally spells two built-ins with a lowercase h (`Cash-in-hand`) which `account_group` — alone among the masters — has **no normalised unique index** to protect | 9 user groups, **217 ledgers** under them. `masters.CreateLedger` refuses ErrNotFound → import died on the first. A case-only near-miss would have CREATED A SECOND cash group | LOUD, and a silent one behind it |
| **7** | ⛔ **A ledger the company was already SEEDED with kept its zero opening.** `EnsureLedgerWithOpening` returns an existing ledger unchanged — correct rule, and on its own it drops the export's opening balance | **`Cash`: ₹14,14,596.01 dropped.** Only caught because a Trial Balance that no longer balances is impossible to miss. **On a debtor it would have balanced and been wrong** | ⛔ SILENT |

### And two more, found on the way

- ⛔ **`peekTallyXMLVoucherDate` and `peekTallyXMLSupersededGUIDs` read the same
  unmodelled envelope shape.** A real Tally file therefore presented **zero dated
  vouchers to the back-dated cutoff and zero superseded GUIDs to the step-up check** —
  §7.2's six doors had a seventh that only a genuine export could open. Fixed by the same
  `env.Messages()` accessor; both now see the file.
- **`SALARY PAYABLE` vs the seeded `Salary Payable`.** The ensure verbs resolved EXACTLY
  while uniqueness has been enforced on `lower(btrim(name))` since migration 0075, so the
  resolve missed, the create was refused as a duplicate, and the migration stopped — on
  its 2,522nd master. `EnsureLedger`/`EnsureLedgerWithOpening` now resolve the way
  uniqueness is enforced. `LedgerID()` stays exact on purpose.

---

## WHAT WAS BUILT BEYOND THE FIXES

- **Account group import** (`migration.importGroups`) — parents before children, without
  assuming the file is ordered that way; Tally's built-ins resolve case-insensitively
  rather than duplicating; a group whose parent is missing is **named, not invented**
  (a primary group's nature is not in the file, and guessing it moves ledgers between the
  Balance Sheet and the P&L).
- **Opening bill-wise breakups** — without them a migrated debtor has the right total and
  **no ageing at all**, which is the first report a switching business opens.
- **State codes and GSTINs that the file does not literally contain** — a real export
  records the state by NAME ("Maharashtra") and never as a code, so **all 342 registered
  parties migrated with a blank state**. Derived from the GSTIN's first two digits
  (340/342), validated against `gststate` so a retired code is refused, falling back to a
  name lookup. The GSTIN itself now comes from the **dated `LEDGSTREGDETAILS.LIST`**, not
  the flat `PARTYGSTIN` — which was stale for 3 parties and absent for 16.
- **`ImportResult.Warnings`, surfaced in the HTTP response.** The import carries on past
  what it cannot represent exactly and **says so**. 19 of 3,706 ledgers in the real file:
  a breakup that does not sum to its own opening, or one mixing Dr and Cr refs on one
  party. Total kept, breakup dropped, ledger named.
- **`maxXMLBody` 32 MiB → 128 MiB.** ⚠️ **This is the session's one judgment call and it
  is a memory trade.** The back-dating peek buffers the whole body before the handler
  reads it and the parse tree is several times the text; a body at the cap can cost over
  a gigabyte of heap. Raised to what a real export needs and no further. **The actual fix
  is a streaming import** that decodes TALLYMESSAGE by TALLYMESSAGE and never holds the
  document — that would let the cap come back down, and it is the top of the T32 backlog.

---

## THE GATES

```
go build ./... · go vet ./...                                    clean
go test ./... -timeout 60m                                       exit 0, 0 FAIL
pnpm vitest run                                                  1466 / 87 files
pnpm exec tsc --noEmit                                           clean
```

**The real-file gate** (opt-in; skips without the paths — the file is not in the repo):

```
CORED_TALLY_MASTERS_XML=…/Master.xml CORED_TALLY_VOUCHERS_XML=…/Transactions.xml \
  go test ./internal/migration/ -run RealTallyExport -v
```
```
masters: 9 groups created, 3706 ledgers ensured, 19 warnings
vouchers: 121 posted, 0 altered, 0 deleted, 0 skipped
RECONCILED: 3706 ledgers, Dr 25986501.88 = Cr 25986501.88, to the paisa
```

⛔ **Its oracle shares no code with the importer.** It re-reads the same two files and
adds up every opening balance and every voucher line itself, then requires cored's own
`TrialBalance` to match ledger by ledger. There is no JSON fixture to quietly restate.

**The CI gate** is `corpora/migration/06-tallyprime-real-shapes.xml` +
`TestTallyPrimeShapesCorpus` — anonymised, UTF-16LE with a BOM, `&#4;` in field values,
nested under IMPORTDATA, one item invoice whose revenue is only in the stock line and
whose round-off contradicts itself. **Verified to be a real gate**: reverting the sign
rule, the inventory merge or the envelope shape each fails it, with the failure naming
the shape rather than a moved total.

---

## ⛔ WHAT THIS FILE DOES **NOT** PROVE — READ BEFORE CLAIMING T32 IS DONE

1. **GST is untouched by this evidence.** The company trades GST-free and zero-rated
   grain: **not one of the 121 vouchers carries a CGST/SGST/IGST line.** The tax path,
   `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join are exactly as tested as they
   were before — i.e. against our own corpora only. **A tax-bearing real export is the
   next thing to ask a user for**, and it is a different ask from the one just answered.
2. **One month, one company, one Tally version.** 121 vouchers, all `ACTION=Create`, all
   August 2026, five voucher classes. **No Alter, no Delete, no Credit/Debit Note, no
   Contra, no stock journal, no order** appeared. The Alter/Delete routing is still
   corpus-tested only.
3. **No `STOCKITEM`, `UNIT`, `GODOWN`, `VOUCHERTYPE` or `COSTCENTRE` masters** — the
   export was accounting masters only, while the vouchers reference 227 stock lines with
   batches and godowns. **Inventory is imported as accounting value and nothing else:**
   no item, no quantity, no batch, no location crosses over. For a trader whose closing
   stock is on the Balance Sheet that is a real gap, and it is invisible in a Trial
   Balance because the value is right.
4. **Cost centres, budgets, multi-currency, payroll** — absent from the file, untested.
5. **Performance.** 3,706 masters + 121 vouchers took ~20 s against local Postgres, one
   transaction per master. A 100,000-voucher export has not been tried and the whole
   document is held in memory (see `maxXMLBody` above).

---

## THE NEXT THREE, IN ORDER

| # | Work | Why now |
|---|---|---|
| **1** | **Ask for a GST-bearing export** and re-run the gate against it | The single largest untested surface in the importer, and it is one email. Point 1 above is the exact ask |
| **2** | **Streaming import** — decode TALLYMESSAGE by TALLYMESSAGE | Lets `maxXMLBody` come back down from 128 MiB and removes the only unbounded memory cost on the API |
| **3** | **Inventory masters + quantities on import** | Point 3. Until then a migrated trader's stock exists only as a rupee value, and T9 (FIFO/LIFO, newly unblocked) has nothing real to value |

⚠️ §7.3 **B / C / D** from `HANDOFF-PARITY-AUDIT.md` are untouched by this session and
remain open. ⛔ **T18 (rule 48(4) QR) is still permanently unshippable** — the payload is
signed with the Income Tax Department's private key. Reject any session that reports it
done.
