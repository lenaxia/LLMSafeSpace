import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { PermissionPrompt } from "./PermissionPrompt";
import type { PermissionRequest } from "../../api/types";

vi.mock("../../api/input", () => ({
  inputApi: {
    permissionReply: vi.fn().mockResolvedValue(true),
  },
}));

import { inputApi } from "../../api/input";
const mockReply = vi.mocked(inputApi.permissionReply);

const shellPermission: PermissionRequest = {
  id: "per_1",
  session_id: "ses_1",
  permission: "shell",
  patterns: ["rm -rf /workspace/node_modules"],
};

const writePermission: PermissionRequest = {
  id: "per_2",
  session_id: "ses_1",
  permission: "write",
  patterns: ["/workspace/src/main.go", "/workspace/go.mod"],
};

describe("PermissionPrompt", () => {
  const onResolved = vi.fn();

  beforeEach(() => { vi.clearAllMocks(); });

  it("displays permission type correctly", () => {
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    expect(screen.getByText("Run shell command")).toBeInTheDocument();
  });

  it("displays patterns in monospace", () => {
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    expect(screen.getByText("rm -rf /workspace/node_modules")).toBeInTheDocument();
  });

  it("Allow once calls API with reply:'once'", async () => {
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    fireEvent.click(screen.getByText("Allow once"));
    await waitFor(() => expect(mockReply).toHaveBeenCalledWith("ws-1", "per_1", "once", undefined));
    await waitFor(() => expect(onResolved).toHaveBeenCalled());
  });

  it("Allow always calls API with reply:'always'", async () => {
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    fireEvent.click(screen.getByText("Allow always"));
    await waitFor(() => expect(mockReply).toHaveBeenCalledWith("ws-1", "per_1", "always", undefined));
    await waitFor(() => expect(onResolved).toHaveBeenCalled());
  });

  it("Deny without message: first click shows feedback, second confirms", async () => {
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    fireEvent.click(screen.getByText("Deny"));
    // Feedback input appears
    expect(screen.getByLabelText("Feedback")).toBeInTheDocument();
    // Confirm deny
    fireEvent.click(screen.getByText("Confirm deny"));
    await waitFor(() => expect(mockReply).toHaveBeenCalledWith("ws-1", "per_1", "reject", undefined));
  });

  it("Deny with message includes message", async () => {
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    fireEvent.click(screen.getByText("Deny"));
    fireEvent.change(screen.getByLabelText("Feedback"), { target: { value: "too dangerous" } });
    fireEvent.click(screen.getByText("Confirm deny"));
    await waitFor(() => expect(mockReply).toHaveBeenCalledWith("ws-1", "per_1", "reject", "too dangerous"));
  });

  it("multiple patterns displayed", () => {
    render(<PermissionPrompt workspaceId="ws-1" request={writePermission} onResolved={onResolved} />);
    expect(screen.getByText("/workspace/src/main.go")).toBeInTheDocument();
    expect(screen.getByText("/workspace/go.mod")).toBeInTheDocument();
  });

  it("loading state disables buttons", async () => {
    mockReply.mockImplementation(() => new Promise(() => {})); // never resolves
    render(<PermissionPrompt workspaceId="ws-1" request={shellPermission} onResolved={onResolved} />);
    fireEvent.click(screen.getByText("Allow once"));
    await waitFor(() => {
      expect(screen.getByText("Allow once")).toBeDisabled();
      expect(screen.getByText("Allow always")).toBeDisabled();
    });
  });
});
