import { test, expect } from "@playwright/test";

// These tests require a running backend with auth.
// They use a helper to log in first.
async function loginAs(page: import("@playwright/test").Page, username: string, password: string) {
  await page.goto("/login");
  await page.getByPlaceholder("Username").fill(username);
  await page.getByPlaceholder("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  // Wait for redirect to /chat
  await page.waitForURL(/\/chat/, { timeout: 5000 }).catch(() => {});
}

test.describe("Chat page (authenticated)", () => {
  test.skip(
    !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD,
    "Skipped: E2E_USERNAME and E2E_PASSWORD env vars required",
  );

  test.beforeEach(async ({ page }) => {
    await loginAs(page, process.env.E2E_USERNAME!, process.env.E2E_PASSWORD!);
  });

  test("shows workspace selection prompt when no workspace selected", async ({ page }) => {
    await page.goto("/chat");
    await expect(page.getByText("Select a workspace to start chatting")).toBeVisible();
  });

  test("sidebar shows workspace list", async ({ page }) => {
    await page.goto("/chat");
    const sidebar = page.locator("aside");
    await expect(sidebar).toBeVisible();
    await expect(sidebar.getByText("Safe Space")).toBeVisible();
  });

  test("settings page renders tabs", async ({ page }) => {
    await page.goto("/settings");
    await expect(page.getByText("API Keys")).toBeVisible();
    await expect(page.getByText("Appearance")).toBeVisible();
  });

  test("theme toggle works", async ({ page }) => {
    await page.goto("/settings");
    // Click Appearance tab
    await page.getByRole("button", { name: "Appearance" }).click();
    // Click Dark
    await page.getByRole("button", { name: "Dark" }).click();
    // Verify dark class on html
    await expect(page.locator("html")).toHaveClass(/dark/);
    // Click Light
    await page.getByRole("button", { name: "Light" }).click();
    await expect(page.locator("html")).not.toHaveClass(/dark/);
  });

  test("logout clears session and redirects to login", async ({ page }) => {
    await page.goto("/chat");
    await page.getByLabel("Log out").click();
    await expect(page).toHaveURL(/\/login/);
  });
});
