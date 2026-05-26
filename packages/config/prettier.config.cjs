// @aireos/config/prettier.config.cjs — shared Prettier config (T-012).
// CommonJS because some Prettier loaders still don't auto-detect ESM config
// files; .cjs keeps the supply chain mechanical and predictable.

/** @type {import("prettier").Config} */
module.exports = {
  semi: true,
  singleQuote: false,
  trailingComma: "all",
  printWidth: 100,
  arrowParens: "always",
  bracketSpacing: true,
  endOfLine: "lf",
};
