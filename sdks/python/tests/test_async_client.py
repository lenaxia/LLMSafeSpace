"""Tests for AsyncLLMSafeSpace — the async Python SDK client (US-14.4)."""

from __future__ import annotations

import httpx
import pytest
import respx

from llmsafespace import AsyncLLMSafeSpace
from llmsafespace.errors import AuthError, NotFoundError, RateLimitError


BASE = "https://llmsafespace.test"


@pytest.fixture
async def client():
    c = AsyncLLMSafeSpace(BASE, api_key="lsp_test")
    yield c
    await c.close()


@respx.mock
@pytest.mark.asyncio
async def test_async_list_workspaces(client: AsyncLLMSafeSpace):
    respx.get(f"{BASE}/api/v1/workspaces").mock(
        return_value=httpx.Response(200, json={
            "items": [{
                "id": "ws-1", "name": "x", "userId": "u1", "runtime": "python",
                "storageSize": "10Gi", "createdAt": "2026-01-01T00:00:00Z",
                "updatedAt": "2026-01-01T00:00:00Z", "phase": "Active",
            }],
            "pagination": {},
        })
    )
    result = await client.workspaces.list()
    assert len(result.items) == 1
    assert result.items[0].id == "ws-1"


@respx.mock
@pytest.mark.asyncio
async def test_async_get_workspace(client: AsyncLLMSafeSpace):
    respx.get(f"{BASE}/api/v1/workspaces/ws-1").mock(
        return_value=httpx.Response(200, json={
            "id": "ws-1", "name": "x", "userId": "u1", "runtime": "python",
            "storageSize": "10Gi", "phase": "Active",
            "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
        })
    )
    ws = await client.workspaces.get("ws-1")
    assert ws.id == "ws-1"


@respx.mock
@pytest.mark.asyncio
async def test_async_send_message_extracts_text(client: AsyncLLMSafeSpace):
    respx.post(f"{BASE}/api/v1/workspaces/ws-1/sessions/ses-1/message").mock(
        return_value=httpx.Response(200, json={"parts": [{"type": "text", "text": "hello"}]})
    )
    resp = await client.sessions.send_message("ws-1", "ses-1", "hi")
    assert resp.content == "hello"


@respx.mock
@pytest.mark.asyncio
async def test_async_ensure_session(client: AsyncLLMSafeSpace):
    respx.post(f"{BASE}/api/v1/workspaces/ws-1/sessions/new").mock(
        return_value=httpx.Response(200, json={
            "workspaceId": "ws-1", "workspacePhase": "Active",
            "sessionId": "ses-new", "resumed": False,
        })
    )
    r = await client.sessions.ensure("ws-1")
    assert r.sessionId == "ses-new"


@respx.mock
@pytest.mark.asyncio
async def test_async_not_found(client: AsyncLLMSafeSpace):
    respx.get(f"{BASE}/api/v1/workspaces/missing").mock(return_value=httpx.Response(404, json={"error": "nope"}))
    with pytest.raises(NotFoundError):
        await client.workspaces.get("missing")


@respx.mock
@pytest.mark.asyncio
async def test_async_auth_error(client: AsyncLLMSafeSpace):
    respx.get(f"{BASE}/api/v1/workspaces").mock(return_value=httpx.Response(403, json={"error": "forbidden"}))
    with pytest.raises(AuthError):
        await client.workspaces.list()


@respx.mock
@pytest.mark.asyncio
async def test_async_rate_limit(client: AsyncLLMSafeSpace):
    respx.get(f"{BASE}/api/v1/workspaces").mock(return_value=httpx.Response(429, json={"error": "slow down"}))
    with pytest.raises(RateLimitError):
        await client.workspaces.list()


@respx.mock
@pytest.mark.asyncio
async def test_async_terminal_ticket(client: AsyncLLMSafeSpace):
    respx.post(f"{BASE}/api/v1/workspaces/ws-1/terminal/ticket").mock(
        return_value=httpx.Response(200, json={"ticket": "abc123", "expiresAt": "2026-01-01T00:00:00Z"})
    )
    t = await client.terminal.get_ticket("ws-1")
    assert t.ticket == "abc123"


@respx.mock
@pytest.mark.asyncio
async def test_async_context_manager():
    respx.get(f"{BASE}/api/v1/workspaces").mock(
        return_value=httpx.Response(200, json={"items": [], "pagination": {}})
    )
    async with AsyncLLMSafeSpace(BASE, api_key="lsp_x") as c:
        result = await c.workspaces.list()
        assert result.items == []


@respx.mock
@pytest.mark.asyncio
async def test_async_login_with_credentials():
    route = respx.post(f"{BASE}/api/v1/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get(f"{BASE}/api/v1/workspaces").mock(
        return_value=httpx.Response(200, json={"items": [], "pagination": {}})
    )
    async with AsyncLLMSafeSpace(BASE, email="u@x.com", password="pw") as c:
        await c.workspaces.list()
    assert route.called


@respx.mock
@pytest.mark.asyncio
async def test_async_401_relogin_after_token_expiry():
    login = respx.post(f"{BASE}/api/v1/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt2"})
    )
    respx.get(f"{BASE}/api/v1/workspaces").mock(
        side_effect=[
            httpx.Response(401, json={"error": "expired"}),
            httpx.Response(200, json={"items": [], "pagination": {}}),
        ]
    )
    async with AsyncLLMSafeSpace(BASE, email="u@x.com", password="pw") as c:
        await c.workspaces.list()  # first call: 401 → clear token → re-login → retry → 200
    assert login.call_count == 2


@respx.mock
@pytest.mark.asyncio
async def test_async_concurrent_requests_run_in_parallel(client: AsyncLLMSafeSpace):
    import asyncio
    respx.get(f"{BASE}/api/v1/workspaces").mock(
        return_value=httpx.Response(200, json={"items": [], "pagination": {}})
    )
    await asyncio.gather(*(client.workspaces.list() for _ in range(10)))
