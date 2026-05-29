/** Base error for all LLMSafeSpace API errors. */
export class LLMSafeSpaceError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
  ) {
    super(message);
    this.name = "LLMSafeSpaceError";
  }
}

export class AuthError extends LLMSafeSpaceError {
  constructor(message: string, status: number = 401) {
    super(message, status, "AUTH_ERROR");
    this.name = "AuthError";
  }
}

export class NotFoundError extends LLMSafeSpaceError {
  constructor(message: string) {
    super(message, 404, "NOT_FOUND");
    this.name = "NotFoundError";
  }
}

export class ConflictError extends LLMSafeSpaceError {
  constructor(message: string) {
    super(message, 409, "CONFLICT");
    this.name = "ConflictError";
  }
}

export class TimeoutError extends LLMSafeSpaceError {
  constructor(message: string = "Request timed out — the prompt may still be processing") {
    super(message, 0, "TIMEOUT");
    this.name = "TimeoutError";
  }
}

export class RateLimitError extends LLMSafeSpaceError {
  constructor(message: string = "Rate limit exceeded") {
    super(message, 429, "RATE_LIMIT");
    this.name = "RateLimitError";
  }
}
