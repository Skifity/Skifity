import js from "@eslint/js"
import globals from "globals"
import reactHooks from "eslint-plugin-react-hooks"
import reactRefresh from "eslint-plugin-react-refresh"
import tseslint from "typescript-eslint"

/**
 * The panel's lint rules.
 *
 * Deliberately close to the defaults: the rules that are on are the ones that
 * catch real bugs (a missing hook dependency, an unhandled promise), not the
 * ones that argue about formatting. Prettier owns formatting.
 */
export default tseslint.config(
  { ignores: ["dist", "node_modules", "src/components/ui/**"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // A context provider and the hook that reads it belong in one file, and
      // so does a badge next to the function that decides its colour. The rule
      // only affects how much hot reload can preserve in development, which is
      // not worth splitting those pairs apart for.
      "react-refresh/only-export-components": "off",
      // An unused argument named with a leading underscore is a deliberate
      // signature match, not an oversight.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  {
    // Node scripts and config files are not browser code.
    files: ["*.config.{js,ts}", "scripts/**/*.mjs"],
    languageOptions: { globals: globals.node },
  },
)
