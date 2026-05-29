"""LLMSafeSpace Python SDK client."""

from __future__ import annotations

from typing import Any

import httpx

from .errors import (
    AuthError,
    ConflictError,
    LLMSafeSpaceError,
    NotFoundError,
    RateLimitError,
    TimeoutError,
)
from .types import (
    APIKey,
    AuthResponse,
    EnsureSessionResponse,
    MessageResponse,
    SecretResponse,
    TerminalTicket,
    Workspace,
    WorkspaceListItem,
    WorkspaceListResult,
)


class LLMSafeSpace:
    """Synchronous client for the LLMSafeSpace API."""

    def __init__(
        self,
        base_url: str,
        *,
        api_key: str | None = None,
        email: str | None = None,
        password: str | None = None,
        timeout: float = 120.0,
    ):
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._email = email
        self._password = password
        self._timeout = timeout
        self._token: str | None = None
        self._client = httpx.Client(timeout=timeout)

        self.workspaces = _WorkspacesAPI(self)
        self.sessions = _SessionsAPI(self)
        self.auth = _AuthAPI(self)
        self.secrets = _SecretsAPI(self)
        self.terminal = _TerminalAPI(self)

    def close(self) -> None:
        self._client.close()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()

    def _request(self, method: str, path: str, *, json: Any = None, timeout: float | None = None) -> Any:
        url = f"{self._base_url}/api/v1{path}"
        headers = self._auth_headers()

        try:
            resp = self._client.request(method, url, headers=headers, json=json, timeout=timeout or self._timeout)
        except httpx.TimeoutException as e:
            raise TimeoutError(str(e)) from e

        if resp.status_code == 401 and self._email and self._token:
            self._token = None
            return self._request(method, path, json=json, timeout=timeout)

        if resp.status_code >= 400:
            self._raise_for_status(resp)

        if resp.status_code == 204:
            return None
        if resp.status_code == 202:
            return None
        return resp.json()

    def _auth_headers(self) -> dict[str, str]:
        if self._api_key:
            return {"Authorization": f"Bearer {self._api_key}"}
        if self._token:
            return {"Authorization": f"Bearer {self._token}"}
        if self._email and self._password:
            self._login()
            return {"Authorization": f"Bearer {self._token}"}
        return {}

    def _login(self) -> None:
        resp = self._client.post(
            f"{self._base_url}/api/v1/auth/login",
            json={"email": self._email, "password": self._password},
            timeout=10.0,
        )
        if resp.status_code != 200:
            raise AuthError("Login failed", resp.status_code)
        self._token = resp.json()["token"]

    @staticmethod
    def _raise_for_status(resp: httpx.Response) -> None:
        msg = "Unknown error"
        try:
            msg = resp.json().get("error", msg)
        except Exception:
            pass
        match resp.status_code:
            case 401 | 403:
                raise AuthError(msg, resp.status_code)
            case 404:
                raise NotFoundError(msg)
            case 409:
                raise ConflictError(msg)
            case 429:
                raise RateLimitError(msg)
            case _:
                raise LLMSafeSpaceError(msg, resp.status_code)


class _WorkspacesAPI:
    def __init__(self, client: LLMSafeSpace):
        self._c = client

    def list(self, limit: int = 20, offset: int = 0) -> WorkspaceListResult:
        data = self._c._request("GET", f"/workspaces?limit={limit}&offset={offset}")
        items = [WorkspaceListItem(**i) for i in data.get("items", [])]
        return WorkspaceListResult(items=items, pagination=data.get("pagination"))

    def create(self, *, name: str = "", runtime: str = "", storage_size: str = "") -> Workspace:
        body = {"name": name, "runtime": runtime, "storageSize": storage_size}
        return Workspace(**self._c._request("POST", "/workspaces", json=body))

    def get(self, workspace_id: str) -> Workspace:
        return Workspace(**self._c._request("GET", f"/workspaces/{workspace_id}"))

    def delete(self, workspace_id: str) -> None:
        self._c._request("DELETE", f"/workspaces/{workspace_id}")

    def suspend(self, workspace_id: str) -> None:
        self._c._request("POST", f"/workspaces/{workspace_id}/suspend")

    def resume(self, workspace_id: str) -> None:
        self._c._request("POST", f"/workspaces/{workspace_id}/resume")

    def activate(self, workspace_id: str) -> dict[str, str]:
        return self._c._request("POST", f"/workspaces/{workspace_id}/activate")

    def get_status(self, workspace_id: str) -> dict[str, Any]:
        return self._c._request("GET", f"/workspaces/{workspace_id}/status")


class _SessionsAPI:
    def __init__(self, client: LLMSafeSpace):
        self._c = client

    def ensure(self, workspace_id: str) -> EnsureSessionResponse:
        return EnsureSessionResponse(**self._c._request("POST", f"/workspaces/{workspace_id}/sessions/new"))

    def list(self, workspace_id: str) -> list[dict[str, Any]]:
        return self._c._request("GET", f"/workspaces/{workspace_id}/sessions")

    def send_message(self, workspace_id: str, session_id: str, content: str) -> MessageResponse:
        raw = self._c._request("POST", f"/workspaces/{workspace_id}/sessions/{session_id}/message", json={"content": content})
        text = _extract_text(raw)
        return MessageResponse(raw=raw, content=text)

    def get_history(self, workspace_id: str, session_id: str) -> list[Any]:
        return self._c._request("GET", f"/workspaces/{workspace_id}/sessions/{session_id}/message")

    def abort(self, workspace_id: str, session_id: str) -> None:
        self._c._request("POST", f"/workspaces/{workspace_id}/sessions/{session_id}/abort")


class _AuthAPI:
    def __init__(self, client: LLMSafeSpace):
        self._c = client

    def me(self) -> dict[str, Any]:
        return self._c._request("GET", "/auth/me")

    def list_api_keys(self) -> list[APIKey]:
        data = self._c._request("GET", "/auth/api-keys")
        return [APIKey(**k) for k in data]

    def create_api_key(self, name: str) -> APIKey:
        return APIKey(**self._c._request("POST", "/auth/api-keys", json={"name": name}))

    def delete_api_key(self, key_id: str) -> None:
        self._c._request("DELETE", f"/auth/api-keys/{key_id}")


class _SecretsAPI:
    def __init__(self, client: LLMSafeSpace):
        self._c = client

    def create(self, *, name: str, type: str, value: str, metadata: Any = None) -> SecretResponse:
        body: dict[str, Any] = {"name": name, "type": type, "value": value}
        if metadata is not None:
            body["metadata"] = metadata
        return SecretResponse(**self._c._request("POST", "/secrets", json=body))

    def list(self) -> list[SecretResponse]:
        return [SecretResponse(**s) for s in self._c._request("GET", "/secrets")]

    def get(self, secret_id: str) -> SecretResponse:
        return SecretResponse(**self._c._request("GET", f"/secrets/{secret_id}"))

    def delete(self, secret_id: str) -> None:
        self._c._request("DELETE", f"/secrets/{secret_id}")

    def reveal(self, secret_id: str) -> str:
        data = self._c._request("POST", f"/secrets/{secret_id}/reveal")
        return data["value"]


class _TerminalAPI:
    def __init__(self, client: LLMSafeSpace):
        self._c = client

    def get_ticket(self, workspace_id: str) -> TerminalTicket:
        return TerminalTicket(**self._c._request("POST", f"/workspaces/{workspace_id}/terminal/ticket"))


def _extract_text(raw: Any) -> str:
    """Extract text content from opencode response parts."""
    if not isinstance(raw, dict):
        return ""
    parts = raw.get("parts", [])
    if not isinstance(parts, list):
        return ""
    return "".join(p.get("text", "") for p in parts if isinstance(p, dict) and p.get("type") == "text")
