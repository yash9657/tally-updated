/**
 * GST registration-type vocabulary for ledger masters (RESEARCH.md sec 5 & 7).
 *
 * Tally derives the CGST/SGST vs IGST split from the party's Registration Type
 * plus the company-state vs place-of-supply comparison. We capture the
 * registration type, GSTIN, and state code on the ledger master; the values map
 * onto CreateLedgerRequest's `registration_type` / `gstin` / `state_code`.
 */

/**
 * The party-level GST registration types, as the OPERATOR sees them.
 *
 * ⛔⛔ THIS LIST HAD FOUR ENTRIES AND THE SERVER'S ENUM HAS SIX. `masters.go`
 * defines UNREGISTERED, REGULAR, COMPOSITION, CONSUMER, **SEZ** and **UNKNOWN**;
 * this file omitted the last two, and this file is what a picker renders. So the
 * moment a picker was built from it, `SEZ` became unreachable from the keyboard
 * — silently, with no error anywhere, because an omission from a list cannot
 * fail. That mattered: a supply to an SEZ unit is ZERO-RATED, so a party that
 * should be SEZ and is keyed REGULAR is charged tax it does not owe.
 *
 * SEZ is now here. UNKNOWN deliberately is NOT, and the two absences are not the
 * same kind of thing:
 *
 *   SEZ     — a STATEMENT the operator can make. It belongs in the picker.
 *   UNKNOWN — the absence of a statement. It is the column DEFAULT, which is
 *             what a party gets when nobody said, and offering it as a CHOICE
 *             would let an operator deliberately record "I did not say", which
 *             is not a fact about the party. Register row D87 exists precisely
 *             because every keyed party landed on it by accident.
 *
 * ⚠️ THE ORDER IS THE PICKER'S ORDER, so the commonest answer is first.
 */
export const GST_REGISTRATION_TYPES = [
  "Unregistered",
  "Regular",
  "Composition",
  "Consumer",
  "SEZ",
] as const;
export type GstRegistrationType = (typeof GST_REGISTRATION_TYPES)[number];

/**
 * The wire value for a label. The server's enum is SCREAMING_SNAKE and the
 * labels are Title Case, and the mapping is total over the list above — there is
 * no fallback arm, on purpose: a label with no wire value must be a compile
 * error here rather than a silently-dropped field on the request.
 */
export const GST_REGISTRATION_WIRE: Readonly<
  Record<GstRegistrationType, string>
> = {
  Unregistered: "UNREGISTERED",
  Regular: "REGULAR",
  Composition: "COMPOSITION",
  Consumer: "CONSUMER",
  SEZ: "SEZ",
};

/** The two-digit GST state codes, with the state each one names. */
export const STATE_CODES: readonly { code: string; name: string }[] = [
  { code: "01", name: "Jammu & Kashmir" },
  { code: "02", name: "Himachal Pradesh" },
  { code: "03", name: "Punjab" },
  { code: "04", name: "Chandigarh" },
  { code: "05", name: "Uttarakhand" },
  { code: "06", name: "Haryana" },
  { code: "07", name: "Delhi" },
  { code: "08", name: "Rajasthan" },
  { code: "09", name: "Uttar Pradesh" },
  { code: "10", name: "Bihar" },
  { code: "11", name: "Sikkim" },
  { code: "12", name: "Arunachal Pradesh" },
  { code: "13", name: "Nagaland" },
  { code: "14", name: "Manipur" },
  { code: "15", name: "Mizoram" },
  { code: "16", name: "Tripura" },
  { code: "17", name: "Meghalaya" },
  { code: "18", name: "Assam" },
  { code: "19", name: "West Bengal" },
  { code: "20", name: "Jharkhand" },
  { code: "21", name: "Odisha" },
  { code: "22", name: "Chhattisgarh" },
  { code: "23", name: "Madhya Pradesh" },
  { code: "24", name: "Gujarat" },
  { code: "26", name: "Dadra & Nagar Haveli and Daman & Diu" },
  { code: "27", name: "Maharashtra" },
  { code: "29", name: "Karnataka" },
  { code: "30", name: "Goa" },
  { code: "31", name: "Lakshadweep" },
  { code: "32", name: "Kerala" },
  { code: "33", name: "Tamil Nadu" },
  { code: "34", name: "Puducherry" },
  { code: "35", name: "Andaman & Nicobar Islands" },
  { code: "36", name: "Telangana" },
  { code: "37", name: "Andhra Pradesh" },
  { code: "38", name: "Ladakh" },
  { code: "97", name: "Other Territory" },
];

/** True when `code` is a state code the GST system recognises. */
export function isKnownStateCode(code: string): boolean {
  const c = code.trim();
  return STATE_CODES.some((s) => s.code === c);
}

/** The state a code names, or "" when it names none. */
export function stateNameForCode(code: string): string {
  return STATE_CODES.find((s) => s.code === code.trim())?.name ?? "";
}

/** A PAN: five letters, four digits, one letter. */
const PAN_RE = /^[A-Z]{5}[0-9]{4}[A-Z]$/;
export function isValidPan(pan: string): boolean {
  return PAN_RE.test(pan.trim().toUpperCase());
}

/**
 * The PAN embedded in a GSTIN — characters 3..12.
 *
 * A GSTIN IS BUILT FROM THE PAN, so when both are stated they must agree. A
 * mismatch is a data-entry error the operator can still fix while the master is
 * on screen, and it is invisible everywhere afterwards.
 */
export function panFromGstin(gstin: string): string {
  return gstin.trim().toUpperCase().slice(2, 12);
}

/** Registration types that require a GSTIN and state code. */
export function requiresGstin(rt: string): boolean {
  return rt === "Regular" || rt === "Composition";
}

/**
 * Validate a GSTIN: 15 chars — 2-digit state code, 10-char PAN, 1 entity digit,
 * "Z", 1 checksum char. Loose but catches the common data-entry errors a Tally
 * GSTIN field rejects. Empty is treated as "not provided" (caller decides if
 * that is allowed for the chosen registration type).
 */
const GSTIN_RE = /^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$/;
export function isValidGstin(gstin: string): boolean {
  return GSTIN_RE.test(gstin.trim().toUpperCase());
}

/** The first two characters of a GSTIN are the state code. */
export function stateCodeFromGstin(gstin: string): string {
  return gstin.trim().slice(0, 2);
}
