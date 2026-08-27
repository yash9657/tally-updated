/**
 * fyTag.ts — the financial-year label inside a PROVISIONAL number.
 *
 * ⚠️ THIS IS A DISPLAY LABEL AND NOTHING ELSE, WHICH IS WHY IT IS ALLOWED TO BE
 * DERIVED CLIENT-SIDE. `OfflineNode` forces `payload.voucher_no` empty
 * (`node.go:146`), so the tag never reaches the server and the real number is
 * allocated by the server's own per-(company, type, FY) allocator when the intent
 * commits. Nothing in the books depends on this string.
 *
 * ⚠️ AND THE ONE INACCURACY, STATED: it assumes the Indian default financial year
 * (1 April – 31 March). A company whose `fy_start_date` is some other month sees
 * a label that is off by a boundary — on a number that is visibly provisional, on
 * a screen that says so, and which the server replaces. Reading the company's
 * real `fy_start_date` would mean a GET inside a provider whose entire reason to
 * exist is that the network is down, and a fallback would be needed anyway.
 */

/** `"2025-26"` for any date in the FY beginning 1 April 2025. */
export function fyTagFor(d: Date): string {
  const y = d.getFullYear();
  // Months are 0-based: 0..2 (Jan–Mar) still belong to the year that began last
  // April, so the FY start year is the previous calendar year.
  const startYear = d.getMonth() >= 3 ? y : y - 1;
  const endShort = String((startYear + 1) % 100).padStart(2, "0");
  return `${startYear}-${endShort}`;
}
