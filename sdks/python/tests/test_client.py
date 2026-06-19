"""Unit tests for LLMSafeSpaces Python SDK."""

import pytest
import httpx
import respx

from llmsafespaces import LLMSafeSpaces, NotFoundError, AuthError, TimeoutError, MessageResponse


BASE = "http://localhost:8080/api/v1"


@respx.mock
def test_list_workspaces():
    respx.get(f"{BASE}/workspaces?limit=20&offset=0").respond(
        json={"items": [{"id": "ws-1", "name": "test", "userId": "u1", "runtime": "python", "storageSize": "10Gi", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"}], "pagination": None}
    )
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    result = client.workspaces.list()
    assert len(result.items) == 1
    assert result.items[0].id == "ws-1"


@respx.mock
def test_create_workspace():
    respx.post(f"{BASE}/workspaces").respond(
        status_code=201,
        json={"id": "ws-new", "name": "my-ws", "userId": "u1", "runtime": "python:3.11", "storageSize": "10Gi", "phase": "Pending", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"},
    )
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    ws = client.workspaces.create(name="my-ws", runtime="python:3.11", storage_size="10Gi")
    assert ws.id == "ws-new"


@respx.mock
def test_not_found():
    respx.get(f"{BASE}/workspaces/nonexistent").respond(status_code=404, json={"error": "workspace not found"})
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    with pytest.raises(NotFoundError):
        client.workspaces.get("nonexistent")


@respx.mock
def test_auth_error():
    respx.get(f"{BASE}/auth/me").respond(status_code=401, json={"error": "authentication required"})
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_bad")
    with pytest.raises(AuthError):
        client.auth.me()


@respx.mock
def test_send_message_extracts_content():
    opencode_resp = {
        "id": "msg-1",
        "role": "assistant",
        "parts": [
            {"type": "text", "text": "Hello "},
            {"type": "text", "text": "world!"},
            {"type": "tool-invocation", "toolName": "read_file"},
        ],
    }
    respx.post(f"{BASE}/workspaces/ws-1/sessions/sess-1/message").respond(json=opencode_resp)
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    result = client.sessions.send_message("ws-1", "sess-1", "hi")
    assert isinstance(result, MessageResponse)
    assert result.content == "Hello world!"
    assert result.raw == opencode_resp


@respx.mock
def test_ensure_session():
    respx.post(f"{BASE}/workspaces/ws-1/sessions/new").respond(
        json={"workspaceId": "ws-1", "workspacePhase": "Active", "sessionId": "sess-1", "resumed": False}
    )
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    result = client.sessions.ensure("ws-1")
    assert result.sessionId == "sess-1"


@respx.mock
def test_terminal_ticket():
    respx.post(f"{BASE}/workspaces/ws-1/terminal/ticket").respond(
        json={"ticket": "tkt_abc123", "expiresAt": "2026-05-29T18:00:00Z"}
    )
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    ticket = client.terminal.get_ticket("ws-1")
    assert ticket.ticket == "tkt_abc123"


@respx.mock
def test_api_key_header():
    route = respx.get(f"{BASE}/auth/me").respond(json={"id": "u1", "username": "test"})
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_mykey")
    client.auth.me()
    assert route.calls[0].request.headers["authorization"] == "Bearer lsp_mykey"


@respx.mock
def test_auto_login_with_credentials():
    respx.post(f"{BASE}/auth/login").respond(json={"token": "jwt-abc", "user": {"id": "u1"}})
    respx.get(f"{BASE}/auth/me").respond(json={"id": "u1", "username": "test"})
    client = LLMSafeSpaces("http://localhost:8080", email="test@example.com", password="pass123")
    result = client.auth.me()
    assert result["id"] == "u1"


@respx.mock
def test_suspend_workspace():
    respx.post(f"{BASE}/workspaces/ws-1/suspend").respond(status_code=202)
    client = LLMSafeSpaces("http://localhost:8080", api_key="lsp_test")
    # Should not raise
    client.workspaces.suspend("ws-1")
