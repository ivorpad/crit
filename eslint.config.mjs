export default [
  {
    files: ["web/app.js"],
    rules: {
      "no-var": "error",
      "prefer-const": "error",
      "no-unused-vars": ["error", { "args": "all", "argsIgnorePattern": "^_" }],
      "no-empty-function": "warn",
      "no-implicit-globals": "error",
      "no-useless-assignment": "error",
      "eqeqeq": "error",
      "no-shadow": "warn"
    }
  },
  {
    // The /herdr page's modules: app.js's rules except no-var/prefer-const
    // (crit's IIFE modules are var-based).
    files: ["web/herdr-transcript-*.js", "web/herdr-page*.js"],
    rules: {
      "no-unused-vars": ["error", { "args": "all", "argsIgnorePattern": "^_", "caughtErrors": "none" }],
      "no-empty-function": "warn",
      "no-implicit-globals": "error",
      "no-useless-assignment": "error",
      "eqeqeq": "error",
      "no-shadow": "warn"
    }
  }
];
