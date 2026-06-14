import { test, expect } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const API_PREFIX = "**/api/v1";

async function mockAdminAuth(page: Page) {
  await page.route(`${API_PREFIX}/auth/me`, async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ id: "admin-1", email: "admin@test.com", role: "admin" }),
    });
  });
  await page.route(`${API_PREFIX}/auth/config`, async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ registrationEnabled: true, oidcEnabled: false, instanceName: "test" }),
    });
  });
}

const mockSetupNotDeployed = {
  deployed: false,
  certManagerInstalled: true,
  metalLBInstalled: true,
  routerDeployed: true,
  crdInstalled: true,
  awsConfigured: false,
  ociConfigured: false,
  wireGuardEndpoint: "",
};

const mockSetupDeployed = {
  ...mockSetupNotDeployed,
  deployed: true,
  awsConfigured: true,
  ociConfigured: true,
};

const mockStatusHealthy = {
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
      wgIP: "10.42.42.4",
      publicIP: "54.210.123.45",
      state: "healthy",
      healthy: true,
      metrics: { requestsToday: 12847, requests429Today: 0, totalRequests: 450000, egressBytes: 149546362, egressLimitBytes: 107374182400, activeStreams: 3 },
      cost: { monthlyEstimate: 700, spentThisMonth: 68 },
    },
    {
      id: "oci-1",
      provider: "oci",
      region: "us-ashburn-1",
      wgIP: "10.42.42.2",
      publicIP: "150.230.67.89",
      state: "healthy",
      healthy: true,
      metrics: { requestsToday: 0, requests429Today: 0, totalRequests: 0, egressBytes: 0, egressLimitBytes: 10995116277760, activeStreams: 0 },
      cost: { monthlyEstimate: 0, spentThisMonth: 0 },
    },
  ],
  conditions: [],
  recentEvents: [
    { timestamp: new Date().toISOString(), type: "Rotated", message: "AWS relay rotated", severity: "info" },
  ],
  alerts: [
    { name: "RelayFleetDegraded", expression: "healthy < 2", firing: false },
    { name: "RelayFleetCritical", expression: "healthy == 0", firing: false },
  ],
};

const mockStatusUnhealthy = {
  ...mockStatusHealthy,
  overall: "unhealthy",
  healthyReplicas: 0,
  instances: [
    {
      ...mockStatusHealthy.instances[0],
      state: "provisioning-failed",
      healthy: false,
      lastProvisionError: "InvalidParameterValue: Invalid AMI id",
    },
  ],
  alerts: [
    { name: "RelayFleetDegraded", expression: "healthy < 2", firing: true },
    { name: "RelayFleetCritical", expression: "healthy == 0", firing: true },
  ],
};

test.describe("Relay admin UI", () => {
  test.beforeEach(async ({ page }) => {
    await mockAdminAuth(page);
  });

  test("setup wizard shows when fleet not deployed", async ({ page }) => {
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupNotDeployed) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    // Wizard should be visible with prerequisites
    await expect(page.getByText("Prerequisites")).toBeVisible();
    await expect(page.getByText("cert-manager installed")).toBeVisible();
  });

  test("setup wizard navigates through steps", async ({ page }) => {
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupNotDeployed) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    await expect(page.getByText("Prerequisites")).toBeVisible();

    // Navigate to AWS step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder("Trust Anchor ID (ta-xxxxx)")).toBeVisible();

    // Navigate to OCI step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder("Tenancy OCID")).toBeVisible();

    // Navigate to Deploy step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder(/WireGuard endpoint/)).toBeVisible();
  });

  test("setup wizard saves AWS config", async ({ page }) => {
    let awsSaved = false;
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(awsSaved ? mockSetupDeployed : mockSetupNotDeployed),
      });
    });
    await page.route(`${API_PREFIX}/admin/relay/aws-config`, async (route: Route) => {
      awsSaved = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ configured: true }) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    // Navigate to AWS step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder("Trust Anchor ID (ta-xxxxx)")).toBeVisible();

    // Fill in AWS config
    await page.getByPlaceholder("Trust Anchor ID (ta-xxxxx)").fill("ta-test-123");
    await page.getByPlaceholder("Profile ID (p-xxxxx)").fill("p-test-456");
    await page.getByPlaceholder("Role ARN (arn:aws:iam::...)").fill("arn:aws:iam::123:role/relay");

    await page.getByText("Save Config").click();

    // Should show success indication
    await expect(page.getByText("AWS configured")).toBeVisible({ timeout: 5000 });
  });

  test("status dashboard shows healthy fleet", async ({ page }) => {
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupDeployed) });
    });
    await page.route(`${API_PREFIX}/admin/relay/status`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockStatusHealthy) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    // Dashboard should be visible
    await expect(page.getByText("2/2 relays active")).toBeVisible({ timeout: 5000 });
    await expect(page.getByText("54.210.123.45")).toBeVisible();
    await expect(page.getByText("150.230.67.89")).toBeVisible();
  });

  test("status dashboard shows provisioning error (US-43.10)", async ({ page }) => {
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupDeployed) });
    });
    await page.route(`${API_PREFIX}/admin/relay/status`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockStatusUnhealthy) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    await expect(page.getByText("Provisioning failed")).toBeVisible({ timeout: 5000 });
    await expect(page.getByText(/Invalid AMI id/)).toBeVisible();
  });

  test("status dashboard shows firing alerts (US-43.11)", async ({ page }) => {
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupDeployed) });
    });
    await page.route(`${API_PREFIX}/admin/relay/status`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockStatusUnhealthy) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    await expect(page.getByText("FIRING")).toBeVisible({ timeout: 5000 });
  });

  test("status dashboard triggers rotation", async ({ page }) => {
    let rotated = false;
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupDeployed) });
    });
    await page.route(`${API_PREFIX}/admin/relay/status`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockStatusHealthy) });
    });
    await page.route(`${API_PREFIX}/admin/relay/rotate/*`, async (route: Route) => {
      rotated = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ rotating: "aws-1" }) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    await expect(page.getByText("2/2 relays active")).toBeVisible({ timeout: 5000 });

    // Click first rotate button
    await page.getByText("Rotate").first().click();

    // Verify rotation was called
    await page.waitForTimeout(500);
    expect(rotated).toBe(true);
  });

  test("relay tab not visible to non-admin users", async ({ page }) => {
    // Override auth to non-admin
    await page.route(`${API_PREFIX}/auth/me`, async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: "user-1", email: "user@test.com", role: "user" }),
      });
    });

    await page.goto("/settings");

    // Relay tab should NOT be in the sidebar
    await expect(page.getByRole("button", { name: "Relay" })).not.toBeVisible({ timeout: 3000 });
  });
});
