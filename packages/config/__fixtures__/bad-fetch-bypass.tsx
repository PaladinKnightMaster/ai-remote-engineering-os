// Fixture: bypass attempts that COULD slip past a naive lint setup.
// The shared config's selectors must catch every form on this list.
// Each line below should produce a lint error.

export async function bypassA() {
  // globalThis.fetch should NOT bypass — covered by the member-expression selector.
  return globalThis.fetch("/v1/auth/me");
}

export async function bypassB() {
  // window.fetch — same.
  return window.fetch("/v1/auth/me");
}

export async function bypassC() {
  // self.fetch — same.
  return self.fetch("/v1/auth/me");
}

export function bypassD() {
  // window.Request — same shape as fetch, applied to Request constructor.
  return new window.Request("/v1/auth/me");
}

export function bypassE() {
  // window.localStorage — should be caught by no-restricted-properties.
  window.localStorage.setItem("aireos_token", "stolen");
}

export function bypassF() {
  // globalThis.sessionStorage — same.
  globalThis.sessionStorage.setItem("paseto", "stolen");
}
