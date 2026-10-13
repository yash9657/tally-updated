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
