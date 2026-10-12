import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite build configuration for the keystroke/navigation engine UI.
//
// DEV PROXY: BrowserApp always gates the workspace with same-origin auth and
// talks to cored SAME-ORIGIN so there is no CORS to configure —
//   /v1, /auth        -> cored     (:8087)
// Override the target with CORED_PROXY_TARGET if needed.
//
// ⛔⛔ THE `/companies -> insightd` RULE IS GONE (register row D49). It routed
// EVERY same-origin `/companies/*` request to a service that authenticated
// nobody, which was how the browser reached the draft routes at all — and it was
// a NAMESPACE GRAB as well as an exposure: cored's own router is
// `/v1/companies/{companyID}`, so any cored route somebody added at `/companies`
// would have been delivered to insightd instead. A fix that appears to do nothing
// in the browser while every unit test passes.
//
// ⚠️ NOTHING IN THE BROWSER ADDRESSES insightd NOW. The drafts are cored routes
// (`/v1/companies/{id}/drafts...`) and cored forwards them onward itself, with a
// service credential and the company its PDP authorized. If a future rule needs
// to reach insightd from here, that is a design change and not a proxy entry:
// insightd will refuse it.
const CORED = process.env.CORED_PROXY_TARGET ?? "http://localhost:8087";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": { target: CORED, changeOrigin: true },
      "/auth": { target: CORED, changeOrigin: true },
    },
  },
});
