import path from "node:path"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

// Matches a module inside one of the named packages in node_modules, with
// either path separator so the build is the same on Windows.
function packages(...names: string[]): RegExp {
  const alternatives = names.map((name) => name.replace("/", "[\\\\/]"))
  return new RegExp(`[\\\\/]node_modules[\\\\/](?:${alternatives.join("|")})[\\\\/]`)
}

const i18nPackages = packages("i18next", "react-i18next", "i18next-browser-languagedetector")
const locales = /[\\/]src[\\/]locales[\\/][^\\/]+\.json$/

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
    rolldownOptions: {
      output: {
        codeSplitting: {
          // Each group takes the packages it names and everything they import,
          // as the object form of Rollup's manualChunks did. Groups are claimed
          // in the order they are listed, so react comes first: every other
          // group imports it, and listed later it would be pulled into
          // whichever of them came first.
          groups: [
            // Split by how often each part changes, not by what it does. React
            // and Radix move when a dependency is upgraded, which is rarely; the
            // panel's own code moves on every release. Keeping them apart means
            // an upgrade re-downloads the part that changed and nothing else,
            // which on a self-hosted panel behind a slow line is the difference
            // people actually notice.
            { name: "react", test: packages("react", "react-dom", "react-router-dom") },
            // The umbrella package is what the components import, but the
            // bundler goes through its re-exports to the @radix-ui/react-*
            // packages behind it, so the scope has to be named too. With only
            // the umbrella, Radix ended up in a chunk named after whichever
            // component happened to use it first.
            { name: "radix", test: packages("radix-ui", "@radix-ui") },
            { name: "query", test: packages("@tanstack/react-query") },
            { name: "icons", test: packages("lucide-react") },
            // Five languages are bundled up front so switching one is instant.
            // They are a fifth of the bundle, so they get their own chunk that
            // the browser can cache across panel upgrades.
            { name: "i18n", test: (id) => i18nPackages.test(id) || locales.test(id) },
          ],
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
