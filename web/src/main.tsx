import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
// Fonts are vendored (not loaded from a CDN) so the desktop build stays
// fully offline. Fraunces carries the editorial display voice, Inter the
// interface text, and JetBrains Mono the tabular numerals in track lists.
import "@fontsource-variable/fraunces/full.css";
import "@fontsource-variable/inter/wght.css";
import "@fontsource-variable/jetbrains-mono/wght.css";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
