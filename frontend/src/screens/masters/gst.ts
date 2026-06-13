/**
 * GST registration-type vocabulary for ledger masters (RESEARCH.md sec 5 & 7).
 *
 * Tally derives the CGST/SGST vs IGST split from the party's Registration Type
 * plus the company-state vs place-of-supply comparison. We capture the
 * registration type, GSTIN, and state code on the ledger master; the values map
 * onto CreateLedgerRequest's `registration_type` / `gstin` / `state_code`.
 */

/** Tally's GST registration types (party-level). */
export const GST_REGISTRATION_TYPES = [
  "Unregistered",
  "Regular",
  "Composition",
  "Consumer",
] as const;
export type GstRegistrationType = (typeof GST_REGISTRATION_TYPES)[number];

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
