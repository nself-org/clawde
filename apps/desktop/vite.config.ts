/**
 * Purpose: Vite build config for ClawDE Tauri 2 desktop frontend.
 * Constraints: Tauri dev server on :5173; host must not use Node APIs directly.
 * SPORT: T-E1-07
 */
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  // Tauri: prevent Vite from obscuring Rust errors
  clearScreen: false,
  server: {
    port: 5173,
    strictPort: true,
    watch: {
      // Tell Vite to ignore watching `src-tauri`
      ignored: ["**/src-tauri/**"],
    },
  },
  build: {
    // Tauri uses Chromium on macOS — target modern baseline
    target: ["es2021", "chrome100", "safari15"],
    // Don't minify for easier debugging of Tauri apps
    minify: !process.env.TAURI_DEBUG ? ("esbuild" as const) : false,
    sourcemap: !!process.env.TAURI_DEBUG,
    outDir: "dist",
  },
});
