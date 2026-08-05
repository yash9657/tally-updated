import { useEffect, useState } from "react";

import { ApiError } from "../api";
import { useClient, useCompanyContext } from "../app/CompanyContext";
import type {
  DeductorCategory,
  DeductorStatus,
  SetTDSProfileRequest,
  TCSDeclaration,
  TDSProfile,
} from "../api/types";

/**
 * F11 Company Features — the honest inventory of what this edition does.
 *
 * Tally's F11 is a screen of Yes/No feature switches. Ours is deliberately a
 * STATEMENT OF FACT rather than a set of switches: a toggle that flips a flag
 * with nothing behind it is worse than no toggle at all. Four states:
 *   • "Always on"   — structural in this build (it cannot be turned off).
 *   • "On"          — built and available FROM THE KEYBOARD, here, today.
 *   • "Server only" — the server half is built, tested and reachable over HTTP,
 *                     and there is NO screen or key that reaches it. An operator
 *                     sitting in front of this application cannot use it.
 *   • "Not built"   — the honest gap, with the reason.
 * The keys that reach a not-built feature are still BOUND (see the keymap), so
 * pressing them tells the user the same thing this screen does.
 *
 * WHY "Server only" EXISTS AS ITS OWN STATE, AND WHY IT IS NOT "On".
 * This file used to have three states, and the rule it enforced — see the
 * comment on the inventory-voucher row — was that a row flips to "On" in the
 * change that gives the operator a SCREEN, not the change that gave them a
 * route. That rule is right and it is kept. But collapsing "the engine is
 * shipped, corpus-gated and reachable at a documented URL" into the same word
 * as "we have not written it" is its own inaccuracy, and it is the one that
 * makes an accountant re-buy something they already own. So the two are now
 * separate words, and neither of them is "On".
 */

type State = "always" | "on" | "server" | "off";

const FEATURES: {
  group: string;
  items: { name: string; state: State; note: string }[];
}[] = [
  {
    group: "Accounting",
    items: [
      {
        name: "Double-entry vouchers (Contra, Payment, Receipt, Journal, Sales, Purchase)",
        state: "on",
        note: "F4–F9. Every voucher is balanced and posted through the ledger core.",
      },
      {
        name: "Edit Log / audit trail",
        state: "always",
        note: "Immutable, hash-chained voucher versions: an alter or delete writes a NEW version, never a mutation. Structural — it cannot be switched off.",
      },
      {
        name: "Bill-wise details (New Ref / Agst Ref / Advance / On Account)",
        state: "on",
        note: "Stored per posting and rolled up by the Outstandings report. Includes the BILL-WISE OPENING BREAKUP: a party's opening balance is split into the individual unpaid bills carried in, each ageing by its own due date. Outstandings and the Balance Sheet are cross-checked to the paisa.",
      },
      {
        name: "Credit Notes and Debit Notes",
        state: "on",
        note: "Alt+F6 Credit Note / Alt+F5 Debit Note. The correct way to reverse part of an invoice: a separately-dated document with its OWN numbering series, rather than altering the original and rewriting history. Reported in GSTR-1's cdnr section (table 9B) when raised on a customer, and as an ITC adjustment in GSTR-3B when raised on a supplier.",
      },
      {
        name: "Opening balances (with bill-wise breakup)",
        state: "on",
        note: "Stated as at BOOKS BEGINNING FROM — a date distinct from the financial-year start, so a mid-year migration carries its opening position in at the right date.",
      },
      {
        name: "Single-entry mode for Payment / Receipt / Contra",
        state: "on",
        // This row said "DECLARED BUT NOT HONOURED" for one release after the
        // mode actually shipped. An honesty surface that lags the product is
        // worse than no surface: it teaches people not to trust it.
        note: "These three open in SINGLE entry the way a Tally operator expects: name one account, list what it moved against, and the account's amount is the derived total (never typed). Ctrl+H switches to explicit Dr/Cr, and the choice is remembered PER VOUCHER TYPE across saves — a bulk-entry mode that reverted after every Ctrl+A would be pointless.",
      },
      {
        name: "Voucher numbering series",
        state: "on",
        note: "Per company / voucher type / financial year counter, gap-free under concurrency.",
      },
      {
        name: "Cost centres and cost categories",
        state: "off",
        note: "Not built yet — no allocation dimension on postings. On the roadmap; a real Tally concept, not something we have replaced.",
      },
      {
        name: "Multi-currency",
        state: "off",
        note: "Not built yet — the company base currency is the only one. Its SYMBOL, formal name and decimal places are configurable per company; forex rates and a second currency are not.",
      },
      {
        name: "Bank reconciliation",
        state: "on",
        note: "Gateway > Bank Reconciliation. Migration 0045 records the date the bank cleared each voucher leg, and the statement shows balance per books, the un-cleared items with their age, balance per bank, and a difference that must be exactly zero to the paisa. The Bank Date column is the only editable cell: type the date off your bank statement to tick an item off, clear the cell to un-tick it. F4 picks the account when a company has more than one; F2 dates the statement, which matters because reconciliation is normally done in arrears. STATEMENT IMPORT NOW WORKS (Alt+O), AND MATCHING STILL DOES NOT. Point Alt+O at the CSV your bank exports and its rows are stored against the account, with totals in and out; that is T4 slice 2a. MATCHING those rows to your vouchers is slice 2b and is NOT built, so every tick in the Bank Date column is still manual and the keys Tally offers for matching (F8, Alt+F8, Alt+S, F9, plus Ctrl+J and Ctrl+B) all answer 'not built' rather than doing nothing. THE IMPORT ASKS YOU TO CONFIRM HOW YOUR FILE IS READ, and that is the feature rather than a nuisance: this build ships NO built-in layout for any bank, because HDFC, ICICI and SBI all differ and a layout remembered wrongly by one column reads the running balance as the amount — at which point every figure on screen is a real number off a real statement that was never a transaction, nothing looks odd, and you reconcile against it. So the columns are proposed from your file's own header row and you confirm them. Where your file cannot settle a question the import refuses to guess: if every date in it fits both dd/mm and mm/dd, it says so and makes you choose, because picking wrongly moves the whole statement by up to eleven months while every amount stays plausible. FOUR THINGS IT CHECKS AND ONE IT ADMITS IT CANNOT. It walks the running balances to the paisa, so a row your bank's export dropped is REFUSED naming the row rather than imported with a gap; re-importing the same file adds nothing; overlapping statements (1-31 January, then 15 January-15 February) skip the rows already present instead of doubling them; and if you type the account number printed on the statement it is compared against this ledger's and a mismatch is refused, because a statement imported against the wrong account reconciles nothing and leaves two accounts wrong with every line in both still real. The one it admits: a file with no running-balance column CANNOT be balance-walked, and it says the check DID NOT RUN rather than staying silent — a check that did not run must never look like a check that passed. The account number and IFSC are now stored (migration 0064) and shown, and when they have not been typed the screen says 'not recorded' rather than showing an empty field, because a blank labelled field reads as 'this bank has no IFSC'. Note that importing a statement is NOT clerical work: it is your bank's own file of every counterparty you pay, including the rows no voucher exists for — salaries, drawings, the loan repayment — so the seeded Data Entry level is refused both the import and the imported statement, while it is still allowed to tick items off. An owner who disagrees writes one allow rule; a file that has been read cannot be un-read. THE STATEMENT NOW PRINTS (Ctrl+P), for the account you have selected. It was refused for a long time because the print route carried no ledger id and a reconciliation is always for ONE bank account; the route now takes the account explicitly, and a request that does not name one is refused rather than defaulted — printing 'the first bank account' would put a real, correctly rendered statement for the WRONG bank on paper with nothing on the page saying so. The printed statement shows the book balance, the un-cleared items with their age, the bank balance and the difference, says so in bold if it does not reconcile, and marks any item BROKEN by a later alter. Every bank account can be printed in one document by asking for all of them, and that document deliberately carries NO combined total: each statement reconciles to its own bank, and a sum across accounts — one of which may be an overdraft — ties to nothing you can check against a passbook. Note that reconciling never touches a voucher — the bank date lives outside the hash chain, because it is a fact about the bank and not about the voucher — so an ALTER after clearing is reported as BROKEN, with what was originally cleared shown beside the current figure, never silently absorbed.",
      },
      {
        name: "Interest on overdue bills",
        state: "off",
        note: "Not built. Nothing computes interest on a receivable or payable that is past its due date, and nothing posts it. Outstandings will tell you a bill is 90 days old; it will not tell you what that has cost. The day-count basis and simple-vs-compound are configuration with statutory defaults, and inventing those defaults would be worse than the gap.",
      },
      {
        name: "Budgets and budget-vs-actual",
        state: "off",
        note: "Not built. A budget is a comparison dimension rather than a posting, so it changes no voucher and no balance — which is exactly why it cannot be faked by keying journals: there is no second column for any report to compare an actual against.",
      },
    ],
  },
  {
    group: "Statutory",
    items: [
      {
        name: "GST (CGST/SGST/IGST derivation, GSTR-1, GSTR-3B)",
        state: "on",
        note: "Tax is derived and re-verified SERVER-side; the client never decides the split.",
      },
      {
        name: "e-Invoice / IRN",
        state: "server",
        // Was "On". Nothing in this application generates an IRN: there is no
        // key, no button and no client call — grep the client for "IRN" and the
        // only hits are this screen and a keymap comment. Under the doc comment
        // above, "On" means reachable FROM THE KEYBOARD, so "On" was the
        // flattering-direction error this screen exists to prevent.
        note: "BUILT, WITH NO KEY THAT REACHES IT. The generation workflow is durable and idempotent — a retried step returns the memoized result, so the provider is hit exactly once per voucher — and pinning an IRN to a voucher (POST .../vouchers/{id}/external-reference) freezes its number and hard-blocks any later alter or delete, which is the invariant that matters: you cannot quietly rewrite an invoice the government has a copy of. WHAT IT TAKES TO ACTUALLY GET ONE, AND WHY IT IS NOT A GOVERNMENT IRN: generation runs in a SEPARATE process (cmd/statutoryd) that calls back into this server, and the provider adapter it is wired with is statutory.FakeProvider — a deterministic stand-in. No Invoice Registration Portal is reachable from this build, so the IRN you can obtain here is a well-shaped fake with the whole workflow, retry classification and freeze behaviour exercised around it, not a registration. The printed invoice also carries no rule 48(4) QR code; see the printing group below.",
      },
      {
        name: "e-Way Bill",
        state: "off",
        note: "Not built yet.",
      },
      {
        name: "TDS / TCS",
        state: "on",
        // FLIPPED ON THE EVIDENCE THIS ROW REQUIRES, WHICH IS THE SHELL DRIVEN IN
        // A BROWSER — not on the test suite, which was green while the feature was
        // unusable (see below).
        //
        // What was actually driven, on a company with three real deductions:
        // Gateway lists "TDS Challans"; Enter opens it and it carries ITS OWN
        // button bar rather than falling through to the Gateway's; Alt+C opens the
        // deposit form ON THE REAL SHELL (the 13th registration point, the one
        // that shipped Alt+O dead through 459 green tests); the allocation picker
        // shows two deductions as "already deposited" with no checkbox and one as
        // selectable; and Ctrl+A is routed. The register renders a recorded
        // deposit with its allocations resolved to party names.
        //
        // AND THE WRITE COMPLETED FROM THE BROWSER, THROUGH THE REAL STEP-UP.
        // Ctrl+A first answers 403 STEPUP_REQUIRED — the seeded Owner level has
        // requires_webauthn — and the shell raises "Confirm the passkey prompt".
        // With an authenticator present the ceremony runs (the app ENROLS this
        // device when it holds none of the listed credentials, see auth/webauthn.ts)
        // and the queued write goes through: challan 05123, BSR 0004329, deposited
        // 2026-07-05, ₹9,000, recorded against the exact deduction ticked in the
        // picker. The register then lists it, and the TDS/TCS Register's filing
        // panel moves to "YOUR BOOKS ARE READY: all 3 deductions in this period
        // have a challan behind them" — because of that deposit.
        //
        // The whole loop, in one browser: pick a deduction, record the deposit,
        // and watch the thing that was blocking the return stop blocking it.
        //
        // WHY "on" AND NOT "server": "server" means built with no key. This has
        // keys. And why not "off": leaving it "Not built" after driving it with the
        // keyboard would be the understating lie this screen's header forbids —
        // which is the same mistake the note made when it denied the challan
        // register in the commit that added it. The file gap is real and stays
        // stated in the note, which is what the note is for.
        //
        // WHAT THIS NOTE GOT WRONG, recorded because this screen exists to be
        // honest and was not: it said "(2) NO CHALLAN REGISTER, so nothing tracks
        // what has actually been DEPOSITED" in the very commit that ADDS the
        // challan register and the deductor return profile, and said nothing at
        // all about the profile. An honesty surface that denies a shipped feature
        // is wrong in the understating direction, which is no more allowed here
        // than the flattering one.
        note: "TDS AND TCS BOTH WORK, THE REGISTER IS VISIBLE, AND WHAT HAS BEEN DEPOSITED IS NOW TRACKED — but no return can be FILED. TDS: derived with the s.393(1) ANNUAL AGGREGATE and catch-up (a run of sub-threshold bills crosses, and the crossing bill carries the tax for all of them), refused unless this company\'s deductor status is recorded, and stored. TCS: scrap collects 2% (the Finance Act 2026 rate), a motor-vehicle sale above ten lakh collects 1%, a buyer\'s s.394(2) declaration exempts their scrap sales, and a declaration with NO PAN triggers the PENAL 5% rather than the exemption. Both are configured on this screen, above. THE DEDUCTION REGISTER (Alt+G > TDS) lists every deduction and collection for a period with the party, section, rate and amount. THE CHALLAN REGISTER (Gateway > TDS Challans) lists what was actually PAID TO THE GOVERNMENT for one tax year — the bank branch, the deposit date, the serial, the four challan boxes and the minor head — and Alt+C on it RECORDS a deposit together with the deductions it paid, chosen from the deductions of that year that carry tax rather than keyed (the voucher id and the leg ordinal are identifiers no screen displays, and the amount can only be the tax that was deducted, so none of the three is asked for). It is the deposit and not the deduction that stops interest running, which is why this is not cosmetic. THE DEDUCTOR RETURN PROFILE — the TAN, the deductor category, the deductor\'s address and the responsible person with their own PAN and address — is on THIS screen, above, and the return sourcing layer refuses to build anything without it. BOTH are refused to the Data Entry level by default (a bare resource token against a level whose default_access is \'none\'), so a 403 on either is a normal answer and is rendered as the decision it is. WHAT IS STILL MISSING: (1) NO RETURN FILE IS EMITTED. No route serves the bytes, and the file header needs four facts nobody in this build holds: the file sequence number (unique across every file this deductor has ever sent), the preparing software\'s name, whether a regular statement was already filed for an earlier period, and its token number. They have to be ASKED for — inferring any of them would put a false statement on a tax return. THE LAYOUT IS NOT THE OBSTACLE ANY MORE, and saying it was is a claim this screen carried after it stopped being true: the prescribed Form 140 file format, version 1.0, HAS been read and is implemented, payment-code list included, and the deduction register cites the document on its own face. AND NOTHING HERE HAS BEEN VALIDATED BY THE DEPARTMENT\'S FVU, which needs a Java runtime this environment does not have, so every file-shaped thing is GENERATED, not validated. (2) A RECORDED CHALLAN CANNOT BE CORRECTED OR RE-ALLOCATED. There is no PUT, no PATCH and no DELETE: the CIN is the identity and re-posting it is a 409, never an upsert, because the second post is either a double entry or a second real deposit whose serial was mistyped and overwriting either loses money. So a deposit allocated to the wrong deduction stays that way in this build. (3) No seller-liability test for TCS, and a credit note reversing a collected sale is not handled.",
      },
      {
        name: "Payroll statutory (EPF/EPS/EDLI, ESI, salary TDS, pay run)",
        state: "server",
        // This row said "Not built yet." for a release AFTER the engine, the
        // pay-run posting and the three statutory returns had all shipped. That
        // is the understating direction, and it is still a lie: an honesty
        // surface is not allowed to be wrong in the flattering direction either.
        note: "BUILT AND POSTING, WITH NO SCREEN. The calculation core is pure and deterministic — statutory wages under the Code on Social Security, EPF/EPS/EDLI, ESI, and salary income tax under s.392 — and a pay run posts real balanced double entry through the same ledger core an invoice does (POST .../vouchers/payroll, its own ACL action so a supervisor can withhold what the company pays its staff without withholding journals). Five statements come off it: the Payroll Register, a Payslip, Form 138 (ex-24Q), the PF ECR and the ESI return. There is NO payroll screen and Ctrl+F4 does not open one, so an operator cannot key a pay run here — only an integrator over HTTP. TWO DISCLOSURES THAT COST REAL MONEY. (1) THE EDLI PREMIUM IS NOW BOOKED — 0.50% of EDLI wages with effect from 29 June 2026, under S.O. 3581(E) dated 1 July 2026, ceiling-capped so it is at most Rs 75 per member per month. It was refused until that notification was identified, and the reason it was so hard to find is worth knowing: the rate is NOT in the EDLI Scheme itself, whose para 5(1)-(2) leaves it to a separate notification decided on actuarial valuation, so reading the Scheme finds no rate at all. The row is marked MEDIUM confidence, not HIGH: the notification was read through secondary reporting, no primary Gazette text could be retrieved here, and 0.50% is in any case the superseded 1976 Scheme's rate carried forward unchanged. WHAT IS STILL REFUSED is both EPF and EDLI ADMINISTRATIVE CHARGES: no standalone rate notification exists for either under the 2026 Schemes, so the engine defers them rather than inventing a number. They are PENDING, NOT ABOLISHED — the EPF Scheme 2026 para 29(2) caps a late-return fee at 'the administrative charges payable for the relevant month', which only makes sense if the charge exists — so an amount that was Rs 500 per establishment per month under the old Scheme is accruing unbooked, and you must accrue it yourself until a rate is notified. Each pay run names it in its deferrals. Do NOT let anyone substitute S.O. 2701(E) of 29 May 2026 for it: that sets INSPECTION charges for exempted establishments, a different levy. (2) No output here has been through the NSDL/Protean FVU, the EPFO portal or the ESIC portal — none of them is reachable from this build. The files are GENERATED, not validated.",
      },
      {
        name: "GSTR-9 annual return",
        state: "off",
        note: "Not built. It is a RECONCILIATION of the year's GSTR-1 and GSTR-3B to the books rather than a fresh derivation, so it must be able to state where the three disagree instead of smoothing it — which is why it is not a report we can generate by summing twelve months and calling it done.",
      },
    ],
  },
  {
    group: "Inventory",
    items: [
      {
        name: "Stock items and units of measure (masters)",
        state: "on",
        note: "Masters exist and can be created.",
      },
      {
        name: "Item Invoice (Ctrl+H on F8 Sales / F9 Purchase) — stock actually moves",
        state: "on",
        note: "Key the GOODS — item, godown, quantity, rate, discount — and the accounting AND the GST are derived from them SERVER-side, in the same transaction that writes the stock movement. That single derivation is what stops the invoice and the stock ledger disagreeing. Quantities are fixed-4 decimals end to end, never floats. Ctrl+H switches back to the Accounting Invoice (a typed taxable value, no stock).",
      },
      {
        name: "Godowns / stock locations and batches",
        state: "on",
        note: "Every company carries a system 'Main Location'; more can be created, and a line may name a batch/lot. A line may also name NEITHER, which posts as untracked — a company that never enables multi-location is not forced to invent master rows.",
      },
      {
        name: "Stock Summary (closing quantity AND value)",
        state: "on",
        note: "Gateway > Stock Summary. NO STOCK VALUE IS EVER STORED: quantity is posted, and value is DERIVED at report time by replaying the movement ledger through a weighted-average valuer. That is why altering an April invoice changes the July closing value with no reversal code anywhere. DENIED to the Data Entry level by default, because the closing valuation rate is what the business PAID — an Owner can lift that in Users & Roles.",
      },
      {
        name: "Negative stock",
        state: "always",
        note: "REFUSED by default: a voucher that would take a position below zero is rejected by a deferred database constraint, not merely by the form. A company can opt in per-company. Structural — the check cannot be bypassed by a different write path, because ledger.post is the only one there is.",
      },
      {
        name: "FIFO / LIFO / batch-wise valuation",
        state: "off",
        note: "Not built. v1 values everything at WEIGHTED AVERAGE (Tally's default) behind a valuer interface whose state is a strict subset of what FIFO needs, so FIFO is a new arm plus fixtures rather than a schema change. An item whose valuation_method names an unimplemented valuer FAILS LOUDLY rather than being quietly valued at average under a FIFO heading.",
      },
      {
        name: "Inventory vouchers (Delivery Note, Receipt Note, Stock Journal, Physical Stock)",
        state: "on",
        // This row read "Not built" for one release WITH the server half
        // shipped, and said so plainly: the operator could not key one. It
        // flips in the same change that gives them a screen, not the change
        // that gave them a route.
        note: "Alt+F8 Delivery Note / Alt+F9 Receipt Note / Alt+F7 Stock Journal / Alt+F10 Physical Stock. Goods move and NOTHING is posted to the ledger — a warehouse ships hours or weeks before the billing department raises the invoice, and a system that can only record a movement by also recording revenue forces the operator to invent an invoice or key nothing. The direction is fixed by the document (a Receipt Note cannot ship goods out); a Stock Journal posts each keyed transfer as a PAIR of movements, so a transfer cannot become a stock write-off; and Physical Stock sends the COUNT — the server derives the correction against the books, so the count can never be a delta measured against a stale read. Physical Stock is REFUSED to the Data Entry level by rule (migration 0031): it is the only document that makes the books agree with a number a human asserts, so it is a supervisor's key, and the screen states that refusal rather than showing a blank form.",
      },
      {
        name: "Order processing (Sales / Purchase Orders) and Bill of Materials",
        state: "off",
        note: "Not built, and not modelled at all rather than modelled wrong. An order is a COMMITTED quantity, not a movement: the stock ledger has exactly one primitive (a positive quantity plus a direction) and every reader SUMS it, so an order recorded as a movement would let a quotation deplete the warehouse and then refuse the real sale for negative stock. It needs its own table, its own fulfilment linkage, and a second pair of columns BESIDE the physical balance in every stock report. Ctrl+F8 / Ctrl+F9 say so when pressed.",
      },
      {
        name: "Stock groups and compound units (Box of 12)",
        state: "off",
        note: "Not modelled. Stock items are flat and units are simple symbols with no conversion.",
      },
      {
        name: "Closing stock on the Balance Sheet",
        state: "on",
        // This row said "NOT WIRED" after it was wired. See the doc comment: a
        // stale row in the UNDERSTATING direction still teaches people not to
        // trust the screen.
        note: "WIRED, THROUGH THE SAME DERIVATION THE STOCK SUMMARY USES. The Balance Sheet's Closing Stock line under Current Assets > Stock-in-Hand and the Stock Summary's total both fold the replayed movement ledger through one function, so they cannot disagree by construction rather than by convention. The Profit & Loss carries the matching Trading-account pair, and the opening term is taken at the day BEFORE the period starts — which is what makes two adjoining periods sum to the whole. NOTE WHAT DOES NOT PROVE THIS: the sheet FOOTS for any value of closing stock, right or wrong, because the same figure lands on both sides and cancels. So the gate is not 'it balances' but 'the Balance Sheet's Closing Stock equals the Stock Summary total, to the byte, at every month-end' — two independent report paths over the same books, checked by verify:demo on the live stack. A position dated before the books begin carries no closing stock at all.",
      },
    ],
  },
  {
    group: "AI-native (beyond Tally)",
    items: [
      {
        name: "Bill Inbox — OCR bill to draft purchase voucher",
        state: "on",
        note: "Gateway > Bill Inbox. The AI plane drafts; a human accepts. It has NO write path to the ledger by construction.",
      },
      {
        name: "Ask your books — plain-English queries",
        state: "on",
        note: "Gateway > Query. Natural language compiles to a closed query DSL, then to parameterized, read-only, tenant-scoped SQL.",
      },
      {
        name: "Cash-flow projection and anomaly detection",
        state: "on",
        note: "Deterministic projection + rule-based anomalies, with an optional statistical forecast.",
      },
    ],
  },
  {
    group: "Users and security (Tally's Security Control)",
    items: [
      {
        name: "Multiple users per company, with security levels",
        state: "on",
        note: "Gateway > Users & Roles. An Owner invites a sign-in subject at a level; the invitee becomes a member when they next sign in. Every request is then authorised server-side against that level.",
      },
      {
        name: "Custom security levels (Disallow / Allow rules)",
        state: "on",
        note: "Beyond the two levels every company is created with (Owner, Data Entry), a level can be defined with its own Allow/Disallow rules per action and resource. Creating one is API-only today; this screen's Users & Roles page lists them.",
      },
      {
        name: "Switch company (F3)",
        state: "on",
        note: "F3 lists the companies YOU are a member of, with the level you hold in each, and re-scopes the session to the one you pick.",
      },
      {
        name: "WebAuthn step-up for writes",
        state: "on",
        note: "A level may require a second factor for writes only: reading your own books never asks, changing them does. Granting someone access counts as a write.",
      },
      {
        name: "Back-dated entry cutoff",
        state: "on",
        note: "A level can be blocked from writing before a date — how a business stops staff back-dating into a filed period. It binds Owners too.",
      },
      {
        name: "Password login (with account lockout)",
        state: "on",
        // This row read "Password / SSO login — NOT BUILT: this session
        // authenticates as a fixed development subject." Password login shipped;
        // SSO did not. Rather than leave one row half-true in both directions,
        // the two are now stated separately — which is also the only way the SSO
        // gap stays visible instead of being absorbed by a green badge.
        note: "There is a sign-in screen, and it is the first thing this application shows: user name + password, keyboard-only, Enter submits. The password is verified SERVER-side against a PBKDF2 verifier stored per user, and the four ways a login can fail — unknown user, no password set, wrong password, deactivated user — return a BYTE-IDENTICAL 401 with the KDF run either way, so the login cannot be used to find out who has an account. Five failures in fifteen minutes locks the subject for fifteen, durably, and the screen says until when. A login NEVER creates the account it is failing to authenticate. TWO LIMITS WORTH KNOWING: a password-only token carries no second factor, so on a company whose Owner level requires WebAuthn (the default every company is seeded with) it can READ but must step up before it can WRITE — the step-up ceremony is built and the shell performs it on the first refused write. And there is no production path to set a FIRST password: changing your own password needs a token you already have, so a newly invited subject must be issued one by an identity provider (or, in development, by the dev token endpoint) before they can ever log in. An admin cannot set someone else's password, deliberately.",
      },
      {
        name: "Single sign-on (SSO / OIDC login)",
        state: "off",
        note: "NOT BUILT as a login. What exists is the VERIFY half: point the server at an OIDC provider's JWKS and it will accept that provider's tokens — keys cached by kid, fail-closed on a fetch failure, asymmetric keys only, a missing expiry rejected, and the issuer pinned. There is no sign-in flow in this application that will fetch you such a token: no redirect, no authorization-code exchange, no provider button on the sign-in screen. A DISCLOSED GAP IN THE VERIFY HALF TOO: the audience claim is not checked, so any token from a trusted issuer's realm is accepted regardless of which client it was minted for — deploy that realm with trusted clients only.",
      },
    ],
  },
  {
    group: "Printing and document output",
    items: [
      {
        name: "Printed documents (PDF) — tax invoice, bill of supply, delivery note, receipt, payment, credit and debit notes",
        state: "on",
        note: "Ctrl+P on the voucher — including one you have just saved with Ctrl+A, which is the case that matters. PDFs are generated SERVER-side by a PDF writer in this codebase using only the base-14 fonts — no font files, no external library, and streams left UNCOMPRESSED so the bytes of a printed invoice are diffable and greppable and the golden tests can assert on the document itself rather than on a screenshot of it. The tax invoice is laid out against Rule 46's mandatory particulars, and an invoice missing one (the place of supply, or the RECIPIENT'S ADDRESS under 46(d)) is refused rather than printed with a blank — and the refusal lists EVERY missing particular at once, each with its clause, so filling the document in is one pass rather than a conversation. A Sales voucher charging no tax prints as a BILL OF SUPPLY under Rule 49 instead. SIX CLASSES PRINT: sales, credit note, debit note, receipt, payment and delivery note. The other seven refuse with the reason — a purchase bill is the SUPPLIER'S document not ours, a contra or journal is issued to nobody, a receipt note arrived on the supplier's own challan, and a stock journal or physical stock never leaves the business. Alt+P also prints, because ERP 9 used it and TallyPrime still binds it, but it says that the print OPTIONS menu (copies, page range) is not built and that it printed with defaults. The PDF opens in a browser tab, where that viewer's own print command takes over: a web page cannot silently drive a printer, and what matters is that the bytes you print are the server's Rule 46 document rather than a screenshot of the screen — which, until this key existed, is exactly what Ctrl+P produced, because the combo fell through to the browser. verify:demo prints the demo company's first invoice on the live stack and greps the resulting bytes for the invoice number and the company GSTIN, so 'a PDF came back' is not mistaken for 'the right document came back'.",
      },
      {
        name: "Rule 48(4) QR code on a printed e-invoice",
        state: "off",
        // Added in the change that shipped printing, not a release later.
        // Without this row, a reader who saw an IRN on the page would
        // reasonably conclude the paper that comes out is a compliant
        // e-invoice. It is not, and the missing half is not visible on it.
        note: "NOT PRINTED, AND NOT GENERATED — AND THE DOCUMENT SAYS SO ON ITS FACE. Once the e-invoice workflow has registered a document, the printed invoice carries its IRN and acknowledgement number at the top of every page, in a monospaced face because a 64-character hex string is something a human occasionally has to compare character by character. What it cannot carry is the Quick Response code rule 48(4) requires beside them: that QR encodes the SIGNED payload the Invoice Registration Portal hands back, no IRP call can be made from this build, and a QR generated from anything else would be a forgery of a government signature. So instead of a silently incomplete sheet, any invoice carrying an IRN prints this line, in bold, under the IRN — 'QR code not printed - this copy is NOT the e-invoice to be issued under rule 48(4); obtain the signed QR from the IRP' — because the failure mode that costs money is the one where the paper LOOKS complete and the person at the counter hands it over. The mandatory-particulars validator likewise states the gap where the check would have gone rather than pretending to check it, and the clause letter for the QR particular is deliberately NOT cited (believed to be 46(r), unverifiable from here — a wrong clause letter in a refusal message is a false citation shown to an auditor).",
      },
      {
        name: "Printed statements (PDF) — Trial Balance, Balance Sheet, P&L, Day Book, Outstandings, Stock Summary, Payroll Register, Payslip, Bank Reconciliation",
        state: "on",
        note: "Ctrl+P on the report. Nine statements print; SEVEN of them have a screen here (Trial Balance, Balance Sheet, P&L, Day Book, Outstandings, Stock Summary, Bank Reconciliation) and the two payroll ones are server-only, because there is no payroll screen to press the key on. The Bank Reconciliation Statement is the one that needs to be told WHICH bank account, and it refuses rather than picking one — see the banking group above. EVERY OTHER REPORT NAME RETURNS 501 NOT IMPLEMENTED — Cash Flow, the anomaly list, GSTR-1, GSTR-3B, the reconciliation queue, the four inventory reports at other grains, ITC reconciliation and the IMS inbox are all readable on screen and none of them prints; pressing Ctrl+P on one shows that 501 rather than appearing to work. A 501 is deliberate: a print route that quietly returned an empty page would be worse. TWO THINGS WORTH KNOWING. Printing asks the SERVER for the statement, so a screen you have filtered with Ctrl+F or narrowed with F12 still prints in full — Alt+E export is the one that writes what is on screen; and on a drill-down Ctrl+P prints the PARENT statement, because that is all the route offers, and it says so when it does. Printing is its own ACL action, distinct from displaying, so a level may be allowed to READ a statement and refused permission to PRINT it — the seeded Data Entry level is granted no report-print rule at all, so it is refused every one of them, and the refusal is now visible instead of silent. Because printing is not a write it does NOT inherit the back-dated-entry cutoff or the WebAuthn step-up: reading your own books, even onto paper, never asks for a second factor.",
      },
      {
        name: "Export (Excel / CSV / XML / e-mail a document)",
        state: "off",
        note: "Not built. The action token exists in the ACL vocabulary and is UNMAPPED, which means it falls to default-DENY for every level — nothing can be exported by anybody, rather than exports being quietly permitted by whatever rule happened to cover display. Tally XML IMPORT is a separate, built thing; this row is about getting data out.",
      },
      {
        name: "Per-company print templates (logo, address block, footer, declarations)",
        state: "off",
        note: "Not built as a UI. The default document layout lives in CODE, so a company created a minute ago prints correctly with zero configuration rows — and the per-company override table holds only DIFFERENCES from it. Nothing in this application yet writes an override, so every company prints the built-in layout.",
      },
    ],
  },
  {
    group: "Data and operations",
    items: [
      {
        name: "Backup / restore, split company data",
        state: "off",
        note: "Not built, and absent is the honest state to be in until it can be TENANT-EXACT: a restore that leaks one company's rows into another is worse than having no backup at all, because it is silent and it is discovered by the wrong person. Use your Postgres backups meanwhile.",
      },
      {
        name: "Offline entry with sync",
        state: "server",
        note: "The sequencer, the idempotent commit path and the partition fuzzer exist server-side and a client can commit a batch of vouchers keyed while disconnected. There is no offline-capable client in this build — this application needs the server — so nothing here can be keyed on a train.",
      },
    ],
  },
];

const BADGE: Record<State, { text: string; className: string }> = {
  always: { text: "Always on", className: "text-tally-title" },
  on: { text: "On", className: "text-tally-title" },
  server: { text: "Server only", className: "text-tally-bar" },
  off: { text: "Not built", className: "text-tally-fieldText" },
};

/**
 * TDS Deductor Details — the FIRST of the three REAL CONTROLS on this screen.
 *
 * Tally puts exactly this behind F11 > Enable TDS > "TDS Deductor Details"
 * (RESEARCH.md:692), which asks for TAN, DEDUCTOR TYPE and the responsible
 * person. `entity_category` is that deductor type.
 *
 * WHY A CONTROL BELONGS ON A SCREEN WHOSE HEADER SAYS IT IS A STATEMENT OF FACT.
 * The rule that header states is "a toggle that flips a flag with nothing behind
 * it is worse than no toggle at all". This has everything behind it: until a row
 * exists here the TDS post path REFUSES every Purchase, Payment and Journal
 * carrying a `tds` block. It is the opposite of a decorative switch — it is the
 * switch the rest of the feature was waiting for.
 *
 * ⚠️ THE YEAR IS THE CONFUSING PART AND THE UI SAYS SO OUT LOUD. s.402(37)(b)
 * tests turnover "during the tax year IMMEDIATELY PRECEDING", so the 2025-26 row
 * is what governs payments made in 2026-27. Every row and the form itself state
 * the governed year, because an operator who keys the payment's own year gets a
 * refusal that looks like a bug.
 */
function DeductorStatusPanel() {
  const client = useClient();
  const { company } = useCompanyContext();
  const companyID = company?.id ?? "";

  const [rows, setRows] = useState<DeductorStatus[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [activities, setActivities] = useState<string[]>([]);
  const [loadError, setLoadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);

  // Default to the year that governs payments in the CURRENT tax year, which is
  // the year an operator hitting the refusal actually needs.
  const now = new Date();
  const currentTaxYear =
    now.getMonth() >= 3 ? now.getFullYear() : now.getFullYear() - 1;
  const [taxYear, setTaxYear] = useState(String(currentTaxYear - 1));
  const [category, setCategory] = useState("");
  const [turnover, setTurnover] = useState("");
  const [activity, setActivity] = useState("BUSINESS");

  // The four categories s.402(11)(l) / s.402(37)(b) subject to the turnover
  // test. Everything else is liable unconditionally and must record NO figure —
  // so the fields are hidden rather than merely optional, because a turnover
  // typed against a COMPANY is a 400, not a harmless extra.
  const needsTurnover = ["INDIVIDUAL", "HUF", "AOP", "BOI"].includes(category);

  async function load() {
    if (!companyID) return;
    try {
      const res = await client.listDeductorStatus(companyID);
      setRows(res.deductor_statuses);
      setCategories(res.entity_categories);
      setActivities(res.prior_year_activity);
      setLoadError("");
    } catch (e) {
      // A Data Entry level is REFUSED this route by design, so a 403 here is
      // correct behaviour and must read as such rather than as a broken screen.
      setLoadError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void load();
  }, [companyID]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setSaveError("");
    setSaved("");
    try {
      const res = await client.setDeductorStatus(companyID, Number(taxYear), {
        entity_category: category,
        // Sent ONLY when the test applies. An empty string means "not
        // applicable" and is stored as NULL; "0" would assert this company
        // turned over nothing, which is a different and false claim.
        prior_year_turnover_rupees: needsTurnover ? turnover : "",
        prior_year_activity: needsTurnover ? activity : "",
        recorded_by: "owner",
      });
      setSaved(
        `Recorded for ${res.tax_year_label} — governs payments made in ${res.governs_payments_in_label}.`,
      );
      await load();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-4" data-testid="deductor-status-panel">
      <h2 className="font-bold text-tally-title">
        TDS Deductor Details (s.402(11) / s.402(37))
      </h2>
      <p className="mt-1 max-w-3xl text-xs">
        Whether THIS company is required to deduct tax at source. Until it is
        recorded, every Purchase, Payment or Journal carrying TDS is refused —
        because &quot;we do not know whether you must deduct&quot; is not
        &quot;you need not deduct&quot;, and it is the deductor who carries the
        shortfall plus interest.{" "}
        <span className="text-tally-title">
          The turnover test reads the tax year IMMEDIATELY PRECEDING the
          payment, so the {currentTaxYear - 1}-
          {String(currentTaxYear % 100).padStart(2, "0")} row is what governs
          payments you make in {currentTaxYear}-
          {String((currentTaxYear + 1) % 100).padStart(2, "0")}.
        </span>
      </p>

      {loadError !== "" ? (
        <p className="mt-2 max-w-3xl text-xs text-tally-bar">
          Cannot read the deductor status: {loadError}
        </p>
      ) : (
        <>
          {rows.length === 0 ? (
            <p className="mt-2 text-xs text-tally-bar">
              Nothing recorded yet — TDS vouchers will be refused.
            </p>
          ) : (
            <table className="mt-2 w-full max-w-4xl text-sm">
              <tbody>
                {rows.map((r) => (
                  <tr key={r.tax_year_start} className="align-top">
                    <td className="w-28 whitespace-nowrap py-0.5 pr-3 text-tally-title">
                      {r.tax_year_label}
                    </td>
                    <td className="w-48 py-0.5 pr-3">{r.entity_category}</td>
                    <td className="py-0.5 text-xs">
                      governs payments in {r.governs_payments_in_label}
                      {r.prior_year_turnover_rupees !== ""
                        ? ` · prior-year turnover ₹${r.prior_year_turnover_rupees} (${r.prior_year_activity})`
                        : " · liable whatever its turnover"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          <form onSubmit={save} className="mt-3 max-w-3xl text-sm">
            <div className="flex flex-wrap items-end gap-3">
              <label className="flex flex-col">
                <span className="text-xs">Tax year beginning</span>
                <input
                  aria-label="Tax year beginning"
                  className="w-24 border border-tally-fieldText bg-tally-field px-1"
                  value={taxYear}
                  onChange={(ev) => setTaxYear(ev.target.value)}
                />
              </label>
              <label className="flex flex-col">
                <span className="text-xs">Deductor type</span>
                <select
                  aria-label="Deductor type"
                  className="border border-tally-fieldText bg-tally-field px-1"
                  value={category}
                  onChange={(ev) => setCategory(ev.target.value)}
                >
                  <option value="">— select —</option>
                  {categories.map((c) => (
                    <option key={c} value={c}>
                      {c}
                    </option>
                  ))}
                </select>
              </label>
              {needsTurnover && (
                <>
                  <label className="flex flex-col">
                    <span className="text-xs">Prior-year turnover (₹)</span>
                    <input
                      aria-label="Prior-year turnover"
                      className="w-32 border border-tally-fieldText bg-tally-field px-1"
                      value={turnover}
                      onChange={(ev) => setTurnover(ev.target.value)}
                    />
                  </label>
                  <label className="flex flex-col">
                    <span className="text-xs">Business or profession</span>
                    <select
                      aria-label="Business or profession"
                      className="border border-tally-fieldText bg-tally-field px-1"
                      value={activity}
                      onChange={(ev) => setActivity(ev.target.value)}
                    >
                      {activities.map((a) => (
                        <option key={a} value={a}>
                          {a}
                        </option>
                      ))}
                    </select>
                  </label>
                </>
              )}
              <button
                type="submit"
                disabled={busy || category === ""}
                className="border border-tally-title px-2 py-0.5 text-tally-title disabled:opacity-50"
              >
                {busy ? "Saving…" : "Record"}
              </button>
            </div>
          </form>
          {saveError !== "" && (
            <p className="mt-2 max-w-3xl text-xs text-tally-bar">{saveError}</p>
          )}
          {saved !== "" && (
            <p className="mt-2 max-w-3xl text-xs text-tally-title">{saved}</p>
          )}
        </>
      )}
    </div>
  );
}

/* ==========================================================================
 * TDS RETURN DEDUCTOR PROFILE (migration 0070) — the SECOND real control here.
 * ========================================================================== */

/**
 * Annexure 3's "Other" ministry code, which BH field 58 exists to expand.
 *
 * ⚠️ WHY THIS ONE LITERAL IS HERE WHEN THE CATEGORY LIST DELIBERATELY IS NOT.
 * The sixteen deductor categories and their two conditional rules are SERVED by
 * GET /tds-profile precisely so no copy of them can drift (see the handler
 * header), and this file holds none of them. Annexure 3's ministry list is NOT
 * served by that route and is not a vocabulary this screen offers a picker for:
 * the ministry code is keyed. "99" is the single sentinel the server states as
 * an EQUIVALENCE — the code is 99 exactly when field 58 names the ministry — and
 * it is used here only to decide whether to SHOW field 58, never to validate.
 * The authority is still the server's check; the field's value is cleared
 * whenever the code is not 99, so a forbidden value can never be posted.
 */
const MINISTRY_OTHER_CODE = "99";

/** One statutory address block as the return numbers it: five lines, no blob. */
interface AddressForm {
  line1: string;
  line2: string;
  line3: string;
  line4: string;
  line5: string;
  country: string;
  state_code: string;
  pincode: string;
}

interface ProfileForm {
  tan: string;
  pan: string;
  deductor_name: string;
  deductor_category: string;
  address: AddressForm;
  email: string;
  contact_country_code: string;
  contact_number: string;
  state_name_code: string;
  ministry_code: string;
  ministry_other: string;
  ain: string;
  gstin: string;
  responsible: {
    name: string;
    designation: string;
    pan: string;
    address: AddressForm;
    email: string;
    contact_country_code: string;
    contact_number: string;
  };
}

/**
 * An empty form. The two non-empty values are the COLUMN DEFAULTS migration 0070
 * declares (country 'India', calling code '91') and not guesses — and the
 * deductor category is deliberately NOT among them: pre-selecting a letter would
 * assert on the company's behalf who is filing the return.
 */
function emptyAddressForm(): AddressForm {
  return {
    line1: "",
    line2: "",
    line3: "",
    line4: "",
    line5: "",
    country: "India",
    state_code: "",
    pincode: "",
  };
}

function emptyProfileForm(): ProfileForm {
  return {
    tan: "",
    pan: "",
    deductor_name: "",
    deductor_category: "",
    address: emptyAddressForm(),
    email: "",
    contact_country_code: "91",
    contact_number: "",
    state_name_code: "",
    ministry_code: "",
    ministry_other: "",
    ain: "",
    gstin: "",
    responsible: {
      name: "",
      designation: "",
      pan: "",
      address: emptyAddressForm(),
      email: "",
      contact_country_code: "91",
      contact_number: "",
    },
  };
}

function addressFormFrom(a: TDSProfile["address"]): AddressForm {
  return {
    line1: a.line1,
    line2: a.line2 ?? "",
    line3: a.line3 ?? "",
    line4: a.line4 ?? "",
    line5: a.line5 ?? "",
    country: a.country,
    state_code: a.state_code,
    pincode: a.pincode,
  };
}

/**
 * The form is seeded from the row that EXISTS, because this is an overwrite of
 * current state rather than a fresh append: an operator changing the responsible
 * person must not have to re-key the TAN and both addresses to do it.
 */
function profileFormFrom(p: TDSProfile): ProfileForm {
  return {
    tan: p.tan,
    pan: p.pan,
    deductor_name: p.deductor_name,
    deductor_category: p.deductor_category,
    address: addressFormFrom(p.address),
    email: p.email ?? "",
    contact_country_code: p.contact_country_code,
    contact_number: p.contact_number,
    state_name_code: p.state_name_code ?? "",
    ministry_code: p.ministry_code ?? "",
    ministry_other: p.ministry_other ?? "",
    ain: p.ain ?? "",
    gstin: p.gstin ?? "",
    responsible: {
      name: p.responsible.name,
      designation: p.responsible.designation,
      pan: p.responsible.pan,
      address: addressFormFrom(p.responsible.address),
      email: p.responsible.email ?? "",
      contact_country_code: p.responsible.contact_country_code,
      contact_number: p.responsible.contact_number,
    },
  };
}

function Field({
  label,
  value,
  onChange,
  hint,
  width = "w-40",
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  hint?: string;
  width?: string;
  placeholder?: string;
}) {
  return (
    <label className="flex flex-col">
      <span className="text-xs">{label}</span>
      <input
        aria-label={label}
        placeholder={placeholder}
        className={`${width} border border-tally-fieldText bg-tally-field px-1`}
        value={value}
        onChange={(ev) => onChange(ev.target.value)}
      />
      {hint !== undefined && (
        <span className="mt-0.5 max-w-xs text-[10px] text-tally-fieldText">
          {hint}
        </span>
      )}
    </label>
  );
}

/**
 * Five numbered lines, a country/region, a two-digit code and a pincode.
 *
 * ⚠️ IT SAYS WHAT IT DOES NOT CHECK. The state code is an Annexure 1 CODE and
 * both this screen and the server check its SHAPE only — the list of 37 codes
 * lives in the return emitter, which neither the write path nor this form can
 * see. So the form must never tell an operator a code is valid; "saved" is all
 * this earns, and whether a return can actually be built is what the TDS
 * register's return-file status answers.
 */
function AddressFields({
  who,
  value,
  onChange,
}: {
  who: string;
  value: AddressForm;
  onChange: (a: AddressForm) => void;
}) {
  const set = (k: keyof AddressForm) => (v: string) =>
    onChange({ ...value, [k]: v });
  return (
    <div className="mt-1 flex flex-wrap items-start gap-3">
      <Field
        label={`${who} address line 1`}
        value={value.line1}
        onChange={set("line1")}
        hint="flat / door / block number — mandatory"
      />
      <Field
        label={`${who} address line 2`}
        value={value.line2}
        onChange={set("line2")}
      />
      <Field
        label={`${who} address line 3`}
        value={value.line3}
        onChange={set("line3")}
      />
      <Field
        label={`${who} address line 4`}
        value={value.line4}
        onChange={set("line4")}
      />
      <Field
        label={`${who} address line 5`}
        value={value.line5}
        onChange={set("line5")}
      />
      <Field
        label={`${who} country`}
        value={value.country}
        onChange={set("country")}
        width="w-28"
      />
      <Field
        label={`${who} state code`}
        value={value.state_code}
        onChange={set("state_code")}
        width="w-16"
        placeholder="29"
        hint="two-digit Annexure 1 CODE, never the state name. Only its shape is checked here."
      />
      <Field
        label={`${who} pincode`}
        value={value.pincode}
        onChange={set("pincode")}
        width="w-24"
        placeholder="560001"
      />
    </div>
  );
}

/**
 * TDS RETURN DEDUCTOR PROFILE — who is FILING the return (migration 0070).
 *
 * Tally puts exactly this behind F11 > Enable TDS > "TDS Deductor Details"
 * (RESEARCH.md:692), which asks for the TAN, the deductor type AND the person
 * responsible. The panel above captures the first question that dialog asks;
 * this one captures the rest of it, which is everything the return's deductor
 * block needs. It lives here rather than on a screen of its own for that
 * reason — and, usefully, `features` is already a registered screen, so this
 * needs none of the screen-registration points a new report would.
 *
 * ⛔ THE TWO "DEDUCTOR CATEGORY" FIELDS ON THIS SCREEN ARE NOT THE SAME FIELD.
 * They are different columns in different tables with different vocabularies,
 * and migration 0070 records that mapping between them is "a DECISION, not a
 * derivation" — so nothing here derives one from the other, and neither panel
 * pre-fills the other:
 *
 *   DeductorStatusPanel  -> company_deductor_status (0049). `entity_category`,
 *                           one of COMPANY / INDIVIDUAL / HUF / ..., recorded
 *                           PER TAX YEAR, answering "must this company deduct
 *                           at all" under s.402(11) / s.402(37).
 *   THIS panel           -> company_tds_profile (0070). The Annexure 4
 *                           `deductor_category`, a single LETTER (A, S, K, ...)
 *                           that goes in BH field 32, recorded PER COMPANY,
 *                           answering "who is filing this return".
 *
 * ⚠️ IT IS CURRENT STATE, NOT HISTORY, AND THE UI SAYS SO WHERE IT IS SEEN.
 * There is one row per company and saving OVERWRITES it. The consequence
 * migration 0070 writes down: re-emitting a return for an old quarter after the
 * responsible person changes produces a file naming the NEW person. That is
 * correct for a fresh filing and wrong as a reproduction of what was filed, so
 * the artefact to keep is the emitted file, not a history of this table. An
 * operator cannot be expected to discover that from a form that looks like the
 * append-per-year one directly above it.
 *
 * ⚠️ THE CONDITIONAL FIELDS ARE DRIVEN BY THE SERVED FLAGS, NOT BY A LIST HERE.
 * The Annexure 5 state code is mandatory for some categories and FORBIDDEN for
 * the rest; the Annexure 3 ministry is mandatory for some and forbidden for
 * others. Both directions are 400s — a hidden-but-required field and a
 * shown-but-forbidden one fail equally — so visibility comes from
 * `state_name_code_required` / `ministry_required` / `ministry_allowed` on the
 * category the server served, and the payload CLEARS whatever is not shown.
 */
function TDSReturnProfilePanel() {
  const client = useClient();
  const { company } = useCompanyContext();
  const companyID = company?.id ?? "";

  const [categories, setCategories] = useState<DeductorCategory[]>([]);
  const [panNotRequired, setPanNotRequired] = useState("");
  const [profile, setProfile] = useState<TDSProfile | null>(null);
  const [recorded, setRecorded] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [denied, setDenied] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [saveDenied, setSaveDenied] = useState(false);
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState<ProfileForm>(emptyProfileForm);

  useEffect(() => {
    let live = true;
    if (!companyID) return;
    void (async () => {
      try {
        const res = await client.getTDSProfile(companyID);
        if (!live) return;
        setCategories(res.deductor_categories);
        setPanNotRequired(res.pan_not_required_literal);
        setProfile(res.profile);
        setRecorded(res.profile_recorded);
        // A company with no row is a 200 with a null profile, NOT a 404 — the
        // ordinary state of a company that has not filled the form in, and the
        // response still carries the vocabulary the empty form is drawn from.
        if (res.profile !== null) setForm(profileFormFrom(res.profile));
        setLoadError("");
        setDenied(false);
      } catch (e) {
        if (!live) return;
        // A 403 IS A NORMAL ANSWER HERE, BY DESIGN: this row carries a named
        // individual's PAN, so the seeded Data Entry level is refused it with no
        // rule written. It is a decision and must read as one — never as a
        // broken panel, and never swallowed into a blank form.
        setDenied(e instanceof ApiError && e.status === 403);
        setLoadError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      live = false;
    };
  }, [client, companyID]);

  const selected = categories.find((c) => c.code === form.deductor_category);
  // The description shown against the RECORDED row is looked up from that row's
  // own letter, never from the picker: an operator part-way through changing the
  // category would otherwise see the stored letter captioned with the meaning of
  // a different one.
  const recordedCategory =
    profile === null
      ? undefined
      : categories.find((c) => c.code === profile.deductor_category);
  // BOTH DIRECTIONS COME FROM THE SERVER'S FLAGS. `ministry_required` implies
  // allowed; a category that merely allows one gets an optional field.
  const showStateName = selected?.state_name_code_required === true;
  const showMinistry =
    selected !== undefined &&
    (selected.ministry_required || selected.ministry_allowed);
  const showMinistryOther =
    showMinistry && form.ministry_code.trim() === MINISTRY_OTHER_CODE;

  const set = <K extends keyof ProfileForm>(k: K) => (v: ProfileForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }));
  const setResponsible =
    <K extends keyof ProfileForm["responsible"]>(k: K) =>
    (v: ProfileForm["responsible"][K]) =>
      setForm((f) => ({ ...f, responsible: { ...f.responsible, [k]: v } }));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setSaveError("");
    setSaveDenied(false);
    setSaved("");
    try {
      // THE PAYLOAD CLEARS WHAT IS NOT SHOWN. A conditional value left behind
      // from a category the operator changed away from is a 400 (the check is
      // conditional in both directions), and the whole row is PUT rather than
      // patched precisely so a stale value cannot survive.
      const req: SetTDSProfileRequest = {
        ...form,
        state_name_code: showStateName ? form.state_name_code : "",
        ministry_code: showMinistry ? form.ministry_code : "",
        ministry_other: showMinistryOther ? form.ministry_other : "",
        recorded_by: "owner",
      };
      const res = await client.setTDSProfile(companyID, req);
      setProfile(res.profile);
      setRecorded(res.profile_recorded);
      // Seeded from the READ-BACK, so the normalisation is visible: a TAN keyed
      // in lower case comes back in capitals, and the operator sees the value
      // the return will actually carry rather than the one they typed.
      setForm(profileFormFrom(res.profile));
      setSaved(
        `Saved. Every TDS return built for this company from now on carries THIS deductor block, including a return for a quarter that has already been filed — re-emitting an old quarter after the responsible person changes produces a file naming ${res.profile.responsible.name}. Keep the file you filed; this table keeps no history.`,
      );
    } catch (err) {
      setSaveDenied(err instanceof ApiError && err.status === 403);
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const heading = (
    <>
      <h2 className="font-bold text-tally-title">
        TDS Return Deductor Profile — who is FILING (Annexure 4)
      </h2>
      <p className="mt-1 max-w-3xl text-xs">
        The deductor block of the TDS return: the TAN and the name held against
        it, the Annexure 4 category, both statutory addresses and the PERSON
        answerable for the deduction. Until it is on record no return can be
        built at all — the return refuses on this before it looks at a single
        challan or deduction.{" "}
        <span className="text-tally-title">
          The Annexure 4 category asked for here is NOT the s.402(11) deductor
          type in the panel above: that one answers &quot;must this company
          deduct&quot; and is recorded per tax year, this one answers &quot;who
          is filing&quot; and is a single letter recorded once per company.
          Mapping between them is a decision, so neither fills in the other.
        </span>
      </p>
    </>
  );

  if (denied) {
    // The loadError treatment the panel above uses, with the reason stated: a
    // refusal that reads as an outage gets escalated as a bug.
    return (
      <div className="mt-4" data-testid="tds-return-profile-panel">
        {heading}
        <p className="mt-2 max-w-3xl text-xs text-tally-bar">
          You do not have permission to read or record the deductor profile:{" "}
          {loadError}
        </p>
        <p className="mt-1 max-w-3xl text-xs">
          That is the intended answer rather than a fault. This row carries a
          named individual&apos;s PAN — the person who is personally answerable
          for the deduction — so it is refused to the Data Entry level by
          default, with no rule needed to refuse it. An owner who wants it
          granted adds{" "}
          <span className="text-tally-title">allow display tds-profile</span>{" "}
          (and <span className="text-tally-title">allow alter tds-profile</span>{" "}
          to let it be changed) in Users &amp; Roles.
        </p>
      </div>
    );
  }

  return (
    <div className="mt-4" data-testid="tds-return-profile-panel">
      {heading}

      {loadError !== "" ? (
        <p className="mt-2 max-w-3xl text-xs text-tally-bar">
          Cannot read the deductor profile: {loadError}
        </p>
      ) : (
        <>
          {!recorded || profile === null ? (
            <p className="mt-2 max-w-3xl text-xs text-tally-bar">
              No deductor profile on record — NO TDS RETURN CAN BE BUILT for
              this company. Deductions and challans can still be recorded; the
              return is what is refused.
            </p>
          ) : (
            <table className="mt-2 w-full max-w-4xl text-sm">
              <tbody>
                <tr className="align-top">
                  <td className="w-28 whitespace-nowrap py-0.5 pr-3 text-tally-title">
                    {profile.tan}
                  </td>
                  <td className="w-64 py-0.5 pr-3">{profile.deductor_name}</td>
                  <td className="py-0.5 text-xs">
                    category {profile.deductor_category}
                    {recordedCategory !== undefined
                      ? ` (${recordedCategory.description})`
                      : ""}
                    {" · "}
                    responsible: {profile.responsible.name},{" "}
                    {profile.responsible.designation}
                    {profile.recorded_at !== undefined &&
                    profile.recorded_at !== ""
                      ? ` · recorded by ${profile.recorded_by} at ${profile.recorded_at}`
                      : ` · recorded by ${profile.recorded_by}`}
                  </td>
                </tr>
              </tbody>
            </table>
          )}

          {/* THE OVERWRITE WARNING, VISIBLE BEFORE THE SAVE AND NOT AFTER IT.
              The panel above appends a row per tax year; this one replaces the
              only row there is, and the two forms otherwise look alike. */}
          <p className="mt-2 max-w-3xl text-xs text-tally-bar">
            This is CURRENT STATE, not history: there is one profile per company
            and saving OVERWRITES it — it does not add a row per year the way the
            s.402(11) panel above does. Re-emitting a return for an OLD quarter
            after the responsible person changes produces a file naming the NEW
            person. That is right for a fresh filing and wrong as a reproduction
            of what you filed, so keep the file you filed.
          </p>

          <form onSubmit={save} className="mt-3 max-w-4xl text-sm">
            <h3 className="mt-2 text-xs font-bold text-tally-title">
              The deductor
            </h3>
            <div className="mt-1 flex flex-wrap items-start gap-3">
              <Field
                label="TAN"
                value={form.tan}
                onChange={set("tan")}
                width="w-32"
                placeholder="AAAA99999A"
                hint="four letters, five digits, a letter"
              />
              <Field
                label="TAN name (as the department holds it)"
                value={form.deductor_name}
                onChange={set("deductor_name")}
                width="w-64"
                hint="must match the TAN name in the .csi file from TIN — punctuation included. A different fact from this company's display name."
              />
              <label className="flex flex-col">
                <span className="text-xs">Annexure 4 deductor category</span>
                <select
                  aria-label="Annexure 4 deductor category"
                  className="border border-tally-fieldText bg-tally-field px-1"
                  value={form.deductor_category}
                  onChange={(ev) => set("deductor_category")(ev.target.value)}
                >
                  {/* NOT pre-selected. The letter goes on a statutory return;
                      defaulting it would file a claim nobody made. The options
                      are the ones the SERVER served — this file holds no copy
                      of the list, and its two conditional rules travel with
                      each entry. */}
                  <option value="">— select —</option>
                  {categories.map((c) => (
                    <option key={c.code} value={c.code}>
                      {c.code} — {c.description}
                    </option>
                  ))}
                </select>
                <span className="mt-0.5 max-w-xs text-[10px] text-tally-fieldText">
                  BH field 32. Not the s.402(11) deductor type above.
                </span>
              </label>
              <Field
                label="Deductor PAN"
                value={form.pan}
                onChange={set("pan")}
                width="w-32"
                placeholder="AAAAA9999A"
              />
              <Field
                label="GSTIN (optional)"
                value={form.gstin}
                onChange={set("gstin")}
                width="w-40"
              />
              <Field
                label="AIN (Account Office Identification Number)"
                value={form.ain}
                onChange={set("ain")}
                width="w-32"
                hint="government deductors paying by book entry only"
              />
            </div>

            {/* ⚠️ THE SINGLE MOST LIKELY OPERATOR ERROR ON THIS FORM, said
                where it is made. The server refuses it with this sentence, but
                by then the operator believes they have keyed their tax number. */}
            <p className="mt-2 max-w-3xl text-xs text-tally-bar">
              THE TAN IS NOT THE GSTIN and cannot be derived from it. A Tax
              Deduction and Collection Account Number is separately allotted and
              has its own shape (AAAA99999A, against a GSTIN&apos;s fifteen
              characters); a company can hold either without the other. Nothing
              this application already knows about your company can produce it,
              which is why it is asked for — and a wrong one is rejected against
              the .csi challan file after you believe you have filed.
            </p>
            {panNotRequired !== "" && (
              <p className="mt-1 max-w-3xl text-xs">
                Where the deductor is not required to hold a PAN, the format
                prescribes the literal{" "}
                <span className="text-tally-title">{panNotRequired}</span> — a
                real value with a meaning, never a blank.{" "}
                <button
                  type="button"
                  className="border border-tally-title px-1 text-tally-title"
                  onClick={() => set("pan")(panNotRequired)}
                >
                  Use {panNotRequired}
                </button>{" "}
                (the responsible person below gets no such exemption: they are a
                named human and their PAN is their own.)
              </p>
            )}

            <AddressFields
              who="Deductor"
              value={form.address}
              onChange={set("address")}
            />

            <div className="mt-1 flex flex-wrap items-start gap-3">
              <Field
                label="Deductor e-mail"
                value={form.email}
                onChange={set("email")}
                width="w-56"
                hint="the format wants the deductor's OR the responsible person's — each mandatory only if the other is absent, so one of the two is required"
              />
              <Field
                label="Deductor calling code"
                value={form.contact_country_code}
                onChange={set("contact_country_code")}
                width="w-16"
              />
              <Field
                label="Deductor telephone"
                value={form.contact_number}
                onChange={set("contact_number")}
                width="w-40"
                hint="this format version splits the calling code from the number, so both are required"
              />
            </div>

            {/* THE CATEGORY-CONDITIONAL FIELDS. Shown only when the category the
                server served says so — hiding a required one and showing a
                forbidden one are equally 400s. */}
            {(showStateName || showMinistry) && (
              <div className="mt-2 flex flex-wrap items-start gap-3">
                {showStateName && (
                  <Field
                    label="Annexure 5 state name code"
                    value={form.state_name_code}
                    onChange={set("state_name_code")}
                    width="w-16"
                    hint={`BH field 54, required for category ${selected?.code ?? ""}. Two digits; the shape is all that is checked here.`}
                  />
                )}
                {showMinistry && (
                  <Field
                    label="Annexure 3 ministry code"
                    value={form.ministry_code}
                    onChange={set("ministry_code")}
                    width="w-16"
                    hint={
                      selected?.ministry_required === true
                        ? `BH field 57, required for category ${selected.code}.`
                        : `BH field 57, permitted for category ${selected?.code ?? ""} and optional.`
                    }
                  />
                )}
                {showMinistryOther && (
                  <Field
                    label="Ministry name (code 99, Other)"
                    value={form.ministry_other}
                    onChange={set("ministry_other")}
                    width="w-56"
                    hint={`BH field 58: code ${MINISTRY_OTHER_CODE} is "Other", so the ministry must be named here.`}
                  />
                )}
              </div>
            )}

            <h3 className="mt-3 text-xs font-bold text-tally-title">
              The person responsible for deduction
            </h3>
            <p className="mt-1 max-w-3xl text-[10px]">
              A named human, printed on the return and personally answerable for
              it. Their PAN is their OWN — BH field 59, a different field from
              the deductor&apos;s field 15 — and the literal above is not
              accepted for it.
            </p>
            <div className="mt-1 flex flex-wrap items-start gap-3">
              <Field
                label="Responsible person name"
                value={form.responsible.name}
                onChange={setResponsible("name")}
                width="w-56"
              />
              <Field
                label="Responsible person designation"
                value={form.responsible.designation}
                onChange={setResponsible("designation")}
                width="w-48"
              />
              <Field
                label="Responsible person PAN"
                value={form.responsible.pan}
                onChange={setResponsible("pan")}
                width="w-32"
                placeholder="AAAAA9999A"
              />
            </div>
            <AddressFields
              who="Responsible person"
              value={form.responsible.address}
              onChange={setResponsible("address")}
            />
            <div className="mt-1 flex flex-wrap items-start gap-3">
              <Field
                label="Responsible person e-mail"
                value={form.responsible.email}
                onChange={setResponsible("email")}
                width="w-56"
              />
              <Field
                label="Responsible person calling code"
                value={form.responsible.contact_country_code}
                onChange={setResponsible("contact_country_code")}
                width="w-16"
              />
              <Field
                label="Responsible person telephone"
                value={form.responsible.contact_number}
                onChange={setResponsible("contact_number")}
                width="w-40"
              />
            </div>

            <button
              type="submit"
              disabled={busy || form.deductor_category === ""}
              className="mt-3 border border-tally-title px-2 py-0.5 text-tally-title disabled:opacity-50"
            >
              {busy
                ? "Saving…"
                : recorded
                  ? "Overwrite the profile"
                  : "Record the profile"}
            </button>
          </form>

          {saveError !== "" && (
            // THE SERVER'S OWN SENTENCE, verbatim. It names the field, its BH
            // number and what to supply instead; a generic "could not save"
            // would throw away the only part that tells the operator what to do.
            <p className="mt-2 max-w-3xl text-xs text-tally-bar">{saveError}</p>
          )}
          {saveDenied && (
            <p className="mt-1 max-w-3xl text-xs">
              That refusal is the intended answer, not a fault: recording this
              row names an individual and their PAN, so it is refused to the
              Data Entry level by default. An owner can add{" "}
              <span className="text-tally-title">allow alter tds-profile</span>{" "}
              in Users &amp; Roles.
            </p>
          )}
          {saved !== "" && (
            <p className="mt-2 max-w-3xl text-xs text-tally-title">{saved}</p>
          )}
        </>
      )}
    </div>
  );
}

/**
 * s.394(2) MANUFACTURING DECLARATIONS — the THIRD real control on this screen.
 *
 * A resident buyer's written statement that goods are for manufacturing and NOT
 * for trading exempts collection at source on Table Sl. Nos. 1-5. Filing one
 * here is what makes the next scrap sale to that buyer collect nothing: the post
 * path reads this table, and there is deliberately no exemption flag on a
 * voucher for an operator to tick.
 *
 * ⚠️ THE PAN FIELD IS THE ONE THAT NEEDS A WARNING, AND IT GETS ONE ON SCREEN.
 * Leaving it empty does not merely weaken the declaration — s.397(2)(f)(i) makes
 * it INVALID and s.397(2)(g) then requires the PENAL rate, so the buyer is
 * charged 5% instead of 2%. A declaration without a PAN is WORSE for the buyer
 * than no declaration at all, and an operator transcribing a document has no way
 * to know that unless the form says so.
 */
function TCSDeclarationsPanel() {
  const client = useClient();
  const { company } = useCompanyContext();
  const companyID = company?.id ?? "";

  const [rows, setRows] = useState<TCSDeclaration[]>([]);
  const [sections, setSections] = useState<string[]>([]);
  const [buyers, setBuyers] = useState<{ id: string; name: string }[]>([]);
  const [loadError, setLoadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);

  const now = new Date();
  const currentTaxYear =
    now.getMonth() >= 3 ? now.getFullYear() : now.getFullYear() - 1;

  const [buyer, setBuyer] = useState("");
  const [section, setSection] = useState("");
  const [taxYear, setTaxYear] = useState(String(currentTaxYear));
  const [declared, setDeclared] = useState("");
  const [received, setReceived] = useState("");
  const [pan, setPan] = useState("");

  async function load() {
    if (!companyID) return;
    try {
      const res = await client.listTCSDeclarations(companyID);
      setRows(res.tcs_declarations);
      setSections(res.eligible_sections);
      // Pre-select when the statute leaves exactly one choice — today scrap is
      // the only section s.394(2) reaches that this build also collects on.
      const only = res.eligible_sections[0];
      if (res.eligible_sections.length === 1 && only !== undefined) {
        setSection((s) => (s === "" ? only : s));
      }
      setLoadError("");
      const led = await client.listLedgers(companyID);
      setBuyers(led.ledgers.map((l) => ({ id: l.id, name: l.name })));
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void load();
  }, [companyID]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setSaveError("");
    setSaved("");
    try {
      await client.createTCSDeclaration(companyID, {
        buyer_ledger_id: buyer,
        section,
        tax_year_start: Number(taxYear),
        declaration_date: declared,
        received_date: received,
        buyer_pan: pan,
        recorded_by: "owner",
      });
      setSaved(
        pan.trim() === ""
          ? "Filed WITHOUT a PAN — under s.397(2)(f)(i) this declaration is INVALID, and s.397(2)(g) requires the penal rate. This buyer will be charged 5%, not exempted."
          : "Filed. Sales to this buyer under this section are now exempt.",
      );
      await load();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-4" data-testid="tcs-declarations-panel">
      <h2 className="font-bold text-tally-title">
        s.394(2) manufacturing declarations
      </h2>
      <p className="mt-1 max-w-3xl text-xs">
        A resident buyer&apos;s written statement that goods are for
        manufacturing, processing, producing or generating power AND not for
        trading. Filing one here EXEMPTS that buyer&apos;s future sales under
        the section — there is no exemption box on the sales voucher, because
        the exemption is a document you hold, not a choice made at entry.{" "}
        <span className="text-tally-title">
          It reaches Table Sl. Nos. 1&ndash;5 only: scrap is Sl. No. 4 and
          qualifies, motor vehicles are Sl. No. 6 and do not.
        </span>
      </p>

      {loadError !== "" ? (
        <p className="mt-2 max-w-3xl text-xs text-tally-bar">
          Cannot read declarations: {loadError}
        </p>
      ) : (
        <>
          {rows.length === 0 ? (
            <p className="mt-2 text-xs text-tally-bar">
              None held — every sale collects at the normal rate.
            </p>
          ) : (
            <table className="mt-2 w-full max-w-4xl text-sm">
              <tbody>
                {rows.map((d) => (
                  <tr
                    key={`${d.buyer_ledger_id}:${d.section}:${d.tax_year_start}`}
                    className="align-top"
                  >
                    <td className="w-48 py-0.5 pr-3 text-tally-title">
                      {d.buyer_name}
                    </td>
                    <td className="w-32 py-0.5 pr-3">{d.tax_year_label}</td>
                    <td className="py-0.5 text-xs">
                      {d.pan_valid ? (
                        <>exempt · PAN {d.buyer_pan}</>
                      ) : (
                        <span className="text-tally-bar">
                          NO PAN — invalid under s.397(2)(f)(i), so the PENAL
                          rate applies, not the exemption
                        </span>
                      )}
                      {" · "}
                      forward a copy to the Commissioner by {
                        d.forward_due_date
                      }{" "}
                      (s.394(3))
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          <form onSubmit={save} className="mt-3 max-w-3xl text-sm">
            <div className="flex flex-wrap items-end gap-3">
              <label className="flex flex-col">
                <span className="text-xs">Buyer</span>
                <select
                  aria-label="Buyer"
                  className="border border-tally-fieldText bg-tally-field px-1"
                  value={buyer}
                  onChange={(ev) => setBuyer(ev.target.value)}
                >
                  <option value="">— select —</option>
                  {buyers.map((b) => (
                    <option key={b.id} value={b.id}>
                      {b.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col">
                <span className="text-xs">Section</span>
                <select
                  aria-label="Section"
                  className="border border-tally-fieldText bg-tally-field px-1"
                  value={section}
                  onChange={(ev) => setSection(ev.target.value)}
                >
                  {sections.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col">
                {/* NOT "Tax year beginning" — the deductor-status panel above
                    uses that, and two identically-labelled year fields on one
                    screen is exactly the ambiguity this feature cannot afford:
                    they follow OPPOSITE rules. That one is the year BEFORE the
                    payments it governs; this one is the year the declaration
                    covers. */}
                <span className="text-xs">Tax year covered</span>
                <input
                  aria-label="Declaration tax year"
                  className="w-24 border border-tally-fieldText bg-tally-field px-1"
                  value={taxYear}
                  onChange={(ev) => setTaxYear(ev.target.value)}
                />
              </label>
              <label className="flex flex-col">
                <span className="text-xs">Dated</span>
                <input
                  aria-label="Declaration date"
                  placeholder="yyyy-mm-dd"
                  className="w-32 border border-tally-fieldText bg-tally-field px-1"
                  value={declared}
                  onChange={(ev) => setDeclared(ev.target.value)}
                />
              </label>
              <label className="flex flex-col">
                <span className="text-xs">Received</span>
                <input
                  aria-label="Received date"
                  placeholder="yyyy-mm-dd"
                  className="w-32 border border-tally-fieldText bg-tally-field px-1"
                  value={received}
                  onChange={(ev) => setReceived(ev.target.value)}
                />
              </label>
              <label className="flex flex-col">
                <span className="text-xs">Buyer PAN</span>
                <input
                  aria-label="Buyer PAN"
                  className="w-32 border border-tally-fieldText bg-tally-field px-1"
                  value={pan}
                  onChange={(ev) => setPan(ev.target.value)}
                />
              </label>
              <button
                type="submit"
                disabled={busy || buyer === "" || section === ""}
                className="border border-tally-title px-2 py-0.5 text-tally-title disabled:opacity-50"
              >
                {busy ? "Filing…" : "File"}
              </button>
            </div>
            {/* The warning that has to be visible BEFORE the mistake, not after. */}
            {pan.trim() === "" && (
              <p className="mt-2 text-xs text-tally-bar">
                Leaving the PAN empty does not just weaken this declaration —
                s.397(2)(f)(i) makes it INVALID and s.397(2)(g) then requires
                the penal rate, so this buyer would be charged 5% instead of 2%.
                That is worse for them than filing nothing.
              </p>
            )}
          </form>
          {saveError !== "" && (
            <p className="mt-2 max-w-3xl text-xs text-tally-bar">{saveError}</p>
          )}
          {saved !== "" && (
            <p className="mt-2 max-w-3xl text-xs text-tally-title">{saved}</p>
          )}
        </>
      )}
    </div>
  );
}

export function FeaturesScreen() {
  const { company } = useCompanyContext();
  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        Company Features — {company?.name ?? "No Company"}
      </h1>
      <p className="mt-1 max-w-3xl text-sm">
        What this edition actually has. Nothing here is a decorative switch: a
        feature is either working from the keyboard, structurally always-on,
        SERVER ONLY (built and reachable over HTTP, with no screen or key here
        that reaches it), or openly missing. Esc goes back.
      </p>
      {/* The two DEDUCTOR panels sit next to each other on purpose and are
          headed so the difference is legible: the first answers "must this
          company deduct", the second "who is filing the return". They are
          different tables with different vocabularies — see each panel's own
          comment — and nothing derives one from the other. */}
      <DeductorStatusPanel />
      <TDSReturnProfilePanel />
      <TCSDeclarationsPanel />
      {FEATURES.map((g) => (
        <div key={g.group} className="mt-4">
          <h2 className="font-bold text-tally-title">{g.group}</h2>
          <table className="mt-1 w-full max-w-4xl text-sm">
            <tbody>
              {g.items.map((f) => (
                <tr key={f.name} className="align-top">
                  <td className="w-28 whitespace-nowrap py-0.5 pr-3">
                    <span className={BADGE[f.state].className}>
                      {BADGE[f.state].text}
                    </span>
                  </td>
                  <td className="w-96 py-0.5 pr-3">{f.name}</td>
                  <td className="py-0.5 text-xs">{f.note}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </section>
  );
}
