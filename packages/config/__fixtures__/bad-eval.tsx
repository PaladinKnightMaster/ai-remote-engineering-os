// Fixture: eval / new Function() / setTimeout-with-string — orthogonal
// XSS escape hatches. The shared config bans all three.

export function badEval(code: string) {
  // no-eval should fire.
  return eval(code);
}

export function badFunction(code: string) {
  // no-new-func should fire.
  return new Function(code);
}

export function badImpliedEval() {
  // no-implied-eval should fire. ESLint's base rule only catches string
  // LITERALS passed to timers/setInterval — TS-typed string parameters
  // slip past because base ESLint doesn't read TS types. Use a literal
  // here so the rule reliably fires (documented limit of no-implied-eval).
  setTimeout("alert('xss')", 0);
}

export function badTargetBlank() {
  // react/jsx-no-target-blank should fire (missing rel="noopener noreferrer").
  return <a href="https://example.com" target="_blank">click</a>;
}
