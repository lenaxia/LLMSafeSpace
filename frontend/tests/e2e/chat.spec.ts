import { test, expect } from "@playwright/test";

async function loginAs(page: import("@playwright/test").Page, username: string, password: string) {
  await page.goto("/login");
  await page.getByPlaceholder("Username").fill(username);
  await page.getByPlaceholder("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  // Wait for either redirect to /chat OR error message
  await Promise.race([
    page.waitForURL(/\/chat/, { timeout: 5000 }),
    page.getByText(/invalid|error/i).waitFor({ timeout: 5000 }),
  ]);
  // If we're still on /login, login failed
  if (page.url().includes("/login")) {
    throw new Error("Login failed — check E2E_USERNAME/E2E_PASSWORD and that the user exists");
  }
}

test.describe("Chat page (authenticated)", () => {
  test.skip(
    () => !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD,
    "Skipped: E2E_USERNAME and E2E_PASSWORD env vars required",
  );

  test.beforeEach(async ({ page }) => {
    await loginAs(page, process.env.E2E_USERNAME!, process.env.E2E_PASSWORD!);
  });

  test("shows workspace selection prompt when no workspace selected", async ({ page }) => {
    await expect(page.getByText("Select a workspace to start chatting")).toBeVisible();
  });

  test("sidebar shows workspace list", async ({ page }) => {
    const sidebar = page.locator("aside");
    await expect(sidebar).toBeVisible();
    await expect(sidebar.getByText("Safe Space")).toBeVisible();
  });

  test("settings page renders tabs", async ({ page }) => {
    await page.goto("/settings");
    await expect(page.getByRole("button", { name: "API Keys" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Appearance" })).toBeVisible();
  });

  test("theme toggle works", async ({ page }) => {
    await page.goto("/settings");
    await page.getByRole("button", { name: "Appearance" }).click();
    await page.getByRole("button", { name: "Dark" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.getByRole("button", { name: "Light" }).click();
    await expect(page.locator("html")).not.toHaveClass(/dark/);
  });

  test("logout clears session and redirects to login", async ({ page }) => {
    await page.getByLabel("Log out").click();
    await expect(page).toHaveURL(/\/login/);
  });
});
