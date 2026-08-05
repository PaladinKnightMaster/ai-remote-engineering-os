import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { Button, useCapability } from "@aireos/ui";
import { apiClient } from "../api";

/**
 * Operator login route — ADR-0046 / ADR-0047.
 *
 * On submit:
 *  1. POST /v1/auth/login (services/api sets HttpOnly session + non-HttpOnly
 *     csrf cookies; body returns expiresAt only — token stays opaque to JS).
 *  2. Reload the CapabilityProvider so the new session cookie powers
 *     GET /v1/auth/me.
 *  3. Navigate to "/".
 *
 * No localStorage. No in-memory token. No client-side token parsing.
 * Bad credentials → 401 from services/api → inline error message.
 *
 * Known limit (T-007b ticket): this endpoint is public; "login CSRF"
 * remains an unmitigated attack class. Defense lands in a hardening
 * ticket or with Zitadel adoption (ADR-0046 reserve).
 */
export function Login() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const navigate = useNavigate();
  const { reload } = useCapability();

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const { data, error: apiErr, response } = await apiClient.POST("/v1/auth/login", {
        body: { username, password },
      });
      if (apiErr || !data) {
        if (response?.status === 401) {
          setError("Invalid username or password.");
        } else {
          setError("Login failed. Try again.");
        }
        return;
      }
      // Cookies are set by services/api in the response. Refetch /v1/auth/me
      // through the same cookie path to hydrate the capability state.
      await reload();
      navigate("/", { replace: true });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <form
        onSubmit={onSubmit}
        aria-label="operator login"
        className="w-full max-w-sm space-y-4 rounded-md border border-border bg-background p-6 shadow-sm"
      >
        <h1 className="text-xl font-semibold">AI Remote Engineering OS</h1>
        <p className="text-sm text-muted-foreground">Operator sign-in.</p>

        <label className="block space-y-1">
          <span className="text-sm font-medium">Username</span>
          <input
            type="text"
            name="username"
            autoComplete="username"
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </label>

        <label className="block space-y-1">
          <span className="text-sm font-medium">Password</span>
          <input
            type="password"
            name="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </label>

        {error && (
          <div
            role="alert"
            className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </div>
        )}

        {/* className: w-full — spans the login card's width (layout, not a
            visual restyle). */}
        <Button variant="primary" size="md" type="submit" disabled={submitting} className="w-full">
          {submitting ? "Signing in…" : "Sign in"}
        </Button>
      </form>
    </div>
  );
}
