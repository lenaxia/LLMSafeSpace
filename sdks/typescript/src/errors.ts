/** Base error for all LLMSafeSpaces API errors. */
export class LLMSafeSpacesError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
  ) {
    super(message);
    this.name = "LLMSafeSpacesError";
  }
}

export class AuthError extends LLMSafeSpacesError {
  constructor(message: string, status: number = 401) {
    super(message, status, "AUTH_ERROR");
    this.name = "AuthError";
  }
}

export class NotFoundError extends LLMSafeSpacesError {
  constructor(message: string) {
    super(message, 404, "NOT_FOUND");
    this.name = "NotFoundError";
  }
}

export class ConflictError extends LLMSafeSpacesError {
  constructor(message: string) {
    super(message, 409, "CONFLICT");
    this.name = "ConflictError";
  }
}

export class TimeoutError extends LLMSafeSpacesError {
  constructor(message: string = "Request timed out — the prompt may still be processing") {
    super(message, 0, "TIMEOUT");
    this.name = "TimeoutError";
  }
}

export class RateLimitError extends LLMSafeSpacesError {
  constructor(message: string = "Rate limit exceeded") {
    super(message, 429, "RATE_LIMIT");
    this.name = "RateLimitError";
  }
}
