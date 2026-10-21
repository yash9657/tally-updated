/// <reference types="vite/client" />
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserApp } from "./BrowserApp";
import "./index.css";

const container = document.getElementById("root");
if (!container) {
  throw new Error("Root container #root not found");
}

createRoot(container).render(
  <StrictMode>
    <BrowserApp />
  </StrictMode>,
);

// Explicit local pilot mode. Production builds never load the observer.
if (import.meta.env.DEV && new URLSearchParams(location.search).has("workflow-benchmark")) {
  void import("./benchmarks/WorkflowObserver").then(({ WorkflowObserver }) => {
    const observer = document.createElement("div");
    document.body.appendChild(observer);
    createRoot(observer).render(<WorkflowObserver target={container} />);
  });
}
