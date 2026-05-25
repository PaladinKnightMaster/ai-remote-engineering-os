// Tests for <CapabilityProvider> + <CapabilityGate> + useCapability.
// Run under vitest with jsdom environment (see ../../vitest.config.ts).

import { afterEach, describe, it, expect, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import {
  CapabilityProvider,
  useCapability,
  type Capability,
} from "./CapabilityProvider";
import { CapabilityGate } from "./CapabilityGate";

// vitest is configured with globals: false (see ../../vitest.config.ts), so
// @testing-library/react's auto-cleanup hook does not register. Explicit
// cleanup prevents DOM leaks between tests (a prior render's nodes would
// otherwise survive into screen.queryByTestId in the next test).
afterEach(() => cleanup());

const ANY = (): Promise<Capability | null> => Promise.resolve(null);

describe("CapabilityGate — ADR-0047 capability-edge UI", () => {
  it("renders children when the edge matches the current capability", () => {
    render(
      <CapabilityProvider
        fetchCapability={ANY}
        initialValue={{ subject: "operator:admin", edge: "human-agent" }}
      >
        <CapabilityGate edge="human-agent">
          <span data-testid="protected">visible</span>
        </CapabilityGate>
      </CapabilityProvider>,
    );
    expect(screen.getByTestId("protected")).toBeTruthy();
  });

  it("hides children when there is no capability (unauthenticated)", () => {
    render(
      <CapabilityProvider fetchCapability={ANY} initialValue={null}>
        <CapabilityGate edge="human-agent">
          <span data-testid="protected">should not render</span>
        </CapabilityGate>
      </CapabilityProvider>,
    );
    expect(screen.queryByTestId("protected")).toBeNull();
  });

  it("hides children when the edge does not match", () => {
    render(
      <CapabilityProvider
        fetchCapability={ANY}
        initialValue={{ subject: "operator:admin", edge: "human-agent" }}
      >
        <CapabilityGate edge="agent-tool">
          <span data-testid="protected">should not render</span>
        </CapabilityGate>
      </CapabilityProvider>,
    );
    expect(screen.queryByTestId("protected")).toBeNull();
  });

  it("renders fallback when denied", () => {
    render(
      <CapabilityProvider fetchCapability={ANY} initialValue={null}>
        <CapabilityGate edge="human-agent" fallback={<span data-testid="fb">denied</span>}>
          <span>should not render</span>
        </CapabilityGate>
      </CapabilityProvider>,
    );
    expect(screen.getByTestId("fb").textContent).toBe("denied");
  });

  it("respects the optional subjects allow-list", () => {
    render(
      <CapabilityProvider
        fetchCapability={ANY}
        initialValue={{ subject: "operator:admin", edge: "human-agent" }}
      >
        <CapabilityGate edge="human-agent" subjects={["operator:root"]}>
          <span data-testid="protected">should not render</span>
        </CapabilityGate>
      </CapabilityProvider>,
    );
    expect(screen.queryByTestId("protected")).toBeNull();
  });
});

describe("CapabilityProvider — fetch lifecycle", () => {
  it("calls fetchCapability on mount when no initialValue is provided", async () => {
    const fetchCap = vi
      .fn<() => Promise<Capability | null>>()
      .mockResolvedValue({ subject: "operator:admin", edge: "human-agent" });

    function ShowSubject() {
      const { capability, loading } = useCapability();
      if (loading) return <span data-testid="loading">loading</span>;
      return <span data-testid="subject">{capability?.subject ?? "none"}</span>;
    }

    render(
      <CapabilityProvider fetchCapability={fetchCap}>
        <ShowSubject />
      </CapabilityProvider>,
    );

    expect(fetchCap).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(screen.getByTestId("subject").textContent).toBe("operator:admin");
    });
  });

  it("does NOT fetch when an initialValue is provided (test/SSR shortcut)", () => {
    const fetchCap = vi.fn<() => Promise<Capability | null>>().mockResolvedValue(null);
    render(
      <CapabilityProvider
        fetchCapability={fetchCap}
        initialValue={{ subject: "operator:admin", edge: "human-agent" }}
      >
        <div />
      </CapabilityProvider>,
    );
    expect(fetchCap).not.toHaveBeenCalled();
  });
});

describe("useCapability — usage guard", () => {
  it("throws when used outside <CapabilityProvider>", () => {
    function NoProvider() {
      useCapability();
      return null;
    }
    // React logs the boundary error to console.error; suppress for this test.
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      expect(() => render(<NoProvider />)).toThrow(
        /useCapability must be used inside <CapabilityProvider>/,
      );
    } finally {
      spy.mockRestore();
    }
  });
});
