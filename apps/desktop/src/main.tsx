/**
 * Purpose: React entry point — mounts App into #root with StrictMode.
 * Inputs:  None
 * Outputs: React tree rendered to DOM
 * Constraints: No router (AppShell manages nav via Zustand); StrictMode for dev
 * SPORT: T-E1-07
 */

import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";

const container = document.getElementById("root");
if (!container) throw new Error("Root element #root not found");

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
