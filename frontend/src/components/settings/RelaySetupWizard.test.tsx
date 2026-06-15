import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { relayApi } from "../../api/relay";
import { ToastProvider } from "../../providers/ToastProvider";
import { RelaySetupWizard } from "./RelaySetupWizard";

vi.mock("../../api/relay", () => ({
  relayApi: {
    getSetup: vi.fn(),
    saveOCICreds: vi.fn(),
    saveGCPCreds: vi.fn(),
    deploy: vi.fn(),
  },
}));

function renderWizard() {
  return render(
    <ToastProvider>
      <RelaySetupWizard onComplete={vi.fn()} />
    </ToastProvider>,
  );
}

const mockSetup = {
  deployed: false,
  metalLBInstalled: true,
  routerDeployed: true,
  crdInstalled: true,
  ociConfigured: false,
  gcpConfigured: false,
  wireGuardEndpoint: "",
};

describe("RelaySetupWizard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows spinner while loading", () => {
    vi.mocked(relayApi.getSetup).mockReturnValue(new Promise(() => {}));
    renderWizard();
    expect(screen.getByLabelText("Loading")).toBeInTheDocument();
  });

  it("renders prerequisites step with check marks", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => {
      expect(screen.getAllByText("Prerequisites").length).toBeGreaterThan(0);
    });
    expect(screen.getByText("MetalLB installed")).toBeInTheDocument();
    expect(screen.getByText("Relay router deployed")).toBeInTheDocument();
  });

  it("navigates to OCI step on Next", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => {
      expect(screen.getByPlaceholderText("Tenancy OCID")).toBeInTheDocument();
    });
  });

  it("saves OCI credentials", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.saveOCICreds).mockResolvedValue({ configured: true });
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => expect(screen.getByPlaceholderText("Tenancy OCID")).toBeInTheDocument());
    await userEvent.type(screen.getByPlaceholderText("Tenancy OCID"), "ocid1.tenancy...");

    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(relayApi.saveOCICreds).toHaveBeenCalled();
    });
  });

  it("navigates to GCP step", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => {
      expect(screen.getByPlaceholderText("Service Account JSON")).toBeInTheDocument();
    });
  });

  it("saves GCP credentials", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.saveGCPCreds).mockResolvedValue({ configured: true });
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => expect(screen.getByPlaceholderText("Service Account JSON")).toBeInTheDocument());
    await userEvent.click(screen.getByPlaceholderText("Service Account JSON"));
    await userEvent.paste('{"type":"service_account"}');

    const saveButtons = screen.getAllByText("Save");
    fireEvent.click(saveButtons[saveButtons.length - 1]!);

    await waitFor(() => {
      expect(relayApi.saveGCPCreds).toHaveBeenCalled();
    });
  });

  it("navigates to deploy step and deploys", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.deploy).mockResolvedValue({ deployed: true });
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));
    fireEvent.click(screen.getByText("Next →"));
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => {
      expect(screen.getByPlaceholderText("WireGuard endpoint (relay-gw.example.com:51820)")).toBeInTheDocument();
    });

    await userEvent.type(
      screen.getByPlaceholderText("WireGuard endpoint (relay-gw.example.com:51820)"),
      "gw.example.com:51820",
    );

    fireEvent.click(screen.getByText("Deploy Relay Fleet"));

    await waitFor(() => {
      expect(relayApi.deploy).toHaveBeenCalledWith(
        expect.objectContaining({
          routerEndpoint: "gw.example.com:51820",
          providers: expect.arrayContaining(["oci", "gcp"]),
        }),
      );
    });
  });

  it("calls onComplete when fleet is already deployed", async () => {
    const onComplete = vi.fn();
    vi.mocked(relayApi.getSetup).mockResolvedValue({ ...mockSetup, deployed: true });

    render(
      <ToastProvider>
        <RelaySetupWizard onComplete={onComplete} />
      </ToastProvider>,
    );

    await waitFor(() => {
      expect(onComplete).toHaveBeenCalled();
    });
  });
});
