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
    saveAWSCreds: vi.fn(),
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
  routerDeployed: true,
  crdInstalled: true,
  awsConfigured: false,
  ociConfigured: false,
  gcpConfigured: false,
};

describe("RelaySetupWizard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows spinner while loading", () => {
    vi.mocked(relayApi.getSetup).mockReturnValue(new Promise(() => {}));
    renderWizard();
    expect(screen.getByLabelText("Loading")).toBeInTheDocument();
  });

  it("renders prerequisites with check marks", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => {
      expect(screen.getByText("Inference Relay")).toBeInTheDocument();
    });
    expect(screen.getByText("Relay router deployed")).toBeInTheDocument();
    expect(screen.getByText("InferenceRelay CRD installed")).toBeInTheDocument();
  });

  it("shows Add Relay Provider button when not adding", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => {
      expect(screen.getByText("Add Relay Provider")).toBeInTheDocument();
    });
  });

  it("shows provider selection cards when Add Relay Provider is clicked", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => {
      expect(screen.getByText("Add Relay Provider")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Add Relay Provider"));

    await waitFor(() => {
      expect(screen.getByText("Select Provider")).toBeInTheDocument();
    });
    expect(screen.getByText("AWS")).toBeInTheDocument();
    expect(screen.getByText("OCI")).toBeInTheDocument();
    expect(screen.getByText("GCP")).toBeInTheDocument();
  });

  it("shows provider credential form when a provider is selected", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());

    fireEvent.click(screen.getByText("AWS"));

    await waitFor(() => {
      expect(screen.getByPlaceholderText("Access Key ID (AKIA...)")).toBeInTheDocument();
    });
  });

  it("saves AWS credentials", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.saveAWSCreds).mockResolvedValue({ configured: true });
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());
    fireEvent.click(screen.getByText("AWS"));

    await waitFor(() => expect(screen.getByPlaceholderText("Access Key ID (AKIA...)")).toBeInTheDocument());

    await userEvent.type(screen.getByPlaceholderText("Access Key ID (AKIA...)"), "AKIATEST");
    await userEvent.type(screen.getByPlaceholderText("Secret Access Key"), "secret123");

    fireEvent.click(screen.getByText("Save AWS Credentials"));

    await waitFor(() => {
      expect(relayApi.saveAWSCreds).toHaveBeenCalledWith(
        expect.objectContaining({ accessKeyId: "AKIATEST", region: "us-east-1" }),
      );
    });
  });

  it("saves OCI credentials", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.saveOCICreds).mockResolvedValue({ configured: true });
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());
    fireEvent.click(screen.getByText("OCI"));

    await waitFor(() => expect(screen.getByPlaceholderText("Tenancy OCID")).toBeInTheDocument());

    await userEvent.type(screen.getByPlaceholderText("Tenancy OCID"), "ocid1.tenancy...");
    await userEvent.type(screen.getByPlaceholderText("User OCID"), "ocid1.user...");
    await userEvent.type(screen.getByPlaceholderText("API Key Fingerprint"), "aa:bb");
    await userEvent.type(
      screen.getByPlaceholderText("Private Key (paste full key including BEGIN/END lines)"),
      "-----BEGIN PRIVATE KEY-----",
    );

    fireEvent.click(screen.getByText("Save OCI Credentials"));

    await waitFor(() => {
      expect(relayApi.saveOCICreds).toHaveBeenCalledWith(
        expect.objectContaining({
          tenancy: "ocid1.tenancy...",
          user: "ocid1.user...",
          fingerprint: "aa:bb",
        }),
      );
    });
  });

  it("saves GCP credentials", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    vi.mocked(relayApi.saveGCPCreds).mockResolvedValue({ configured: true });
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());
    fireEvent.click(screen.getByText("GCP"));

    await waitFor(() =>
      expect(screen.getByPlaceholderText("Service Account JSON (paste entire file)")).toBeInTheDocument(),
    );

    await userEvent.click(screen.getByPlaceholderText("Service Account JSON (paste entire file)"));
    await userEvent.paste('{"type":"service_account"}');

    fireEvent.click(screen.getByText("Save GCP Credentials"));

    await waitFor(() => {
      expect(relayApi.saveGCPCreds).toHaveBeenCalledWith(
        expect.objectContaining({ serviceAccountJson: '{"type":"service_account"}' }),
      );
    });
  });

  it("shows configured providers after save and allows deploy", async () => {
    vi.mocked(relayApi.getSetup)
      .mockResolvedValueOnce(mockSetup)
      .mockResolvedValue({ ...mockSetup, awsConfigured: true });
    vi.mocked(relayApi.saveAWSCreds).mockResolvedValue({ configured: true });
    vi.mocked(relayApi.deploy).mockResolvedValue({ deployed: true });
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());
    fireEvent.click(screen.getByText("AWS"));

    await waitFor(() => expect(screen.getByPlaceholderText("Access Key ID (AKIA...)")).toBeInTheDocument());
    await userEvent.type(screen.getByPlaceholderText("Access Key ID (AKIA...)"), "AKIATEST");

    fireEvent.click(screen.getByText("Save AWS Credentials"));

    await waitFor(() => {
      expect(screen.getByText("AWS Relay")).toBeInTheDocument();
      expect(screen.getByText("Configured")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Deploy Relay Fleet"));

    await waitFor(() => {
      expect(relayApi.deploy).toHaveBeenCalledWith(
        expect.objectContaining({
          providers: ["aws"],
        }),
      );
    });
  });

  // Regression for issue #464: WireGuard was removed from the router↔relay path
  // (worklog 0447) and the backend deploy handler ignores routerEndpoint. The
  // wizard must not render a WireGuard endpoint field, and deploy must not be
  // gated on (or send) any endpoint value.
  it("does not render a WireGuard endpoint field and deploys without one", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue({ ...mockSetup, awsConfigured: true });
    vi.mocked(relayApi.deploy).mockResolvedValue({ deployed: true });
    renderWizard();

    await waitFor(() => expect(screen.getByText("AWS Relay")).toBeInTheDocument());

    expect(screen.queryByText("WireGuard Endpoint")).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("relay-gw.example.com")).not.toBeInTheDocument();

    const deployButton = screen.getByText("Deploy Relay Fleet");
    expect(deployButton).not.toBeDisabled();

    fireEvent.click(deployButton);

    await waitFor(() => {
      const call = vi.mocked(relayApi.deploy).mock.calls[0]?.[0];
      expect(call).toBeDefined();
      expect(call).not.toHaveProperty("routerEndpoint");
      expect(call).not.toHaveProperty("wireGuardPort");
    });
  });

  it("shows provider instructions when How to get… details is expanded", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());
    fireEvent.click(screen.getByText("AWS"));

    await waitFor(() => {
      expect(screen.getByText("How to get AWS credentials")).toBeInTheDocument();
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

  it("shows empty state when no providers configured", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue(mockSetup);
    renderWizard();

    await waitFor(() => {
      expect(screen.getByText("No relay providers configured yet. Add at least one to deploy.")).toBeInTheDocument();
    });
  });

  it("disables already-configured providers in selection", async () => {
    vi.mocked(relayApi.getSetup).mockResolvedValue({
      ...mockSetup,
      awsConfigured: true,
      ociConfigured: true,
    });
    renderWizard();

    await waitFor(() => fireEvent.click(screen.getByText("Add Relay Provider")));
    await waitFor(() => expect(screen.getByText("Select Provider")).toBeInTheDocument());

    const awsButton = screen.getByText("AWS").closest("button");
    const ociButton = screen.getByText("OCI").closest("button");
    const gcpButton = screen.getByText("GCP").closest("button");

    expect(awsButton).toBeDisabled();
    expect(ociButton).toBeDisabled();
    expect(gcpButton).not.toBeDisabled();
  });
});
