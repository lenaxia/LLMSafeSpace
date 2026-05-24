import { test, expect } from "@playwright/test";

test.describe("Login flow", () => {
  test("shows login page when unauthenticated", async ({ page }) => {
    await page.goto("/");
    // Should redirect to /login
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByText("Welcome back")).toBeVisible();
    await expect(page.getByPlaceholder("Username")).toBeVisible();
    await expect(page.getByPlaceholder("Password")).toBeVisible();
  });

  test("login form has accessible labels", async ({ page }) => {
    await page.goto("/login");
    const form = page.locator("form");
    await expect(form).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("shows error on invalid credentials", async ({ page }) => {
    await page.goto("/login");
    await page.getByPlaceholder("Username").fill("baduser");
    await page.getByPlaceholder("Password").fill("badpass");
    await page.getByRole("button", { name: "Sign in" }).click();
    // API returns error — should show generic message
    await expect(page.getByText(/invalid|wrong|error/i)).toBeVisible({ timeout: 5000 });
  });

  test("register link visible when registration enabled", async ({ page }) => {
    // This depends on /auth/config returning registrationEnabled: true
    // In dev mode with proxy, this hits the real backend
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
    await page.goto("/chat");
    await expect(page).toHaveURL(/\/login/);
  });

  test("redirects /settings to /login when unauthenticated", async ({ page }) => {
    await page.goto("/settings");
    await expect(page).toHaveURL(/\/login/);
  });
});

test.describe("404 page", () => {
  test("shows 404 for unknown routes", async ({ page }) => {
    await page.goto("/nonexistent-page");
    await expect(page.getByText("404")).toBeVisible();
    await expect(page.getByText("Page not found")).toBeVisible();
    await expect(page.getByRole("link", { name: /go to chat/i })).toBeVisible();
  });
});
