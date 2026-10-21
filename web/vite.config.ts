import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import { VitePWA } from "vite-plugin-pwa";

// tech-stack.md T15/T18: single bundle, shell-only Service Worker, no manifest.
export default defineConfig({
  plugins: [
    preact(),
    VitePWA({
      injectRegister: "script",
      manifest: false,
      workbox: {
        skipWaiting: true,
        clientsClaim: true,
        cleanupOutdatedCaches: true,
        navigateFallback: "/index.html",
        navigateFallbackAllowlist: [/^\/$/, /^\/print$/, /^\/recover$/],
        navigateFallbackDenylist: [/^\/api\//, /^\/healthz$/],
        runtimeCaching: [],
      },
    }),
  ],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: { output: { codeSplitting: false } },
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:8080", "/healthz": "http://127.0.0.1:8080" },
  },
});
