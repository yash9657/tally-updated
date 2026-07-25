import type { ButtonBarItem } from "../keystroke/buttonBar";

/**
 * The fixed-position right-side button bar. Positions stay constant for muscle
 * memory; the visible set is supplied by the active screen. The key token is
 * rendered in Tally's red-on-light style.
 *
 * Clicking a button fires the SAME combo the keyboard sends (`onPress`), so the
 * mouse path and the keyboard path share one implementation and cannot drift.
 * `onMouseDown` preventDefault keeps focus where it was — a click must never
 * steal the caret out of the field the user is typing in.
 */
export function ButtonBar({
  items,
  onPress,
}: {
  items: readonly ButtonBarItem[];
  onPress?: (combo: string) => void;
}) {
  return (
    <nav
      aria-label="Button bar"
      className="flex w-40 shrink-0 flex-col gap-px bg-tally-bar font-mono text-xs"
    >
      {items.map((b) => (
        <button
          key={b.full}
          type="button"
          aria-label={b.full}
          className="flex flex-col items-start bg-tally-bar px-2 py-1 text-left text-tally-barText hover:bg-tally-accent"
          tabIndex={-1}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onPress?.(b.key)}
        >
          <span className="font-bold text-tally-barKey">{b.key}</span>
          <span>{b.label}</span>
        </button>
      ))}
    </nav>
  );
}
