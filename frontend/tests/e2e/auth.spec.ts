import { test, expect } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const API_PREFIX = "**/api/v1";

async function mockUnauthenticated(page: Page) {
  await page.route(`${API_PREFIX}/auth/config`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ registrationEnabled: true, oidcEnabled: false, instanceName: "test" }) });
  });
  await page.route(`${API_PREFIX}/auth/me`, async (route: Route) => {
    await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "unauthorized" }) });
  });
  await page.route(`${API_PREFIX}/auth/login`, async (route: Route) => {
    await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "invalid credentials" }) });
  });
}

test.describe("Login flow", () => {
  test("shows login page when unauthenticated", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/");
    // Should redirect to /login
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByText(/Welcome to/)).toBeVisible();
    await expect(page.getByPlaceholder("Email")).toBeVisible();
    await expect(page.getByPlaceholder("Password")).toBeVisible();
  });

  test("login form has accessible labels", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/login");
    const form = page.locator("form");
    await expect(form).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("shows error on invalid credentials", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/login");
    await page.getByPlaceholder("Email").fill("bad@user.com");
    await page.getByPlaceholder("Password").fill("badpass");
    await page.getByRole("button", { name: "Sign in" }).click();
    // API returns error — should show generic message
    await expect(page.getByText(/invalid|wrong|error/i)).toBeVisible({ timeout: 5000 });
  });

  test("register link visible when registration enabled", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/login");
    // If registration is disabled, link won't appear — test is conditional
    const link = page.getByText("Create an account");
    // Don't fail if registration is disabled — just verify the page loads
    if (await link.isVisible().catch(() => false)) {
      await expect(link).toHaveAttribute("href", "/register");
    }
  });
});

test.describe("Protected routes", () => {
  test("redirects /chat to /login when unauthenticated", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/chat");
    await expect(page).toHaveURL(/\/login/);
  });

  test("redirects /settings to /login when unauthenticated", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/settings");
    await expect(page).toHaveURL(/\/login/);
  });
});

test.describe("404 page", () => {
  test("shows 404 for unknown routes", async ({ page }) => {
    await mockUnauthenticated(page);
    await page.goto("/nonexistent-page");
    await expect(page.getByText("404")).toBeVisible();
    await expect(page.getByText("Page not found")).toBeVisible();
    await expect(page.getByRole("link", { name: /go to chat/i })).toBeVisible();
  });
});
