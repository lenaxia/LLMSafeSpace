"""Typed error hierarchy for LLMSafeSpace API errors."""


class LLMSafeSpaceError(Exception):
    """Base error for all API errors."""

    def __init__(self, message: str, status: int = 0, code: str | None = None):
        super().__init__(message)
        self.status = status
        self.code = code


class AuthError(LLMSafeSpaceError):
    """Authentication or authorization failure (401/403)."""

    def __init__(self, message: str = "Authentication required", status: int = 401):
        super().__init__(message, status, "AUTH_ERROR")


class NotFoundError(LLMSafeSpaceError):
    """Resource not found (404)."""

    def __init__(self, message: str = "Resource not found"):
        super().__init__(message, 404, "NOT_FOUND")


class ConflictError(LLMSafeSpaceError):
    """Conflict state (409)."""

    def __init__(self, message: str = "Conflict"):
        super().__init__(message, 409, "CONFLICT")


class TimeoutError(LLMSafeSpaceError):
    """Request timed out — prompt may still be processing."""

    def __init__(self, message: str = "Request timed out"):
        super().__init__(message, 0, "TIMEOUT")


class RateLimitError(LLMSafeSpaceError):
    """Rate limit exceeded (429)."""

    def __init__(self, message: str = "Rate limit exceeded"):
        super().__init__(message, 429, "RATE_LIMIT")
