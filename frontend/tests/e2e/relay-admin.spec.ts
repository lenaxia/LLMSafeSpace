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
  // Mock other API calls the settings page makes
  await page.route(`${API_PREFIX}/users/me/settings`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ settings: {}, schemaVersion: 1 }) });
  });
  await page.route(`${API_PREFIX}/users/me/settings/schema`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ settings: [], schemaVersion: 1 }) });
  });
  await page.route(`${API_PREFIX}/provider-credentials`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) });
  });
  await page.route(`${API_PREFIX}/secrets`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) });
  });
  await page.route(`${API_PREFIX}/api-keys`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) });
  });
  await page.route(`${API_PREFIX}/orgs`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) });
  });
  await page.route(`${API_PREFIX}/events`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) });
  });
}

const mockSetupNotDeployed = {
  deployed: false,
  metalLBInstalled: true,
  routerDeployed: true,
  crdInstalled: true,
  ociConfigured: false,
  gcpConfigured: false,
  wireGuardEndpoint: "",
};

const mockSetupDeployed = {
  ...mockSetupNotDeployed,
  deployed: true,
  ociConfigured: true,
  gcpConfigured: true,
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
      id: "oci-1",
      provider: "oci",
      region: "us-ashburn-1",
      wgIP: "10.42.42.2",
      publicIP: "150.230.67.89",
      state: "healthy",
      healthy: true,
      metrics: { requestsToday: 12847, requests429Today: 0, totalRequests: 450000, egressBytes: 149546362, egressLimitBytes: 10995116277760, activeStreams: 3 },
      cost: { monthlyEstimate: 0, spentThisMonth: 0 },
    },
    {
      id: "gcp-1",
      provider: "gcp",
      region: "us-west1",
      wgIP: "10.42.42.3",
      publicIP: "34.16.50.1",
      state: "healthy",
      healthy: true,
      metrics: { requestsToday: 0, requests429Today: 0, totalRequests: 0, egressBytes: 0, egressLimitBytes: 1073741824, activeStreams: 0 },
      cost: { monthlyEstimate: 0, spentThisMonth: 0 },
    },
  ],
  conditions: [],
  recentEvents: [
    { timestamp: new Date().toISOString(), type: "Rotated", message: "OCI relay rotated", severity: "info" },
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
      ...mockStatusHealthy.instances[0]!,
      state: "provisioning-failed",
      healthy: false,
      lastProvisionError: "Out of host capacity",
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

    await expect(page.getByText("Prerequisites")).toBeVisible();
    await expect(page.getByText("MetalLB installed")).toBeVisible();
  });

  test("setup wizard navigates through steps", async ({ page }) => {
    await page.route(`${API_PREFIX}/admin/relay/setup`, async (route: Route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(mockSetupNotDeployed) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    await expect(page.getByText("Prerequisites")).toBeVisible();

    // OCI step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder("Tenancy OCID")).toBeVisible();

    // GCP step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder("Service Account JSON")).toBeVisible();

    // Deploy step
    await page.getByText("Next →").click();
    await expect(page.getByPlaceholder(/WireGuard endpoint/)).toBeVisible();
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

    await expect(page.getByText("2/2 relays active")).toBeVisible({ timeout: 5000 });
    await expect(page.getByText("150.230.67.89")).toBeVisible();
    await expect(page.getByText("34.16.50.1")).toBeVisible();
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
    await expect(page.getByText(/Out of host capacity/)).toBeVisible();
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
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ rotating: "oci-1" }) });
    });

    await page.goto("/settings");
    await page.getByText("Relay").click();

    await expect(page.getByText("2/2 relays active")).toBeVisible({ timeout: 5000 });
    await page.getByText("Rotate").first().click();
    await page.waitForTimeout(500);
    expect(rotated).toBe(true);
  });

  test("relay tab not visible to non-admin users", async ({ page }) => {
    await page.route(`${API_PREFIX}/auth/me`, async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: "user-1", email: "user@test.com", role: "user" }),
      });
    });

    await page.goto("/settings");
    await expect(page.getByRole("button", { name: "Relay" })).not.toBeVisible({ timeout: 3000 });
  });
});
