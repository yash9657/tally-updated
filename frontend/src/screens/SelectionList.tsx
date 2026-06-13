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
}: {
  label: string;
  options: readonly string[];
  onSelect: (item: string) => void;
  autoFocus?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (q.length === 0) return options.slice();
    return options.filter((o) => o.toLowerCase().includes(q));
  }, [options, query]);

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
        ev.stopPropagation();
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
            {opt}
          </li>
        ))}
      </ul>
    </div>
  );
}
