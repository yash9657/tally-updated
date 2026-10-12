import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { LandingPage } from "./marketing/LandingPage";
import { Brand, ButtonLink } from "./design-system/components";
import type { AuthGateConfig } from "./App";
import "./auth/auth.css";

// Keep the accounting engine out of the public landing page's initial bundle.
const Workspace = lazy(() => import("./App").then(module => ({ default: module.App })));
const browserAuth: AuthGateConfig = { baseUrl: "" };

/** Public routes live outside the accounting navigation machine. */
export function BrowserApp({ auth = browserAuth }: { auth?: AuthGateConfig }) {
  const [path, setPath] = useState(() => window.location.pathname.replace(/\/+$/, "") || "/");
  const navigate = useCallback((next: string) => {
    window.history.replaceState(null, "", next);
    setPath(window.location.pathname.replace(/\/+$/, "") || "/");
  }, []);

  useEffect(() => {
    const onPopState = () => setPath(window.location.pathname.replace(/\/+$/, "") || "/");
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  // Old shared company URLs continue to open the requested company after login.
  const legacyCompanyLink = path === "/" && new URLSearchParams(window.location.search).has("company");
  useEffect(() => {
    if (legacyCompanyLink) navigate(`/app${window.location.search}`);
  }, [legacyCompanyLink, navigate]);

  const onSessionChange = useCallback((signedIn: boolean) => {
    const current = window.location.pathname.replace(/\/+$/, "");
    if (signedIn && current === "/signin") navigate(`/app${window.location.search}`);
    if (!signedIn && current === "/app") navigate(`/signin${window.location.search}`);
  }, [navigate, path]);

  useEffect(() => {
    document.title = path === "/" ? "Tallyweb — Familiar keys. A new chapter."
      : path === "/signin" ? "Sign in — Tallyweb"
      : path === "/app" ? "Workspace — Tallyweb" : "Page not found — Tallyweb";
  }, [path]);

  if (legacyCompanyLink) return <LoadingWorkspace />;
  if (path === "/") return <LandingPage />;
  if (path === "/signin" || path === "/app") {
    return <Suspense fallback={<LoadingWorkspace />}><Workspace auth={auth} onSessionChange={onSessionChange} /></Suspense>;
  }
  return <main className="route-message"><Brand /><h1>That page isn’t in the books.</h1><p>The link may have moved. Head back to Tallyweb to find your way.</p><ButtonLink href="/">Back to home</ButtonLink></main>;
}

function LoadingWorkspace() {
  return <main className="route-message" role="status"><Brand /><p>Opening your workspace…</p></main>;
}
