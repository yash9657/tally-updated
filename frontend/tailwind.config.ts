import type { Config } from "tailwindcss";

/**
 * Tailwind theme. Tally's chrome is a monospace character grid in a blue theme
 * (TallyPrime) with a fixed right-side button bar. We encode those as design
 * tokens so the "Legacy-Faithful" (tallyBlue) skin and a future "Modern" skin
 * can be swapped without touching component logic (PLAN.md B.1).
 */
const config: Config = {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // TallyPrime "legacy-faithful" blue palette.
        tally: {
          bg: "#0a1e5e", // deep blue application background
          panel: "#13287a", // panel / menu background
          field: "#ffffff", // white data-entry field background
          fieldText: "#000080", // navy field text
          accent: "#ffd24d", // yellow highlight / active field
          active: "#1f9d55", // green active-field highlight (Tally's green cursor)
          bar: "#c9d4ff", // light button-bar background
          barText: "#0a1e5e",
          barKey: "#b30000", // red key letter on a button
          title: "#ffd24d",
        },
      },
      fontFamily: {
        mono: [
          "ui-monospace",
          "SFMono-Regular",
          "Menlo",
          "Consolas",
          "Liberation Mono",
          "monospace",
        ],
      },
    },
  },
  plugins: [],
};

export default config;
