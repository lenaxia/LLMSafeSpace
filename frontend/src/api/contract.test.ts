import { describe, expect, it } from "vitest";
import fixtures from "./contract-fixtures.json";
import type {
  AuthConfig,
  ActivateWorkspaceResponse,
  ActiveSessionsResponse,
  SessionListItem,
  WorkspaceListItem,
  AuthResponse,
  QuestionRequest,
  PermissionRequest,
} from "./types";

/**
 * Contract tests: validate that the Go-generated JSON fixtures are
 * assignable to our TypeScript types. If a Go struct field is renamed,
 * added, or removed, these tests will catch the drift.
 *
 * Regenerate fixtures: go test -run TestGenerateContractFixtures ./pkg/types/
 */

describe("Go↔TS contract", () => {
  it("AuthConfig matches Go shape", () => {
    const data: AuthConfig = fixtures.AuthConfig;
    expect(data.registrationEnabled).toBe(true);
    expect(data.oidcEnabled).toBe(false);
    expect(data.ssoProviders).toEqual(["okta"]);
  });

  it("ActivateWorkspaceResponse matches Go shape", () => {
    const data: ActivateWorkspaceResponse = fixtures.ActivateWorkspaceResponse;
    expect(data.resumed).toBe("ws-1");
    expect(data.suspended).toBe("ws-old");
  });

  it("ActiveSessionsResponse matches Go shape", () => {
    const data: ActiveSessionsResponse = fixtures.ActiveSessionsResponse;
    expect(data.active).toEqual(["sess-1", "sess-2"]);
    expect(data.maxActive).toBe(5);
  });

  it("SessionListItem matches Go shape", () => {
    const data: SessionListItem = fixtures.SessionListItem;
    expect(data.id).toBe("sess-1");
    expect(data.title).toBe("Chat about auth");
    expect(data.lastMessageAt).toBeDefined();
    expect(data.messageCount).toBe(12);
    expect(data.status).toBe("active");
    expect(data.lastSeenAt).toBeDefined();
    expect(data.hasUnread).toBe(true);
    expect(data.contextUsed).toBe(12500);
  });

  it("WorkspaceListItem matches Go shape", () => {
    const data: WorkspaceListItem = fixtures.WorkspaceListItem;
    expect(data.id).toBe("ws-1");
    expect(data.name).toBe("alpha");
    expect(data.userId).toBe("u-123");
    expect(data.runtime).toBe("python:3.11");
    expect(data.storageSize).toBe("5Gi");
    expect(data.phase).toBe("Active");
    expect(data.maxActiveSessions).toBe(5);
    expect(data.createdAt).toBeDefined();
    expect(data.updatedAt).toBeDefined();
  });

  it("AuthResponse matches Go shape", () => {
    const data: AuthResponse = fixtures.AuthResponse;
    expect(data.token).toBe("jwt-token");
    expect(data.user.id).toBe("u-123");
    expect(data.user.username).toBe("alice");
    expect(data.user.email).toBe("alice@test.com");
    expect(data.user.role).toBe("user");
    expect(data.user.active).toBe(true);
  });

  it("all fixture keys have corresponding tests", () => {
    const testedKeys = [
      "AuthConfig", "ActivateWorkspaceResponse", "ActiveSessionsResponse",
      "SessionListItem", "WorkspaceListItem", "AuthResponse",
      "QuestionRequest", "PermissionRequest",
    ];
    const fixtureKeys = Object.keys(fixtures);
    expect(fixtureKeys.sort()).toEqual(testedKeys.sort());
  });

  it("QuestionRequest matches Go shape", () => {
    const data: QuestionRequest = fixtures.QuestionRequest;
    expect(data.id).toBe("que_18b28260affeoxXrX1iwPH8wFg");
    expect(data.session_id).toBe("ses_18b28260affeoxXrX1iwPH8wFg");
    expect(data.questions).toHaveLength(1);
    const q = data.questions[0]!;
    expect(q.header).toBe("Choose language");
    expect(q.options).toHaveLength(2);
    expect(q.options[0]!.label).toBe("Go");
    expect(q.multiple).toBe(false);
    expect(data.tool?.message_id).toBe("msg_abc");
    expect(data.tool?.call_id).toBe("call_xyz");
  });

  it("PermissionRequest matches Go shape", () => {
    const data: PermissionRequest = fixtures.PermissionRequest;
    expect(data.id).toBe("per_18b28260affeoxXrX1iwPH8wFg");
    expect(data.session_id).toBe("ses_18b28260affeoxXrX1iwPH8wFg");
    expect(data.permission).toBe("shell");
    expect(data.patterns).toEqual(["/workspace/src/main.go"]);
    expect(data.metadata).toEqual({ command: "go build" });
    expect(data.always).toEqual(["/workspace/*"]);
    expect(data.tool?.message_id).toBe("msg_abc");
  });
});
