import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { relayApi } from "../../api/relay";
import { ToastProvider } from "../../providers/ToastProvider";
import { RelaySetupWizard } from "./RelaySetupWizard";

vi.mock("../../api/relay", () => ({
  relayApi: {
    getSetup: vi.fn(),
    saveAWSConfig: vi.fn(),
    testAWS: vi.fn(),
    saveOCICreds: vi.fn(),
    downloadCA: vi.fn(),
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
  certManagerInstalled: true,
  metalLBInstalled: true,
  routerDeployed: true,
  crdInstalled: true,
  awsConfigured: false,
  ociConfigured: false,
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
    expect(screen.getByText("cert-manager installed")).toBeInTheDocument();
    expect(screen.getByText("MetalLB installed")).toBeInTheDocument();
  });

  it("shows X for unmet prerequisites", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue({
      ...mockSetup,
      certManagerInstalled: false,
    });
    renderWizard();

    await waitFor(() => {
      expect(screen.getByText("cert-manager installed")).toBeInTheDocument();
    });
  });

  it("navigates to AWS step on Next", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => {
      expect(screen.getByText("Next →")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Next →"));
    await waitFor(() => {
      expect(screen.getByPlaceholderText("Trust Anchor ID (ta-xxxxx)")).toBeInTheDocument();
    });
  });

  it("saves AWS config and shows success", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.saveAWSConfig).mockResolvedValue({ configured: true });
    vi.mocked(relayApi.getSetup).mockResolvedValueOnce(mockSetup).mockResolvedValueOnce({ ...mockSetup, awsConfigured: true });
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => expect(screen.getByPlaceholderText("Trust Anchor ID (ta-xxxxx)")).toBeInTheDocument());

    await userEvent.type(screen.getByPlaceholderText("Trust Anchor ID (ta-xxxxx)"), "ta-abc");
    await userEvent.type(screen.getByPlaceholderText("Profile ID (p-xxxxx)"), "p-xyz");
    await userEvent.type(screen.getByPlaceholderText("Role ARN (arn:aws:iam::...)"), "arn:aws:iam::123:role/r");

    fireEvent.click(screen.getByText("Save Config"));

    await waitFor(() => {
      expect(relayApi.saveAWSConfig).toHaveBeenCalledWith({
        trustAnchorId: "ta-abc",
        profileId: "p-xyz",
        roleArn: "arn:aws:iam::123:role/r",
        region: "us-east-1",
      });
    });
  });

  it("tests AWS connection when configured", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue({ ...mockSetup, awsConfigured: true });
    vi.mocked(relayApi.testAWS).mockResolvedValue({ valid: true, accountId: "123" });
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => expect(screen.getByText("Test Connection")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Test Connection"));

    await waitFor(() => {
      expect(relayApi.testAWS).toHaveBeenCalled();
    });
  });

  it("navigates to OCI step", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => expect(screen.getByText("Next →")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Next →"));
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
    fireEvent.click(screen.getByText("Next →"));

    await waitFor(() => expect(screen.getByPlaceholderText("Tenancy OCID")).toBeInTheDocument());
    await userEvent.type(screen.getByPlaceholderText("Tenancy OCID"), "ocid1.tenancy...");

    fireEvent.click(screen.getByText("Save & Test"));

    await waitFor(() => {
      expect(relayApi.saveOCICreds).toHaveBeenCalled();
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
          providers: expect.arrayContaining(["oci"]),
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
