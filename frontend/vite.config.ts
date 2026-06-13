import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite build configuration for the keystroke/navigation engine UI.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
  },
});
