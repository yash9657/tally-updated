import type { PrintTarget } from "../screens/printTarget";

/**
 * ALT+M — E-MAIL A DOCUMENT (B2). The pure half: what the draft SAYS, and the URL.
 *
 * ✅ THE PRODUCT DECISION, RECORDED SO NOBODY REOPENS IT: **this product does not
 * send mail.** The PDF is saved to the operator's downloads and their own mail
 * client opens with a pre-subjected, pre-bodied draft. `internal/notify` — a real
 * durable transactional outbox — is NOT touched: what is missing there is a
 * transport, and we are deliberately not building one. No SMTP, no deliverability
 * story, no bounce handling, no migration.
 *
 * ⛔⛔ A WEB PAGE CANNOT ATTACH A FILE TO A `mailto:` DRAFT. There is no such
 * field in the `mailto:` URL scheme and no browser implements one. The flow is
 * therefore INHERENTLY TWO STEPS, and the operator must be told so BEFORE they
 * send — otherwise they send an invoice e-mail with no invoice on it and find out
 * from the customer. Hence:
 *
 *   1. the PDF is DOWNLOADED FIRST (through the one shared anchor);
 *   2. only then does the draft open — and if the download failed, it does NOT;
 *   3. the status line NAMES the saved file and says to attach it.
 *
 * ⛔ SO THE BODY MUST NOT SAY "the invoice is attached", because at that moment it
 * is not. It says nothing about an attachment at all: it is a short covering note
 * that is TRUE of a draft nobody has attached to yet, and it stays true after they
 * do. The INSTRUCTION lives in the UI, not in the message — the recipient must not
 * read "please attach the file", which is addressed to the sender.
 *
 * ⛔ AND THE DRAFT HAS NO RECIPIENT, WHICH CORRECTS THE PLAN THIS WAS BUILT FROM.
 * `HANDOFF-THE-LAST-TWO.md` §2 describes a "pre-addressed" draft. **There is no
 * party e-mail address anywhere in this product** — measured over the whole
 * schema, the only `%email%` columns are `app_user.email` (the operator's own
 * login), `company_invite.email` (an invitation) and `company_tds_profile.email`
 * / `.responsible_email` (the company's own TDS contact). A ledger carries none.
 * So there is nothing to prefill, the draft opens with an empty To: field, and the
 * screen SAYS so rather than leaving the operator to wonder. Storing a party
 * e-mail is a real and separate piece of work (a column, a master-form field, and
 * a decision about whose address is authoritative when it differs from the one in
 * the operator's own address book).
 */

/**
 * The practical ceiling on a `mailto:` URL.
 *
 * ⚠️ IT IS A CLIENT LIMIT, NOT A SPEC LIMIT, AND THE FAILURE IS SILENT. `mailto:`
 * fixes no maximum; browsers and mail clients do, and a URL past theirs is
 * TRUNCATED rather than refused — so a long body loses its tail with nothing
 * reporting a fault. 1800 is comfortably inside the worst common ceiling (~2000).
 *
 * The bodies this module builds are a few hundred characters, so this guard should
 * never fire in practice. It exists because "should never fire" is how the last
 * silent truncation shipped, and because a company's own name goes into both the
 * subject and the body and nothing bounds it.
 */
export const MAILTO_MAX_LENGTH = 1800;

/** A draft: what the mail client should open with. */
export interface MailDraft {
  subject: string;
  /** Plain text. Newlines are CRLF — see {@link buildMailtoURL}. */
  body: string;
  /**
   * True when the body was shortened to keep the URL under the cap, so the caller
   * can SAY so. A silently trimmed body is the failure this whole field exists to
   * make impossible.
   */
  trimmed?: boolean;
}

/**
 * The covering note for a resolved print target.
 *
 * ⛔ NOTHING HERE MENTIONS AN ATTACHMENT. Read the file header: at the moment this
 * draft opens there is no file on it, and there may never be one. Every sentence
 * below is true of an empty draft and stays true of a full one.
 *
 * ⚠️ AND NOTHING LOAD-BEARING GOES AT THE END. If a client truncates the body, it
 * truncates the tail — so the tail is a sign-off, not an instruction.
 */
export function draftFor(target: PrintTarget, companyName: string): MailDraft {
  // A refusal has no document and therefore no draft. Callers must never reach
  // here with one; this is belt-and-braces rather than a code path.
  const what = target.kind === "refusal" ? "document" : target.label;
  const from = companyName.trim() || "us";

  const subject = `${from} — ${what}`;
  const body = [
    "Dear Sir/Madam,",
    "",
    // ⛔ "regarding our" AND NOT "please find attached" / "enclosed" / "follows".
    // Every one of those asserts an attachment that is not there yet, and two of
    // them read as a lie if the operator forgets to attach the file.
    `This e-mail is regarding our ${what.toLowerCase()}.`,
    "",
    "Regards,",
    from,
  ].join("\r\n");

  return { subject, body };
}

/**
 * Build the `mailto:` URL for a draft.
 *
 * ⛔ THERE IS NO `to`, AND ITS ABSENCE IS DELIBERATE — see the file header. The
 * result is `mailto:?subject=…&body=…`, which is valid and opens a compose window
 * with an empty recipient. The address lives in the operator's own address book,
 * which is the only place this product has ever known it.
 *
 * ⚠️ `encodeURIComponent` IS THE RIGHT ENCODER AND `encodeURI` IS NOT. The latter
 * leaves `&`, `?` and `=` alone, so a company named "Smith & Sons" would end the
 * subject early and inject a parameter. It also turns the CRLF in the body into
 * `%0D%0A`, which is what a mail client expects a line break to be.
 *
 * If the URL would exceed {@link MAILTO_MAX_LENGTH} the BODY is shortened (never
 * the subject) and the returned draft is marked `trimmed`.
 */
export function buildMailtoURL(draft: MailDraft): {
  url: string;
  draft: MailDraft;
} {
  const build = (body: string) =>
    `mailto:?subject=${encodeURIComponent(draft.subject)}&body=${encodeURIComponent(body)}`;

  let url = build(draft.body);
  if (url.length <= MAILTO_MAX_LENGTH) return { url, draft };

  // Shorten by characters of BODY, re-encoding each time: the encoded length is
  // not a fixed multiple of the plain length (one "&" becomes three characters,
  // one "—" becomes nine), so trimming a computed number of bytes would either
  // overshoot or leave the URL still too long.
  let body = draft.body;
  while (body.length > 0 && build(body).length > MAILTO_MAX_LENGTH) {
    body = body.slice(0, Math.max(0, body.length - 32));
  }
  url = build(body);
  return { url, draft: { ...draft, body, trimmed: true } };
}

/**
 * The sentence the operator must read, naming the file and both limits.
 *
 * ⛔ THIS IS THE PRODUCT, NOT DECORATION. The whole flow's one failure mode is an
 * operator who presses Alt+M, sees a draft, types an address and sends — with no
 * invoice on the message. The two facts that prevent it (the file is on disk and
 * must be attached; there is no recipient because we do not store one) are stated
 * HERE, together, in the status line, rather than being split between a tooltip
 * and a help page.
 */
export function emailHandoffMessage(
  label: string,
  filename: string,
  trimmed = false,
): string {
  return (
    `Saved ${label} as ${filename} and opened an e-mail draft. ` +
    `⛔ ATTACH ${filename} BEFORE YOU SEND — a web page cannot put a file on a ` +
    `mail draft, so the message is empty until you attach it yourself. ` +
    `The draft has no recipient: this product does not store a party's e-mail address.` +
    (trimmed
      ? " ⚠️ The draft's body was shortened to fit what a mail client accepts in a link."
      : "")
  );
}
