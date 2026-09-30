import path from "node:path"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    // The whole build is embedded in the Go binary, so keeping it small keeps
    // the binary small. Source maps would roughly double it.
    sourcemap: false,
    // Panel assets are served from the same origin as the API and are
    // fingerprinted, so they can be cached for a year.
    assetsDir: "assets",
    // web/dist is tracked (empty) so that the Go embed compiles in a fresh
    // clone. Emptying it here would delete the .gitignore that keeps it, so
    // the build script clears the assets directory instead.
    emptyOutDir: false,
    // The panel's own code is the largest chunk and the one that changes on
    // every release; everything else is split out so an upgrade re-downloads
    // only that. Raising this number is not the fix if it starts failing.
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        manualChunks: {
          // Five languages are bundled up front so switching one is instant.
          // They are a fifth of the bundle, so they get their own chunk that
          // the browser can cache across panel upgrades.
          i18n: [
            "i18next",
            "react-i18next",
            "i18next-browser-languagedetector",
            "./src/locales/en.json",
            "./src/locales/id.json",
            "./src/locales/hi.json",
            "./src/locales/ru.json",
            "./src/locales/zh-CN.json",
          ],
          // Split by how often each part changes, not by what it does. React
          // and Radix move when a dependency is upgraded, which is rarely; the
          // panel's own code moves on every release. Keeping them apart means
          // an upgrade re-downloads the part that changed and nothing else,
          // which on a self-hosted panel behind a slow line is the difference
          // people actually notice.
          react: ["react", "react-dom", "react-router-dom"],
          // The umbrella package, which is what the components import. Naming
          // the individual @radix-ui/react-* packages instead catches only
          // whatever happens to be pulled in directly and leaves the rest in
          // the main chunk, which is what it did.
          radix: ["radix-ui"],
          query: ["@tanstack/react-query"],
          icons: ["lucide-react"],
        },
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // In development the Go binary serves the API and proxies everything
      // else here, but running Vite on its own must work too.
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: false,
      },
    },
  },
})
