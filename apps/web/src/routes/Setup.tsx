import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { Button, useCapability } from "@aireos/ui";
import { apiClient, fetchSetupRequired } from "../api";

/**
 * First-run setup — ADR-0078. Until an operator exists the server is in setup mode: every API route but this
 * flow answers 503, so there is nothing else to show. The page asks for the one-time code `setup.sh` printed
 * on the server's terminal plus the operator's chosen username and password, and posts them in the BODY.
 * Layout and controls are Login's own (same card, same fields, same <Button>): no new primitive, no new token.
 * Provisioning stays anchored to root on the server; this is not a sign-up form (ADR-0046).
 */
const MIN_PASSWORD = 12;

export function Setup({ onComplete }: { onComplete: () => void }) {
  const [code, setCode] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const navigate = useNavigate();
  const { reload } = useCapability();

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    if (password.length < MIN_PASSWORD) {
      setError(`Use a password of at least ${MIN_PASSWORD} characters.`);
      return;
    }
    if (password !== confirm) {
      setError("The two passwords don't match.");
      return;
    }
    setSubmitting(true);
    try {
      // Case, spaces and dashes in the code are forgiven by the server; it is sent as typed.
      const { data, error: apiErr, response } = await apiClient.POST("/v1/setup/complete", {
        body: { code: code.trim(), username: username.trim(), password },
      });
      if (apiErr || !data) {
        if (response?.status === 404) {
          setError("Setup is already complete. Reload the page to sign in.");
        } else if (response?.status === 403 || response?.status === 400) {
          // The server's own words: an invalid or expired code names `setup.sh --new-setup-code`; a refused
          // username or password says why.
          setError(apiErr?.message ?? "Setup was refused. Check what you entered.");
        } else {
          setError("Setup failed. Try again.");
        }
        return;
      }
      // The session cookie came back with the 200: hydrate the capability, leave setup, land on the dashboard.
      await reload();
      onComplete();
      navigate("/", { replace: true });
    } finally {
      setSubmitting(false);
    }
  }

  const field = "w-full rounded-md border border-input bg-background px-3 py-2 text-sm";
  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <form
        onSubmit={onSubmit}
        aria-label="first-run setup"
        className="w-full max-w-sm space-y-4 rounded-md border border-border bg-background p-6 shadow-sm"
      >
        <h1 className="text-xl font-semibold">AI Remote Engineering OS</h1>
        <p className="text-sm text-muted-foreground">Create the operator account.</p>
        <p className="text-sm text-muted-foreground">
          Enter the setup code from the server's terminal. Before you type it, check that the certificate
          fingerprint your browser shows matches the one printed beside the code.
        </p>

        <label className="block space-y-1">
          <span className="text-sm font-medium">Setup code</span>
          <input type="text" name="code" autoComplete="off" autoCapitalize="characters" spellCheck={false}
            required value={code} onChange={(e) => setCode(e.target.value)} className={field} />
        </label>
        <label className="block space-y-1">
          <span className="text-sm font-medium">Username</span>
          <input type="text" name="username" autoComplete="username" required value={username}
            onChange={(e) => setUsername(e.target.value)} className={field} />
        </label>
        <div className="space-y-1">
          <label className="block space-y-1">
            <span className="text-sm font-medium">Password</span>
            <input type="password" name="password" autoComplete="new-password" required value={password}
              aria-describedby="password-hint" onChange={(e) => setPassword(e.target.value)} className={field} />
          </label>
          <p id="password-hint" className="text-xs text-muted-foreground">At least {MIN_PASSWORD} characters.</p>
        </div>
        <label className="block space-y-1">
          <span className="text-sm font-medium">Confirm password</span>
          <input type="password" name="confirm" autoComplete="new-password" required value={confirm}
            onChange={(e) => setConfirm(e.target.value)} className={field} />
        </label>

        {error && (
          <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {error}
          </div>
        )}

        <Button variant="primary" size="md" type="submit" disabled={submitting} className="w-full">
          {submitting ? "Creating account…" : "Create operator account"}
        </Button>
      </form>
    </div>
  );
}

/**
 * SetupGate — asks the server once whether setup is open. If it is, EVERY route renders <Setup> and /login
 * redirects to /setup; if not (404, or no answer), the normal router runs, where /setup is no route at all
 * and falls through to "/" (the dashboard, or login without a session).
 */
export function SetupGate({ children }: { children: ReactNode }) {
  const [required, setRequired] = useState<boolean | null>(null);
  const { pathname } = useLocation();
  useEffect(() => {
    let live = true;
    void fetchSetupRequired().then((r) => live && setRequired(r));
    return () => {
      live = false;
    };
  }, []);
  if (required === null) return null;
  if (!required) return <>{children}</>;
  if (pathname === "/login") return <Navigate to="/setup" replace />;
  return <Setup onComplete={() => setRequired(false)} />;
}
