package com.llmsafespace.sdk;

/**
 * Exception thrown by LLMSafeSpace API operations.
 */
public class LLMSafeSpaceException extends Exception {
    private final int status;

    public LLMSafeSpaceException(String message, int status) {
        super(message);
        this.status = status;
    }

    public int getStatus() { return status; }
    public boolean isNotFound() { return status == 404; }
    public boolean isAuth() { return status == 401 || status == 403; }
    public boolean isConflict() { return status == 409; }
}
