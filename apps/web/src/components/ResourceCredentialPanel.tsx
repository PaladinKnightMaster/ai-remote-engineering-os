// ResourceCredentialPanel — a target's SSH key as the cockpit may show it (T-176d; ADR-0079 D2/D5, FRX-4).
//
// PUBLIC material only: the public key, its fingerprint, and the exact restricted `authorized_keys` line the OPERATOR
// installs on the target themselves — nothing in the product writes to a target's trust (D3/D4). There is deliberately NO
// input anywhere in this file that accepts a private key (D5): the key is generated inside connector-service and the
// private half never reaches the browser. A test asserts the absence.
//
// States, from GET /v1/resources/{id}/credential:
//   200            a generated key: show it, offer Rotate.
//   404            nothing to show. The API does not say WHY (no credential yet, or one imported on the server with no public
//                  half recorded — both are connector 404s collapsed by the api client), so the panel offers "Generate a key";
//                  POST {rotate:false} answering 409 is how an imported credential is told apart, and then the panel says so.
//   anything else  the server's own message, plainly.
//
// Rotate is gated by a confirmation that names the consequence (ADR-0079 §Consequences): the old key stops working at once,
// so the cockpit cannot connect until the new line is installed. The confirmation is an inline `alertdialog` (no new
// primitive, none exists in packages/ui): focus moves to Cancel on open (the safe default), Escape cancels, and focus returns
// to the Rotate button afterwards.

import { useCallback, useEffect, useState } from "react";
import { Button } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type Credential = components["schemas"]["ResourceCredential"];
type View = { kind: "loading" } | { kind: "none" } | { kind: "imported" } | { kind: "key"; cred: Credential } | { kind: "error"; message: string };

const ROTATE_WARNING =
  "Rotating replaces this key. Until you install the new line on the target, the cockpit will not be able to connect to it.";

export function ResourceCredentialPanel({ resourceId, resourceName }: { resourceId: string; resourceName: string }) {
  const [view, setView] = useState<View>({ kind: "loading" });
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [copied, setCopied] = useState<"yes" | "failed" | null>(null);
  // Button is a plain function component (no ref forwarding), so focus is managed by id.
  const rotateId = `rotate-${resourceId}`;
  const cancelId = `rotate-cancel-${resourceId}`;
  const focus = (id: string) => document.getElementById(id)?.focus();

  const load = useCallback(async () => {
    const { data, error, response } = await apiClient.GET("/v1/resources/{id}/credential", { params: { path: { id: resourceId } } });
    if (data) setView({ kind: "key", cred: data });
    else if (response?.status === 404) setView({ kind: "none" });
    else setView({ kind: "error", message: error?.message ?? "Could not read this resource's key." });
  }, [resourceId]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (confirming) focus(cancelId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [confirming]);

  async function generate(rotate: boolean) {
    setBusy(true);
    setNotice(null);
    setCopied(null);
    try {
      const { data, error, response } = await apiClient.POST("/v1/resources/{id}/credential", {
        params: { path: { id: resourceId } },
        body: { rotate },
      });
      if (data) {
        setView({ kind: "key", cred: data });
        if (rotate) setNotice("Key rotated. The old key no longer works: add the new line below to the target account's ~/.ssh/authorized_keys.");
      } else if (response?.status === 409 && !rotate) {
        setView({ kind: "imported" });
      } else {
        setNotice(error?.message ?? "Could not generate a key.");
      }
    } finally {
      setBusy(false);
    }
  }

  function closeConfirm() {
    setConfirming(false);
    focus(rotateId);
  }

  async function confirmRotate() {
    setConfirming(false);
    await generate(true);
    setTimeout(() => focus(rotateId), 0); // after the busy state clears and the button is enabled again
  }

  async function copyLine(line: string) {
    try {
      await navigator.clipboard.writeText(line);
      setCopied("yes");
    } catch {
      setCopied("failed");
    }
  }

  return (
    <section aria-label={`credential for ${resourceName}`} className="space-y-2 border-t border-border p-3 text-sm">
      {view.kind === "loading" && <p className="text-muted-foreground">Loading…</p>}
      {view.kind === "error" && <p role="alert" className="text-destructive">{view.message}</p>}
      {view.kind === "imported" && (
        <p className="text-muted-foreground">
          This key was imported on the server, so the cockpit has no public half to show. It cannot be read or replaced from here.
        </p>
      )}
      {view.kind === "none" && (
        <div className="space-y-2">
          <p className="text-muted-foreground">No key is recorded for this resource.</p>
          <Button variant="primary" size="xs" type="button" disabled={busy} onClick={() => void generate(false)}>
            {busy ? "Generating…" : "Generate a key"}
          </Button>
        </div>
      )}
      {view.kind === "key" && (
        <div className="space-y-2">
          <dl className="space-y-1 text-xs">
            <div>
              <dt className="text-muted-foreground">Public key</dt>
              <dd className="break-all font-mono">{view.cred.publicKey}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Fingerprint</dt>
              <dd className="break-all font-mono">{view.cred.fingerprint}</dd>
            </div>
          </dl>
          <p className="text-xs text-muted-foreground">
            Add this line to the target account&apos;s <code className="font-mono text-foreground">~/.ssh/authorized_keys</code>.
          </p>
          <pre aria-label="authorized_keys line" className="select-all overflow-x-auto whitespace-pre-wrap break-all rounded-md bg-muted px-3 py-2 font-mono text-xs text-foreground">
            {view.cred.authorizedKeysLine}
          </pre>
          <div className="flex items-center gap-2">
            <Button variant="default" size="xs" type="button" onClick={() => void copyLine(view.cred.authorizedKeysLine)}>
              Copy line
            </Button>
            <Button id={rotateId} variant="destructive" size="xs" type="button" disabled={busy} onClick={() => setConfirming(true)}>
              {busy ? "Rotating…" : "Rotate key"}
            </Button>
            <span role="status" className="text-xs text-muted-foreground">
              {copied === "yes" ? "Copied." : copied === "failed" ? "Copy failed: select the line and copy it." : ""}
            </span>
          </div>
        </div>
      )}
      {confirming && (
        <div
          role="alertdialog"
          aria-modal="true"
          aria-labelledby={`rotate-title-${resourceId}`}
          aria-describedby={`rotate-body-${resourceId}`}
          onKeyDown={(e) => {
            if (e.key === "Escape") closeConfirm();
          }}
          className="space-y-2 rounded-md border border-destructive bg-background p-3"
        >
          <h3 id={`rotate-title-${resourceId}`} className="text-sm font-medium">
            Rotate the key for {resourceName}?
          </h3>
          <p id={`rotate-body-${resourceId}`} className="text-xs">
            {ROTATE_WARNING}
          </p>
          <div className="flex gap-2">
            <Button variant="destructive" size="xs" type="button" onClick={() => void confirmRotate()}>
              Rotate and replace the key
            </Button>
            <Button id={cancelId} variant="default" size="xs" type="button" onClick={closeConfirm}>
              Cancel
            </Button>
          </div>
        </div>
      )}
      {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}
    </section>
  );
}
