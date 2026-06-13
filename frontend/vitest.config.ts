import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Vitest configuration. Uses jsdom so React screens and key-event wiring can be
// exercised, and globals so tests can use describe/it/expect without imports.
export default defineConfig({
  plugins: [react()],
  test: {
    globals: true,
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
