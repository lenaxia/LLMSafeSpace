package com.llmsafespaces.sdk;

/**
 * Exception thrown by LLMSafeSpaces API operations.
 */
public class LLMSafeSpacesException extends Exception {
    private final int status;

    public LLMSafeSpacesException(String message, int status) {
        super(message);
        this.status = status;
    }

    public int getStatus() { return status; }
    public boolean isNotFound() { return status == 404; }
    public boolean isAuth() { return status == 401 || status == 403; }
    public boolean isConflict() { return status == 409; }
}
