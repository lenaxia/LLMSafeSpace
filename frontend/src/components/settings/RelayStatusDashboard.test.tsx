import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { relayApi } from "../../api/relay";
import { ToastProvider } from "../../providers/ToastProvider";
import { RelayStatusDashboard } from "./RelayStatusDashboard";

vi.mock("../../api/relay", () => ({
  relayApi: {
    getStatus: vi.fn(),
    rotate: vi.fn(),
    pause: vi.fn(),
    resume: vi.fn(),
  },
}));

const mockStatus = {
  deployed: true,
  overall: "healthy",
  healthyReplicas: 2,
  totalReplicas: 2,
  fallbackActive: false,
  activeStreams: 3,
  instances: [
    {
      id: "aws-1",
      provider: "aws",
      region: "us-east-1",
      shape: "t4g.micro",
      wgIP: "10.42.42.4",
      publicIP: "54.210.123.45",
      state: "healthy",
      healthy: true,
      metrics: {
        requestsToday: 12847,
        requests429Today: 3,
        totalRequests: 450000,
        egressBytes: 149546362,
        egressLimitBytes: 107374182400,
        activeStreams: 3,
      },
      cost: { monthlyEstimate: 700, spentThisMonth: 68 },
    },
    {
      id: "oci-1",
      provider: "oci",
      region: "us-ashburn-1",
      shape: "VM.Standard.A1.Flex",
      wgIP: "10.42.42.2",
      publicIP: "150.230.67.89",
      state: "healthy",
      healthy: true,
      metrics: {
        requestsToday: 0,
        requests429Today: 0,
        totalRequests: 0,
        egressBytes: 0,
        egressLimitBytes: 10995116277760,
        activeStreams: 0,
      },
      cost: { monthlyEstimate: 0, spentThisMonth: 0 },
    },
  ],
  conditions: [],
  recentEvents: [],
  alerts: [
    { name: "RelayFleetDegraded", expression: "healthy < 2", firing: false },
    { name: "RelayFleetCritical", expression: "healthy == 0", firing: false },
  ],
};

function renderDashboard() {
  return render(
    <ToastProvider>
      <RelayStatusDashboard />
    </ToastProvider>,
  );
}

describe("RelayStatusDashboard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows spinner while loading", () => {
    vi.mocked(relayApi.getStatus).mockReturnValue(new Promise(() => {}));
    renderDashboard();
    expect(screen.getByLabelText("Loading")).toBeInTheDocument();
  });

  it("shows 'not deployed' message when status is null", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({ ...mockStatus, deployed: false });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText(/No relay fleet deployed/i)).toBeInTheDocument();
    });
  });

  it("renders fleet overview with healthy status", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("2/2 relays active")).toBeInTheDocument();
    });
    expect(screen.getByText(/3 streams/)).toBeInTheDocument();
  });

  it("shows degraded status in yellow", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({
      ...mockStatus,
      overall: "degraded",
      healthyReplicas: 1,
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("1/2 relays active")).toBeInTheDocument();
    });
  });

  it("shows unhealthy status in red", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({
      ...mockStatus,
      overall: "unhealthy",
      healthyReplicas: 0,
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("0/2 relays active")).toBeInTheDocument();
    });
  });

  it("renders per-relay cards with provider info", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("54.210.123.45")).toBeInTheDocument();
    });
    expect(screen.getByText("150.230.67.89")).toBeInTheDocument();
  });

  it("renders cost information", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText(/\$0\.68/)).toBeInTheDocument();
    });
  });

  it("shows provisioning error in red banner (US-43.10)", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({
      ...mockStatus,
      instances: [
        {
          ...mockStatus.instances[0],
          state: "provisioning-failed",
          healthy: false,
          lastProvisionError: "InvalidParameterValue: Invalid AMI id",
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Provisioning failed")).toBeInTheDocument();
    });
    expect(screen.getByText(/Invalid AMI id/)).toBeInTheDocument();
  });

  it("shows 429 rate indicator", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText(/3 rate-limited requests today/)).toBeInTheDocument();
    });
  });

  it("renders alert rules section (US-43.11)", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Alerting Rules")).toBeInTheDocument();
    });
    expect(screen.getByText("RelayFleetDegraded")).toBeInTheDocument();
    expect(screen.getAllByText("OK")).toHaveLength(2);
  });

  it("shows firing alerts in red", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({
      ...mockStatus,
      alerts: [
        { name: "RelayFleetDegraded", expression: "x", firing: true },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("FIRING")).toBeInTheDocument();
    });
  });

  it("shows fallback indicator when active", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({
      ...mockStatus,
      fallbackActive: true,
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Fallback active")).toBeInTheDocument();
    });
  });

  it("triggers rotation on button click", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    vi.mocked(relayApi.rotate).mockResolvedValue({ rotating: "aws-1" });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getAllByText("Rotate")).toHaveLength(2);
    });

    fireEvent.click(screen.getAllByText("Rotate")[0]);

    await waitFor(() => {
      expect(relayApi.rotate).toHaveBeenCalledWith("aws-1");
    });
  });

  it("toggles pause/resume", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue(mockStatus);
    vi.mocked(relayApi.pause).mockResolvedValue({ paused: true });
    vi.mocked(relayApi.resume).mockResolvedValue({ paused: false });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Pause")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Pause"));

    await waitFor(() => {
      expect(relayApi.pause).toHaveBeenCalled();
    });

    await waitFor(() => {
      expect(screen.getByText("Resume")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Resume"));

    await waitFor(() => {
      expect(relayApi.resume).toHaveBeenCalled();
    });
  });

  it("renders recent events", async () => {
    vi.mocked(relayApi.getStatus).mockResolvedValue({
      ...mockStatus,
      recentEvents: [
        { timestamp: "2026-06-14T08:00:00Z", type: "Rotated", message: "AWS relay rotated", severity: "info" },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Recent Events")).toBeInTheDocument();
    });
    expect(screen.getByText("AWS relay rotated")).toBeInTheDocument();
  });
});
