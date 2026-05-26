// Fixture: violates SEC-7 "no direct fetch()" — must be flagged by the
// shared ESLint config when linted from outside packages/shared-types/src/.
// Path: NOT under packages/shared-types/src/ → exception does not apply.

export async function badDirectFetch() {
  // This line should trigger no-restricted-syntax (CallExpression[callee.name='fetch']).
  const r = await fetch("/v1/auth/me");
  return r.json();
}

export async function badDirectRequest() {
  // This line should trigger no-restricted-syntax (NewExpression[callee.name='Request']).
  const req = new Request("/v1/auth/me");
  return req;
}
