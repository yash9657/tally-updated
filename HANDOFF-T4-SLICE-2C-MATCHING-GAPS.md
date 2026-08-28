# HANDOFF — T4 SLICE 2c · CLOSING THE MATCHING GAPS AGAINST TALLY

**Written:** 2026-08-28, working tree CLEAN at `6e338fd` · **Branch:** `session/t32-and-close-7-3`
**Read time: 8 minutes.** ⭐ **Every claim below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU HAVE READ NOTHING.** Slice 2b (bank-statement matching) shipped on
2026-08-28 in `929df7f`. This is the list of what it does NOT do that TallyPrime does, why each was
left out, and what each would cost. `docs/design/t4-slice2-model.md` §9 is the as-built record;
read its §9.2–§9.4 before touching anything here, because three of the five items below are in
tension with decisions that section argues for at length.

---

## 0. ⭐ THE ONE RULE THAT GOVERNS EVERY ITEM BELOW

**A PROPOSED MATCH IS NOT A RECONCILIATION, AND THE TWO MUST NEVER RENDER THE SAME.**

The value of a bank reconciliation is that a human asserted *"this line is that voucher"*. A matcher
that ticks its own guesses produces a reconciliation that attests to nothing, and it fails in the
direction that **foots perfectly**: every figure is real, the difference is zero, and the one wrong
link is invisible until somebody chases a supplier for a payment the books say cleared.

⛔ **§5 BELOW PROPOSES SOFTENING EXACTLY THAT RULE.** It is the most dangerous item in this file and
it is deliberately last. Do the cheap, safe ones first.

---

## 1. WHERE THE PARITY CLAIMS COME FROM, AND WHAT THEY ARE WORTH

Every "Tally does X" below is from **TallyPrime's own help**, read 2026-08-28:

| Source | Settles |
|---|---|
| `help.tallysolutions.com/auto-bank-reconciliation/` | The matching rules, the potential-match config, the auto-reconcile setting |
| `help.tallysolutions.com/bank-statement/` | Statement import (slice 2a, already shipped) |
| `RESEARCH.md:858–916` | The banking module's shape, including cheque-book management |

⛔⛔ **A VENDOR HELP PAGE IS A PARITY CITATION AND NEVER A STATUTORY ONE.** It is good evidence about
what Tally does — which is what this product is measured against — and worth **nothing** as evidence
about what a law requires. `docs/statutory/README.md`'s rule ("the notified form, the gazette
text… **Not** blog articles, and not summaries") governs rates and is untouched by anything here.
⚠️ **Do not let a Tally default migrate into a statutory table.** The two kinds of source must stay
in separate drawers, and this file is the parity drawer.

⭐ **ONE PARAMETER HAS ALREADY BEEN CORRECTED THIS WAY** and it is the precedent for the rest:
`DefaultDateWindowDays` was **7, invented**, with a comment that admitted the invention and chose a
number anyway. Tally documents *"Find Potential Matches within"*, default **30 days**, 0 meaning
identical dates. Fixed in `6e338fd`. See `match.go`'s constant for the full write-up.

---

## 2. THE GAPS, MEASURED — AND THEY ARE NOT THE SAME SIZE

```bash
git log --oneline -2                       # -> 6e338fd, 929df7f
/usr/bin/grep -n "Party" backend/internal/bankstatement/match.go        # -> loaded, used only in strongKey's haystack
/usr/bin/grep -rn "instrument\|cheque" backend/internal/ledger/*.go     # -> NOTHING. See §4
```

| # | Gap | Size | Risk |
|---|---|---|---|
| §3.1 | **Reference normalisation** — leading zeroes, special characters | **Small.** One pure function | Low |
| §3.2 | **Party as a matching signal** | **Small.** The data is already loaded | Low–medium |
| §3.3 | **Optional amount tolerance** | **Medium.** Needs a config surface | ⚠️ Medium — it weakens B-7 |
| §4 | **Instrument Date matching** | ⛔ **LARGE — BLOCKED on an unbuilt feature** | n/a |
| §5 | **Opt-in auto-reconcile** | **Medium.** Small code, large consequences | ⛔ **HIGH** |

⭐ **DO THEM IN THAT ORDER.** §3.1 and §3.2 are strictly better matching with no new authority and no
new failure mode. §3.3 onwards each trade something away.

---

## 3. THE THREE THAT ARE ORDINARY WORK

### 3.1 Reference normalisation — the cheapest real win

**Tally:** offers *"Ignore zeroes prefixed in References"* and *"Ignore special characters in
References"*, both applying to the Instrument Number.

**We:** `strongKey()` in `match.go` upper-cases and splits on non-alphanumerics, then requires a
**whole-token** match. So cheque `000123` on the statement does not match `123` in the narration,
and `CHQ-123` does not match `CHQ 123`.

⛔ **KEEP THE WHOLE-TOKEN RULE.** Its comment explains why and the reason has not changed: `123` is a
substring of `1234` and of "invoice 51236", and a substring match promotes a coincidence to the tier
that means *certain*. Normalisation is about **canonicalising both sides before comparing**, not
about relaxing the comparison. Strip leading zeroes and punctuation on BOTH the statement reference
and the voucher tokens, then still require whole-token equality.

⚠️ **`TestAChequeNumberMatchesAWholeTokenAndNeverASubstring` MUST STILL PASS.** If normalisation
makes it fail, the change has quietly become a substring match.

### 3.2 Party as a matching signal

**Tally:** potential matches are found on *"various aspects of transactions such as Instrument Date,
Amount, and Party"*.

**We:** `BookLeg.Party` is loaded and reaches the matcher, but is used **only** as somewhere to look
for a cheque number (`match.go`'s `strongKey` haystack). It is never a criterion of its own.

**What to build:** when amount and direction agree and the party's name appears in the bank row's
narration, that is evidence — a bank narration usually carries the counterparty. It should promote
**potential → exact**, exactly as a cheque number does today.

⛔ **TWO TRAPS.**
1. **It must not create matches, only strengthen them.** Party is never sufficient on its own:
   amount and direction still have to agree to the paisa first. A party-only match would pair the
   two ₹25,000 payments to Acme in the same month.
2. ⚠️ **A PARTY MATCH MAKES THE MIRROR AMBIGUITY MORE LIKELY, NOT LESS.** Paying one supplier twice
   is the ordinary case. Promoting both rows to `exact` on the party name makes **both**
   bulk-acceptable unless `markContestedLegs` catches them — which it does, and which
   `TestTheMirrorAmbiguityIsCaughtTooWhenTwoBankRowsWantOneVoucher` pins. **Run that test first and
   watch it pass, then again after.**

### 3.3 An optional amount tolerance ⚠️ *the first one that trades something away*

**Tally:** *"Ignore difference in Amount for Potential Match"*, with greater/lesser-than options and
a user-set threshold.

**We:** zero tolerance, not configurable. `TestAPaisaOutIsNotAMatchAtAnyTier` pins it and a mutation
proof kills it.

⭐ **THIS IS A GAP AND NOT A CONTRADICTION.** The memo's invariant B-7 reads *"Match tolerance is
**zero unless explicitly configured**"* — the configuration half was simply never built.

⛔ **THREE THINGS THE BUILD MUST DO, AND THE THIRD IS THE ONE THAT WILL BE FORGOTTEN.**
1. **Default stays exactly zero.** A tolerance nobody set must never apply.
2. **A tolerated match can never be `exact`, and never `AcceptableInBulk`.** The amounts *disagree*;
   that is precisely the fact a human is being asked to weigh. `potential` at best.
3. ⛔ **THE DIFFERENCE MUST BE STATED ON THE ROW, IN RUPEES.** "These matched within tolerance" is
   not enough — the operator has to see *by how much*, because the residue is a real bank charge or
   a real short payment and it will otherwise be silently reconciled away. A difference absorbed
   without being named is how a genuine shortfall hides for a year.

---

## 4. ⛔ INSTRUMENT DATE — BLOCKED, AND NOT ON A DECISION

**Tally** matches on **Instrument Date** — the date written on the cheque, which is not the voucher
date and not the bank's value date.

**We cannot**, and this is a hard measurement rather than a preference:

```bash
/usr/bin/grep -rn "instrument\|cheque" backend/internal/ledger/*.go   # -> NOTHING
/usr/bin/grep -rn "instrument\|cheque" backend/migrations/*.sql       # -> nothing outside bank_statement*
```

**A VOUCHER IN THIS BUILD CARRIES NO CHEQUE NUMBER AND NO INSTRUMENT DATE.** There is nowhere to
read them from. The statement side has both (`bank_statement_line.cheque_number`); the books side
has neither.

⛔ **AND THE MISSING PIECE IS A WHOLE TALLY FEATURE, NOT A COLUMN.** `RESEARCH.md:883–886` documents
what supplies these in Tally: **Cheque Book Management** — per-bank-ledger cheque book ranges
(From/To), numbers auto-assigned during payment-voucher entry, a Cheque Register tracking
pending / printed / unreconciled / cancelled. `docs/design/t4-slice2-model.md` §7 item 4 declared
cheque printing and cheque-book management **out of scope for T4** and that is still the right call.

⭐ **SO THIS IS NOT SLICE 2c's WORK.** Adding a bare nullable `cheque_number` column to a voucher to
feed the matcher would be a column shipped ahead of its meaning — nothing would populate it, no
screen would collect it, and the matcher would gain a criterion that is always empty. **Build cheque
books, or do not build this.** Track it as a dependency, not as a gap.

---

## 5. ⛔⛔ OPT-IN AUTO-RECONCILE — THE DANGEROUS ONE

**Tally:** *"If you enable 'Automatically Reconcile Exact Matches found', then if there are any exact
matches found while importing statements and saving vouchers, they will get automatically reconciled
with the transactions available."*

**We:** nothing auto-clears, ever. `match.go`'s header argues for that at length and §9.4 of the memo
records it as a deliberate departure from the design memo's §0, which only forbade auto-clearing an
*ambiguous* match.

⚠️ **TALLY'S VERSION IS AN OPT-IN, AND THAT IS THE WHOLE DIFFERENCE.** Off by default, turned on by
somebody who accepted the consequence. That is a defensible feature. **An auto-reconcile that is on
by default is not**, and no amount of disclosure makes it so.

### 5.1 THE FIVE TRAPS, AND FOUR OF THEM ARE INVISIBLE ONCE SHIPPED

1. ⛔⛔ **`reconciled_by` BECOMES A LIE.** `bank_reconciliation.reconciled_by` is an audit column
   meaning *"this person asserted the bank cleared this"*. If a machine writes the row while nobody
   is looking and stamps a user's name on it, **the audit trail now records a human assertion that
   never happened** — and it is indistinguishable from a real one forever after. Write a value that
   says a machine did it, and make the reconciliation screen show it differently.
2. ⛔ **THE BACK-DATED CUTOFF.** Accepting a match writes a bank date, and that write is judged by
   `peekBankDate` + `storedBankDateForLeg` **because a human's request carries it**. An import that
   auto-reconciles is one request writing many clearances, some of which may land in a **filed,
   locked period**. Decide explicitly: does the cutoff refuse the whole import, skip those rows, or
   is auto-reconcile simply forbidden across a cutoff? ⚠️ **Saying nothing means the loosest of the
   three, silently.**
3. ⛔ **THE WebAuthn STEP-UP.** It gates on `Action.IsWrite()` and exists so a human is present at
   the keyboard for a sensitive write. A batch auto-reconcile satisfies it **once** and then writes
   an unbounded number of clearances. That may be acceptable — but it is a decision, and it must be
   written down where the next reader finds it.
4. ⚠️ **THE MIRROR AMBIGUITY IS THE FAILURE MODE.** Auto-reconcile must fire **only** on
   `AcceptableInBulk` — never on `BestTier == exact`, which is a different and weaker condition. Two
   bank rows contesting one voucher are each `exact` and each look unambiguous alone.
   ⛔ **`markContestedLegs` runs over the whole statement at once; a per-row auto-reconcile during
   import would bypass it entirely.** Compute the whole proposal set, then act.
5. ⚠️ **UNDO HAS TO BE CHEAP AND OBVIOUS.** A person who turns this on and dislikes the result needs
   to reverse a batch. Un-ticking each is `DELETE .../bank-reconciliation/...` and the match row
   cascades (0088), so the mechanism exists — but there is no batch undo and no way to ask *"which
   clearances did that import write?"* ⭐ **`bank_statement_match.matched_at` and the import's own
   timestamp make that answerable; nothing currently asks.**

### 5.2 WHAT WOULD MAKE IT SAFE

- **A per-company setting, default OFF**, whose label says what it does rather than what it saves.
- **Fires only on `AcceptableInBulk`.**
- **A distinct `reconciled_by` value** and a visible marker on the reconciliation screen, so a
  machine-asserted clearance never reads as a human one.
- **A report of what it did**, per import, reachable afterwards.
- **An explicit cutoff decision**, written in `auth_middleware.go` beside the existing bank-date
  comment — which already records that this exact control shipped ABSENT once, with a comment
  asserting it was present.

⭐ **AND THE HONEST ALTERNATIVE, WHICH ALREADY EXISTS.** The bulk-accept key on the matching panel
already collapses the typing to one keystroke while keeping the assertion human. **Before building
§5, be able to say what an operator gains over pressing that key** — if the answer is only "they do
not have to look", that is the thing the feature is for, and it is also the thing that makes it
dangerous.

---

## 6. DEFINITION OF DONE

- [ ] ⛔ **CHECK THE `FeaturesScreen.tsx` ROW BEFORE STARTING.** It is the closest thing to ground
      truth, it is SHARED with the T8 and T32 lanes, and it must be updated **in the same commit**.
      ⚠️ Announce before editing; cite rows by NAME, never by number.
- [ ] Every new parameter carries its Tally citation **and the words "parity, not statutory"**.
- [ ] `TestAChequeNumberMatchesAWholeTokenAndNeverASubstring` and
      `TestTheMirrorAmbiguityIsCaughtTooWhenTwoBankRowsWantOneVoucher` pass before AND after.
- [ ] A tolerated match states its difference **in rupees**, on the row.
- [ ] If §5 ships: default OFF, `AcceptableInBulk` only, machine-distinguishable `reconciled_by`,
      and a written cutoff decision.
- [ ] ⛔ **MUTATION-PROVE EVERY NEW GUARD**, and restore byte-identically (`shasum -a 256`). A
      migration mutation needs the `schema_migrations` un-stamp — see `HANDOFF-AFTER-T12.md` §5.
- [ ] Extend `frontend/scripts/drive-t4-slice2b-matching.mjs` rather than writing a second drive:
      it already builds the ambiguous fixture these changes are most likely to break.
- [ ] Gates: backend `go test ./... -timeout 60m` + `test_residue.sh` · `tsc --noEmit` ·
      `vitest run` (**baseline 1831 / 113 files**) · `pnpm verify:demo "$(cat frontend/.demo-company-id)"`
      (34/34) · the three drives (2b **35/35**, t12 33/33, t11 36/36).

---

## 7. ⚠️ WHAT IS *NOT* IN THIS FILE

| Item | Where it lives |
|---|---|
| Cheque book management / cheque printing | `RESEARCH.md:883–886`; §7 item 4 of the slice-2 memo. **§4 above depends on it** |
| Auto-creating a voucher from a bank row | Tally offers it; slice-2 memo §7 item 3 keeps it out — it is a write to the books from an outside file and needs its own memo |
| Three-or-more-way part payments | Deliberately bounded to PAIRS and **disclosed**. Widening it is a search-cost decision, not a parity one |
| A printed matching report | No layout specified. Ctrl+P is unbuilt here |
| F5 amount-view toggle | Still a refusal, deliberately — slice-2 memo §6: a display toggle, not matching |
