import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { AdminCredentialsTab } from "./AdminCredentialsTab";

const mockList = vi.fn();
const mockDelete = vi.fn();
const mockSetDefault = vi.fn();
const mockRotate = vi.fn();

vi.mock("../../api/credentials", () => ({
  credentialsApi: {
    list: () => mockList(),
    delete: (id: string) => mockDelete(id),
    setDefault: (id: string) => mockSetDefault(id),
    rotateKey: () => mockRotate(),
  },
}));

beforeEach(() => {
  vi.clearAllMocks();
  window.confirm = vi.fn(() => true);
  window.alert = vi.fn();
});

describe("AdminCredentialsTab", () => {
  it("shows spinner while loading", () => {
    mockList.mockReturnValue(new Promise(() => {}));
    render(<AdminCredentialsTab />);
    expect(document.querySelector(".animate-spin")).toBeInTheDocument();
  });

  it("shows empty state when no credential sets", async () => {
    mockList.mockResolvedValue([]);
    render(<AdminCredentialsTab />);
    await waitFor(() => {
      expect(screen.getByText("No credential sets configured.")).toBeInTheDocument();
    });
  });

  it("renders credential sets", async () => {
    mockList.mockResolvedValue([
      { id: "1", name: "Production", isDefault: true, providers: ["openai"], modelAllowlist: ["gpt-4"], assignedTo: "all", keyVersion: 1 },
      { id: "2", name: "Dev", isDefault: false, providers: ["anthropic"], modelAllowlist: [], assignedTo: "all", keyVersion: 1 },
    ]);
    render(<AdminCredentialsTab />);
    await waitFor(() => {
      expect(screen.getByText("Production")).toBeInTheDocument();
      expect(screen.getByText("Dev")).toBeInTheDocument();
      expect(screen.getByText("default")).toBeInTheDocument();
    });
  });

  it("deletes a credential set", async () => {
    mockList.mockResolvedValue([
      { id: "1", name: "ToDelete", isDefault: false, providers: [], modelAllowlist: [], assignedTo: "all", keyVersion: 1 },
    ]);
    mockDelete.mockResolvedValue(undefined);
    render(<AdminCredentialsTab />);

    await waitFor(() => screen.getByText("ToDelete"));
    fireEvent.click(screen.getByTitle("Delete"));

    await waitFor(() => {
      expect(mockDelete).toHaveBeenCalledWith("1");
    });
  });

  it("sets a credential set as default", async () => {
    mockList.mockResolvedValue([
      { id: "1", name: "NotDefault", isDefault: false, providers: ["x"], modelAllowlist: [], assignedTo: "all", keyVersion: 1 },
    ]);
    mockSetDefault.mockResolvedValue(undefined);
    render(<AdminCredentialsTab />);

    await waitFor(() => screen.getByText("NotDefault"));
    fireEvent.click(screen.getByTitle("Set as default"));

    await waitFor(() => {
      expect(mockSetDefault).toHaveBeenCalledWith("1");
    });
  });

  it("rotates encryption key", async () => {
    mockList.mockResolvedValue([]);
    mockRotate.mockResolvedValue({ rotated: 3, alreadyCurrent: 1, errors: 0 });
    render(<AdminCredentialsTab />);

    await waitFor(() => screen.getByText("Rotate Encryption Key"));
    fireEvent.click(screen.getByText("Rotate Encryption Key"));

    await waitFor(() => {
      expect(screen.getByText(/3 rotated/)).toBeInTheDocument();
    });
  });

  it("hides for non-admin (404)", async () => {
    mockList.mockRejectedValue(new Error("404 Not Found"));
    const { container } = render(<AdminCredentialsTab />);
    await waitFor(() => {
      expect(container.innerHTML).toBe("");
    });
  });
});
