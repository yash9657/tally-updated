import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite build configuration for the keystroke/navigation engine UI.
//
// DEV PROXY: when the UI runs in live mode (VITE_LIVE=1, see App.tsx) it talks to
// the backends SAME-ORIGIN so there is no CORS to configure on cored/insightd —
// the dev server forwards by path prefix:
//   /v1, /auth        -> cored     (:8087)
//   /companies        -> insightd  (:8088)  [draft + forecast routes; NOT /v1]
// Override the targets with CORED_PROXY_TARGET / INSIGHTD_PROXY_TARGET if needed.
const CORED = process.env.CORED_PROXY_TARGET ?? "http://localhost:8087";
const INSIGHTD = process.env.INSIGHTD_PROXY_TARGET ?? "http://localhost:8088";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": { target: CORED, changeOrigin: true },
      "/auth": { target: CORED, changeOrigin: true },
      "/companies": { target: INSIGHTD, changeOrigin: true },
    },
  },
});
