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
});
