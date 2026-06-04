/**
 * E2E test for Epic 16: Agent input requests (questions + permissions).
 *
 * Uses Playwright route interception to mock the backend. Tests the full
 * browser flow: SSE event → prompt renders → user interacts → API call fires.
 */
import { test, expect } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const WORKSPACE_ID = "ws-e2e-input";
const SESSION_ID = "ses_e2e_input";
const API_PREFIX = "**/api/v1";

async function setupAPIMocks(page: Page) {
  await page.route(`${API_PREFIX}/auth/me`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "u1", username: "testuser", email: "t@t.com", role: "user", active: true }) });
  });
  await page.route(`${API_PREFIX}/auth/config`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ registrationEnabled: false, oidcEnabled: false, instanceName: "test" }) });
  });
  await page.route(`${API_PREFIX}/workspaces`, async (route: Route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ id: WORKSPACE_ID, name: "Input Test", userId: "u1", runtime: "python", storageSize: "1Gi", phase: "Active" }], pagination: { limit: 50, offset: 0, total: 1 } }) });
    } else { await route.continue(); }
  });
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/status`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ phase: "Active", credentialState: { available: true }, agentHealth: { status: "healthy", agentVersion: "1.0.0" }, sessions: [{ id: SESSION_ID, status: "busy" }] }) });
  });
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/sessions`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([{ id: SESSION_ID, title: "Input Test", messageCount: 0, status: "busy" }]) });
  });
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/sessions/*/message`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) });
  });
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/sessions/*/ensure`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: SESSION_ID }) });
  });

  // Question/permission API endpoints
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/question/*/reply`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
  });
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/question/*/reject`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
  });
  await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/permission/*/reply`, async (route: Route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
  });
}

test.describe("Epic 16: Agent input requests (mocked backend)", () => {
  test.beforeEach(async ({ page }) => {
    await setupAPIMocks(page);
  });

  test("question prompt renders when agent.question SSE event arrives", async ({ page }) => {
    // Pre-load the SSE with a question event
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = { type: "agent.question", data: { id: "que_e2e1", session_id: SESSION_ID, questions: [{ header: "Choose DB", question: "Which database?", options: [{ label: "PostgreSQL", description: "Relational" }, { label: "MongoDB", description: "Document" }] }] } };
      const body = `data: ${JSON.stringify(event)}\n\n`;
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Which database?")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole("button", { name: "PostgreSQL" })).toBeVisible();
    await expect(page.getByRole("button", { name: "MongoDB" })).toBeVisible();
  });

  test("user can select option and submit question answer", async ({ page }) => {
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = { type: "agent.question", data: { id: "que_e2e2", session_id: SESSION_ID, questions: [{ header: "Language", question: "Pick one", options: [{ label: "Go", description: "Fast" }, { label: "Rust", description: "Safe" }] }] } };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    let replyCalled = false;
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/question/que_e2e2/reply`, async (route: Route) => {
      replyCalled = true;
      const body = await route.request().postDataJSON();
      expect(body.answers).toEqual([["Go"]]);
      await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Pick one")).toBeVisible({ timeout: 10_000 });

    await page.getByRole("button", { name: "Go" }).click();
    await page.getByText("Submit answers").click();

    // Prompt should disappear after submit
    await expect(page.getByText("Pick one")).not.toBeVisible({ timeout: 5_000 });
    expect(replyCalled).toBe(true);
  });

  test("permission prompt renders and user can approve", async ({ page }) => {
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = { type: "agent.permission", data: { id: "per_e2e1", session_id: SESSION_ID, permission: "shell", patterns: ["rm -rf /tmp/cache"] } };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    let replyCalled = false;
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/permission/per_e2e1/reply`, async (route: Route) => {
      replyCalled = true;
      const body = await route.request().postDataJSON();
      expect(body.reply).toBe("always");
      await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Run shell command")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText("rm -rf /tmp/cache")).toBeVisible();

    await page.getByText("Allow always").click();
    await expect(page.getByText("Run shell command")).not.toBeVisible({ timeout: 5_000 });
    expect(replyCalled).toBe(true);
  });

  test("permission deny shows feedback input", async ({ page }) => {
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = { type: "agent.permission", data: { id: "per_e2e2", session_id: SESSION_ID, permission: "write", patterns: ["/etc/passwd"] } };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Write file")).toBeVisible({ timeout: 10_000 });

    // First click shows feedback
    await page.getByText("Deny").click();
    await expect(page.getByLabel("Feedback")).toBeVisible();

    // Type feedback and confirm
    await page.getByLabel("Feedback").fill("Not safe");
    await page.getByText("Confirm deny").click();
    await expect(page.getByText("Write file")).not.toBeVisible({ timeout: 5_000 });
  });

  test("question dismiss calls reject API", async ({ page }) => {
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = { type: "agent.question", data: { id: "que_e2e3", session_id: SESSION_ID, questions: [{ header: "Test", question: "Dismiss me", options: [{ label: "A", description: "" }] }] } };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    let rejectCalled = false;
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/question/que_e2e3/reject`, async (route: Route) => {
      rejectCalled = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Dismiss me")).toBeVisible({ timeout: 10_000 });
    await page.locator("button", { hasText: /^Dismiss$/ }).click();
    await expect(page.getByText("Dismiss me")).not.toBeVisible({ timeout: 5_000 });
    expect(rejectCalled).toBe(true);
  });

  // Subtask bubbling: opencode's `task` tool spawns a subagent session whose
  // permission/question events carry the subtask's session_id, not the
  // user-visible parent. The backend stamps root_session_id with the parent
  // so the chat UI can bubble the prompt into the right view.
  // Without this fix the prompt was silently dropped — see worklog 0121.
  test("subtask permission bubbles to parent session via root_session_id", async ({ page }) => {
    const SUBTASK_ID = "ses_subtask_xyz";
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = {
        type: "agent.permission",
        data: {
          id: "per_subtask_e2e",
          session_id: SUBTASK_ID,         // subtask's session
          root_session_id: SESSION_ID,    // parent session — what the user is viewing
          permission: "shell",
          patterns: ["ls /workspace"],
        },
      };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    let replyCalled = false;
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/permission/per_subtask_e2e/reply`, async (route: Route) => {
      replyCalled = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: "true" });
    });

    // Navigate to the PARENT session — without root_session_id support the
    // prompt would never appear here.
    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Run shell command")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText("ls /workspace")).toBeVisible();

    await page.getByText("Allow always").click();
    await expect(page.getByText("Run shell command")).not.toBeVisible({ timeout: 5_000 });
    expect(replyCalled).toBe(true);
  });

  test("subtask question bubbles to parent session via root_session_id", async ({ page }) => {
    const SUBTASK_ID = "ses_subtask_q";
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = {
        type: "agent.question",
        data: {
          id: "que_subtask_e2e",
          session_id: SUBTASK_ID,
          root_session_id: SESSION_ID,
          questions: [{ header: "Subagent confirm", question: "Proceed with refactor?", options: [{ label: "Yes", description: "go" }, { label: "No", description: "stop" }] }],
        },
      };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    await expect(page.getByText("Proceed with refactor?")).toBeVisible({ timeout: 10_000 });
  });

  test("subtask permission for a different parent tree is NOT shown", async ({ page }) => {
    // Two subtask trees coexist in the same workspace. The user is viewing
    // tree A (SESSION_ID); the event is for tree B. Must NOT render.
    await page.route(`${API_PREFIX}/workspaces/${WORKSPACE_ID}/session-events`, async (route: Route) => {
      const event = {
        type: "agent.permission",
        data: {
          id: "per_other_tree",
          session_id: "ses_other_subtask",
          root_session_id: "ses_other_parent", // different parent
          permission: "shell",
          patterns: ["echo hi"],
        },
      };
      await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }, body: `data: ${JSON.stringify(event)}\n\n` });
    });

    await page.goto(`/chat/${WORKSPACE_ID}/${SESSION_ID}`);
    // Wait long enough that any erroneous render would have happened.
    await page.waitForTimeout(2000);
    await expect(page.getByText("Run shell command")).not.toBeVisible();
  });
});
