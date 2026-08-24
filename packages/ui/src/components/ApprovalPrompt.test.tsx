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

  // T-143 (FE-43, ADR-0071 §D3): description/payload-label are driven off `tool`,
  // not hardcoded by the caller — this is the fix for the live falsehood where every
  // invocation (file ops included) was told "run a command".
  describe("tool-derived copy (T-143)", () => {
    it("renders the exec sentence and 'command' label for remote.exec, unchanged", () => {
      render(
        <ApprovalPrompt onApprove={() => {}} onDeny={() => {}} tool="remote.exec" command="echo hi" />,
      );
      expect(screen.getByText(/requesting to run a command on a governed resource/i)).toBeTruthy();
      expect(screen.getByLabelText("command").textContent).toBe("echo hi");
      expect(screen.queryByRole("status")).toBeNull(); // no mutating badge for exec
    });

    it("renders the read sentence, the disclosure consequence, and a 'file descriptor' label for remote.file.read", () => {
      render(
        <ApprovalPrompt
          onApprove={() => {}}
          onDeny={() => {}}
          tool="remote.file.read"
          command="remote.file.read /etc/nginx/nginx.conf"
        />,
      );
      expect(screen.getByText(/requesting to read a file/i)).toBeTruthy();
      // T-143 review: read is a DISCLOSURE, and the prompt must say so — this is
      // the single most consequential fact about approving a read.
      expect(screen.getByText(/file's contents will be disclosed to the agent/i)).toBeTruthy();
      expect(screen.getByLabelText("file descriptor").textContent).toBe(
        "remote.file.read /etc/nginx/nginx.conf",
      );
      expect(screen.queryByLabelText("command")).toBeNull(); // wrong label must not linger
      expect(screen.queryByRole("status")).toBeNull(); // read does not change the host
    });

    it("renders the list sentence, the listing-disclosure consequence, and a 'directory descriptor' label for remote.file.list", () => {
      render(
        <ApprovalPrompt
          onApprove={() => {}}
          onDeny={() => {}}
          tool="remote.file.list"
          command="remote.file.list /etc/nginx"
        />,
      );
      expect(screen.getByText(/requesting to list a directory/i)).toBeTruthy();
      // T-143 review: a listing discloses filenames/sizes/timestamps — considered
      // deliberately, not overlooked, per the review's "your call" on list.
      expect(
        screen.getByText(/filenames, sizes and timestamps will be disclosed to the agent/i),
      ).toBeTruthy();
      expect(screen.getByLabelText("directory descriptor").textContent).toBe("remote.file.list /etc/nginx");
      expect(screen.queryByRole("status")).toBeNull(); // list does not change the host
    });

    it("marks remote.file.write as host-changing: badge (plain, not shouting), plain description, and the not-content consent line", () => {
      render(
        <ApprovalPrompt
          onApprove={() => {}}
          onDeny={() => {}}
          tool="remote.file.write"
          command="remote.file.write /etc/nginx/nginx.conf (4218 bytes, sha256:9f2c00000000000000000000000000000000000000000000000000000a41b, mode 0644)"
        />,
      );
      // T-143 review: plain language, no shouting caps — the badge carries the
      // emphasis, the sentence does not need to.
      expect(screen.getByRole("status").textContent).toBe("Changes the host");
      expect(screen.getByText(/this changes the target host/i)).toBeTruthy();
      expect(screen.queryByText(/MUTATES/)).toBeNull(); // the shouting copy must not linger
      expect(screen.getByText(/approving the path, size, digest and mode/i)).toBeTruthy();
      expect(screen.getByText(/not the file's contents/i)).toBeTruthy();
      expect(screen.getByLabelText("file descriptor").textContent).toMatch(/^remote\.file\.write /);
    });

    it("does not show the host-changing badge for a non-write tool", () => {
      render(
        <ApprovalPrompt
          onApprove={() => {}}
          onDeny={() => {}}
          tool="remote.file.read"
          command="remote.file.read /etc/nginx/nginx.conf"
        />,
      );
      expect(screen.queryByRole("status")).toBeNull();
      expect(screen.queryByText(/not the file's contents/i)).toBeNull(); // that's write's line, not read's
    });

    it("falls back to the caller's description override even when tool is set", () => {
      render(
        <ApprovalPrompt
          onApprove={() => {}}
          onDeny={() => {}}
          tool="remote.file.write"
          description="custom override text"
          command="remote.file.write /a (1 bytes, sha256:aa, mode 0644)"
        />,
      );
      expect(screen.getByText("custom override text")).toBeTruthy();
    });
  });
});
