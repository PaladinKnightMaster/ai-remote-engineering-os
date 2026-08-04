import { type ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { CapabilityProvider, useCapability } from "@aireos/ui";
import { fetchCurrentCapability } from "./api";
import { Login } from "./routes/Login";
import { Dashboard } from "./routes/Dashboard";
import { Sessions } from "./routes/Sessions";
import { SessionView } from "./routes/SessionView";
import { Invocations } from "./routes/Invocations";
import { Runs } from "./routes/Runs";
import { Grants } from "./routes/Grants";
import { Workstreams } from "./routes/Workstreams";
import { Memories } from "./routes/Memories";
import { Audit } from "./routes/Audit";
import { Pairing } from "./routes/Pairing";

/**
 * RequireAuth — route guard. The capability-edge security boundary is
 * services/api's RequireCapability middleware (ADR-0046); this guard is
 * pure UX so the SPA doesn't render protected routes for an
 * unauthenticated user (who would just see a flash of content followed
 * by a 401 redirect).
 */
function RequireAuth({ children }: { children: ReactNode }) {
  const { capability, loading } = useCapability();
  if (loading) return null;
  if (!capability) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

export function App() {
  return (
    <CapabilityProvider fetchCapability={fetchCurrentCapability}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route
            path="/"
            element={
              <RequireAuth>
                <Dashboard />
              </RequireAuth>
            }
          />
          <Route
            path="/sessions"
            element={
              <RequireAuth>
                <Sessions />
              </RequireAuth>
            }
          />
          <Route
            path="/sessions/:id"
            element={
              <RequireAuth>
                <SessionView />
              </RequireAuth>
            }
          />
          <Route
            path="/invocations"
            element={
              <RequireAuth>
                <Invocations />
              </RequireAuth>
            }
          />
          <Route
            path="/runs"
            element={
              <RequireAuth>
                <Runs />
              </RequireAuth>
            }
          />
          <Route
            path="/grants"
            element={
              <RequireAuth>
                <Grants />
              </RequireAuth>
            }
          />
          <Route
            path="/workstreams"
            element={
              <RequireAuth>
                <Workstreams />
              </RequireAuth>
            }
          />
          <Route
            path="/memories"
            element={
              <RequireAuth>
                <Memories />
              </RequireAuth>
            }
          />
          <Route
            path="/audit"
            element={
              <RequireAuth>
                <Audit />
              </RequireAuth>
            }
          />
          <Route
            path="/pairing"
            element={
              <RequireAuth>
                <Pairing />
              </RequireAuth>
            }
          />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </CapabilityProvider>
  );
}
