import { useMemo, useState } from "react";

/**
 * Tally's as-you-type, substring-matching selection list (RESEARCH.md sec 4).
 * Renders an accessible combobox: typing filters the options by case-insensitive
 * substring; Enter selects the highlighted row; the first match is highlighted.
 */
export function SelectionList({
  label,
  options,
  onSelect,
  autoFocus = false,
  allowCreate = false,
}: {
  label: string;
  options: readonly string[];
  onSelect: (item: string) => void;
  autoFocus?: boolean;
  /**
   * Offer the typed text itself as a final row when it matches nothing —
   * Tally's "create it as you type" behaviour for master fields.
   *
   * WITHOUT THIS THE LIST IS A DEAD END on a company that has no such account
   * yet, which is EVERY company on its first voucher. The party picker used to
   * hide that behind a hardcoded sample list; once it read the real chart of
   * accounts, a brand-new company offered nothing and the field could not be
   * completed at all. (Found by the live browser e2e, not by a unit test.)
   */
  allowCreate?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (q.length === 0) return options.slice();
    const hits = options.filter((o) => o.toLowerCase().includes(q));
    // Only offer to create when nothing matches EXACTLY. A substring hit still
    // lists the real account first, so "Acme" never hides "Acme Traders".
    if (allowCreate && !options.some((o) => o.toLowerCase() === q)) {
      hits.push(query.trim());
    }
    return hits;
  }, [options, query, allowCreate]);

  /** True for the synthetic "create this" row (it is not one of the options). */
  const isNew = (opt: string) =>
    allowCreate && !options.some((o) => o.toLowerCase() === opt.toLowerCase());

  function handleKeyDown(ev: React.KeyboardEvent<HTMLInputElement>) {
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      setActive((a) => Math.min(a + 1, Math.max(matches.length - 1, 0)));
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (ev.key === "Enter") {
      const choice = matches[active];
      if (choice) {
        ev.preventDefault();
        // NOTE: deliberately NOT stopPropagation. onSelect highlights the choice
        // in the nav machine (a "select" event); the SAME Enter must then bubble
        // to the shell's key handler so its "key: Enter" drills into the
        // highlighted item in ONE keypress. Stopping propagation here made the
        // Gateway and the Go To overlay UN-OPENABLE for a real keyboard user (the
        // autofocused combobox swallowed the drilling Enter) — the shell's key
        // handler is on the app root, so a bubbling Enter is the only way it fires.
        onSelect(choice);
      }
    }
  }

  return (
    <div className="font-mono text-sm text-tally-fieldText">
      <label className="block text-tally-field">
        {label}
        <input
          // eslint-disable-next-line jsx-a11y/no-autofocus
          autoFocus={autoFocus}
          role="combobox"
          aria-expanded="true"
          aria-controls="selection-list"
          aria-label={label}
          className="ml-2 bg-tally-field px-1 text-tally-fieldText outline-none"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={handleKeyDown}
        />
      </label>
      <ul
        id="selection-list"
        role="listbox"
        className="mt-1 max-h-48 w-64 overflow-auto bg-tally-field"
      >
        {matches.map((opt, i) => (
          <li
            key={opt}
            role="option"
            aria-selected={i === active}
            className={
              i === active
                ? "bg-tally-active px-1 text-white"
                : "px-1 text-tally-fieldText hover:bg-tally-accent"
            }
            onMouseEnter={() => setActive(i)}
            onMouseDown={(e) => {
              e.preventDefault();
              onSelect(opt);
            }}
          >
            {isNew(opt) ? `Create “${opt}”` : opt}
          </li>
        ))}
      </ul>
    </div>
  );
}
