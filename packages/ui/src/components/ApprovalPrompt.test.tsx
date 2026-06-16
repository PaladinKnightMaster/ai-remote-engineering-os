// <ApprovalPrompt> tests — presentational; the consumer wires the API, so these
// assert rendering, the approve/deny callbacks, the busy lock, and the a11y error.

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ApprovalPrompt } from "./ApprovalPrompt";

afterEach(cleanup);

describe("ApprovalPrompt", () => {
  it("renders the heading, resource context and approve/deny actions", () => {
    render(<ApprovalPrompt onApprove={() => {}} onDeny={() => {}} resourceId="res-1" />);
    expect(screen.getByRole("heading", { name: /approval required/i })).toBeTruthy();
    expect(screen.getByText(/res-1/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /approve/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /deny/i })).toBeTruthy();
  });

  it("calls onApprove / onDeny on click", () => {
    const onApprove = vi.fn();
    const onDeny = vi.fn();
    render(<ApprovalPrompt onApprove={onApprove} onDeny={onDeny} />);
    fireEvent.click(screen.getByRole("button", { name: /approve/i }));
    fireEvent.click(screen.getByRole("button", { name: /deny/i }));
    expect(onApprove).toHaveBeenCalledOnce();
    expect(onDeny).toHaveBeenCalledOnce();
  });

  it("disables the actions while busy", () => {
    render(<ApprovalPrompt onApprove={() => {}} onDeny={() => {}} busy />);
    expect((screen.getByRole("button", { name: /working/i }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: /deny/i }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("surfaces a resolve error via role=alert", () => {
    render(<ApprovalPrompt onApprove={() => {}} onDeny={() => {}} error="approval expired" />);
    expect(screen.getByRole("alert").textContent).toMatch(/approval expired/i);
  });

  // T-040c: the invocation consumer passes agent + command + overrides; the command
  // must render verbatim for informed consent (ADR-0059).
  it("renders the agent and the verbatim command for an invocation", () => {
    render(
      <ApprovalPrompt
        onApprove={() => {}}
        onDeny={() => {}}
        title="Tool invocation approval"
        description="An AI agent is requesting to run a command."
        agent="agent:smoke"
        resourceId="res-1"
        command="echo INVOKE-SMOKE-OK"
      />,
    );
    expect(screen.getByRole("heading", { name: /tool invocation approval/i })).toBeTruthy();
    expect(screen.getByText(/agent:smoke/)).toBeTruthy();
    expect(screen.getByLabelText("command").textContent).toBe("echo INVOKE-SMOKE-OK");
  });

  it("defaults to the session copy when no overrides are given (backward-compatible)", () => {
    render(<ApprovalPrompt onApprove={() => {}} onDeny={() => {}} resourceId="res-1" />);
    expect(screen.getByText(/requires approval before it can start/i)).toBeTruthy();
    expect(screen.queryByLabelText("command")).toBeNull();
  });
});
