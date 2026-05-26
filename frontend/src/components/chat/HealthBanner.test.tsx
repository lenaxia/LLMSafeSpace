import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { HealthBanner } from "./HealthBanner";

describe("HealthBanner", () => {
  it("renders nothing when healthy", () => {
    const { container } = render(
      <HealthBanner
        credentialState={{ available: true, reason: "CredentialsValid" }}
        agentHealth={{ status: "Healthy" }}
      />,
    );
    expect(container.innerHTML).toBe("");
  });

  it("renders nothing when props are undefined", () => {
    const { container } = render(<HealthBanner />);
    expect(container.innerHTML).toBe("");
  });

  it("renders credential warning when credentials missing", () => {
    render(
      <HealthBanner
        credentialState={{ available: false, reason: "CredentialSecretNotFound" }}
      />,
    );
    expect(screen.getByText("No credentials configured")).toBeInTheDocument();
  });

  it("renders credential warning when credentials empty", () => {
    render(
      <HealthBanner
        credentialState={{ available: false, reason: "CredentialEmpty" }}
      />,
    );
    expect(screen.getByText("Credentials are empty")).toBeInTheDocument();
  });

  it("renders credential warning when credentials invalid", () => {
    render(
      <HealthBanner
        credentialState={{ available: false, reason: "CredentialInvalid" }}
      />,
    );
    expect(screen.getByText("Credentials are invalid")).toBeInTheDocument();
  });

  it("renders agent degraded warning", () => {
    render(
      <HealthBanner
        agentHealth={{ status: "Degraded", message: "no providers connected" }}
      />,
    );
    expect(screen.getByText("no providers connected")).toBeInTheDocument();
  });

  it("renders agent unhealthy warning", () => {
    render(
      <HealthBanner
        agentHealth={{ status: "Unhealthy", message: "agent crashed" }}
      />,
    );
    expect(screen.getByText("agent crashed")).toBeInTheDocument();
  });

  it("renders agent unknown warning", () => {
    render(
      <HealthBanner agentHealth={{ status: "Unknown" }} />
    );
    expect(screen.getByText("Agent health unknown")).toBeInTheDocument();
  });

  it("renders both credential and agent issues", () => {
    render(
      <HealthBanner
        credentialState={{ available: false, reason: "CredentialSecretNotFound" }}
        agentHealth={{ status: "Unhealthy", message: "down" }}
      />,
    );
    expect(screen.getByText("No credentials configured")).toBeInTheDocument();
    expect(screen.getByText("down")).toBeInTheDocument();
  });

  it("renders fallback message for unknown reason", () => {
    render(
      <HealthBanner
        credentialState={{ available: false, reason: "SomeNewReason", message: "custom msg" }}
      />,
    );
    expect(screen.getByText("custom msg")).toBeInTheDocument();
  });
});
