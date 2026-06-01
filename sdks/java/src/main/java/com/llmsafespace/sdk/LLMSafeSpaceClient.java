package com.llmsafespace.sdk;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;

/**
 * LLMSafeSpace API client for Java 17+.
 */
public class LLMSafeSpaceClient {
    private final String baseUrl;
    private final String apiKey;
    private final HttpClient httpClient;
    private final Gson gson = new Gson();
    private final Duration timeout;

    /** Regex pattern for valid secret names. Keep in sync with pkg/validation/name.go. */
    public static final String SECRET_NAME_PATTERN = "^[a-z0-9._-]+$";

    private LLMSafeSpaceClient(Builder builder) {
        this.baseUrl = builder.baseUrl.replaceAll("/$", "");
        this.apiKey = builder.apiKey;
        this.timeout = builder.timeout;
        this.httpClient = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(10))
                .build();
    }

    public static Builder builder(String baseUrl) {
        return new Builder(baseUrl);
    }

    public <T> T get(String path, Class<T> type) throws LLMSafeSpaceException {
        return request("GET", path, null, type);
    }

    public <T> T post(String path, Object body, Class<T> type) throws LLMSafeSpaceException {
        return request("POST", path, body, type);
    }

    public void post(String path, Object body) throws LLMSafeSpaceException {
        request("POST", path, body, null);
    }

    public void delete(String path) throws LLMSafeSpaceException {
        request("DELETE", path, null, null);
    }

    private <T> T request(String method, String path, Object body, Class<T> responseType) throws LLMSafeSpaceException {
        String url = baseUrl + "/api/v1" + path;
        HttpRequest.Builder reqBuilder = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .timeout(timeout)
                .header("Content-Type", "application/json");

        if (apiKey != null) {
            reqBuilder.header("Authorization", "Bearer " + apiKey);
        }

        if (body != null) {
            reqBuilder.method(method, HttpRequest.BodyPublishers.ofString(gson.toJson(body)));
        } else {
            reqBuilder.method(method, HttpRequest.BodyPublishers.noBody());
        }

        try {
            HttpResponse<String> resp = httpClient.send(reqBuilder.build(), HttpResponse.BodyHandlers.ofString());

            if (resp.statusCode() >= 400) {
                String msg = "Unknown error";
                try {
                    JsonObject err = gson.fromJson(resp.body(), JsonObject.class);
                    if (err.has("error")) msg = err.get("error").getAsString();
                } catch (Exception ignored) {}
                throw new LLMSafeSpaceException(msg, resp.statusCode());
            }

            if (responseType == null || resp.statusCode() == 204 || resp.statusCode() == 202) {
                return null;
            }

            return gson.fromJson(resp.body(), responseType);
        } catch (IOException | InterruptedException e) {
            throw new LLMSafeSpaceException("Request failed: " + e.getMessage(), 0);
        }
    }

    /** Extract text content from opencode response parts. */
    public static String extractTextContent(JsonObject raw) {
        if (raw == null || !raw.has("parts")) return "";
        JsonArray parts = raw.getAsJsonArray("parts");
        StringBuilder sb = new StringBuilder();
        for (JsonElement el : parts) {
            JsonObject part = el.getAsJsonObject();
            if ("text".equals(part.get("type").getAsString()) && part.has("text")) {
                sb.append(part.get("text").getAsString());
            }
        }
        return sb.toString();
    }

    public static class Builder {
        private final String baseUrl;
        private String apiKey;
        private Duration timeout = Duration.ofSeconds(120);

        private Builder(String baseUrl) {
            this.baseUrl = baseUrl;
        }

        public Builder apiKey(String apiKey) {
            this.apiKey = apiKey;
            return this;
        }

        public Builder timeout(Duration timeout) {
            this.timeout = timeout;
            return this;
        }

        public LLMSafeSpaceClient build() {
            return new LLMSafeSpaceClient(this);
        }
    }
}
