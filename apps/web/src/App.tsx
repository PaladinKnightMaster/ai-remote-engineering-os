import { type ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { CapabilityProvider, useCapability } from "@aireos/ui";
import { fetchCurrentCapability } from "./api";
import { Login } from "./routes/Login";
import { Dashboard } from "./routes/Dashboard";
import { Sessions } from "./routes/Sessions";
import { SessionView } from "./routes/SessionView";

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
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </CapabilityProvider>
  );
}
