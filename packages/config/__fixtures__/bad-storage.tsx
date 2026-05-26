// Fixture: violates SEC-7 "no localStorage / sessionStorage in apps & ui".
// Must be flagged by no-restricted-globals.

export function badPersistToken(token: string) {
  // Should trigger no-restricted-globals (localStorage).
  localStorage.setItem("aireos_token", token);
  // Should trigger no-restricted-globals (sessionStorage).
  sessionStorage.setItem("paseto", token);
}
