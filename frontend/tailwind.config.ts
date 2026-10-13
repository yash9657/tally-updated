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
        // Compatibility aliases keep every existing workspace screen on the
        // shared CSS tokens. <alpha-value> preserves classes such as /50.
        tally: {
          bg: "rgb(var(--tw-color-canvas) / <alpha-value>)",
          panel: "rgb(var(--tw-color-panel) / <alpha-value>)",
          field: "rgb(var(--tw-color-text) / <alpha-value>)",
          fieldText: "rgb(var(--tw-color-ink) / <alpha-value>)",
          accent: "rgb(var(--tw-color-accent) / <alpha-value>)",
          active: "rgb(var(--tw-color-success) / <alpha-value>)",
          bar: "rgb(var(--tw-color-bar) / <alpha-value>)",
          barText: "rgb(var(--tw-color-canvas) / <alpha-value>)",
          barKey: "rgb(var(--tw-color-danger) / <alpha-value>)",
          title: "rgb(var(--tw-color-accent) / <alpha-value>)",
          muted: "rgb(var(--tw-color-muted) / <alpha-value>)",
          border: "rgb(var(--tw-color-border) / <alpha-value>)",
        },
      },
      fontFamily: {
        sans: ["var(--tw-font-sans)"],
        mono: ["var(--tw-font-mono)"],
      },
      spacing: Object.fromEntries([1, 2, 3, 4, 5, 6, 8, 10, 12, 16, 20, 24].map(n => [n, `var(--tw-space-${n})`])),
      borderRadius: { sm: "var(--tw-radius-sm)", md: "var(--tw-radius-md)", lg: "var(--tw-radius-lg)" },
    },
  },
  plugins: [],
};

export default config;
