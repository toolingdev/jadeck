import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import { App } from "./App";
import { LogProvider } from "./lib/log";
import { installErrorReporting } from "./lib/report";

installErrorReporting();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <LogProvider>
      <App />
    </LogProvider>
  </StrictMode>,
);

if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(() => {}));
}
