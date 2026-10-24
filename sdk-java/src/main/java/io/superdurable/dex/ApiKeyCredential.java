/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.grpc.Context;
import io.grpc.Deadline;
import io.grpc.Status;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.time.Duration;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.CancellationException;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.function.LongSupplier;
import java.util.regex.Pattern;

/**
 * Exchanges a long-lived API key for short-lived Dex bearer tokens.
 *
 * <p>Use this credential when the application holds an API key and Dex accepts access tokens.
 * Use {@link FileCredential} when another process writes an access token to a local file. The
 * first call exchanges the key; concurrent callers share that exchange. A token call after 80% of
 * its lifetime starts one background exchange while callers continue using the valid token. A failed
 * refresh is retried no more often than every five seconds while that token remains valid. An
 * expired token makes callers wait for the shared exchange, up to the configured timeout. Dex
 * rejection discards only the rejected token, and unary service calls retry once with a fresh one.
 *
 * <p>The token endpoint must use HTTPS, except that HTTP is allowed for localhost, 127.0.0.0/8,
 * and ::1. Redirect responses fail without forwarding the API key. A gRPC call deadline or
 * cancellation bounds a waiting token lookup. This class is thread-safe. The application owns and
 * closes it after all Clients and Workers sharing it are closed. A supplied HTTP connection
 * factory remains caller-owned.
 * The credential description contains no secrets. Exchange errors redact the API key,
 * cached and two most recently issued access tokens, the failed response's access token,
 * and JWT-shaped substrings before truncating each endpoint error field to 200 characters.
 * A JWT-shaped substring has three or more base64url segments of at least 10 characters each.
 * The rule also applies to the token URL and can redact other text of that shape, such as a
 * long dotted host name. Raw transport causes are omitted.
 *
 * <pre>{@code
 * ApiKeyCredential credential = ApiKeyCredential.builder(
 *         URI.create("https://auth.example.com/token"), "dex-server-id", apiKey).build();
 * try (ApiKeyCredential ownedCredential = credential;
 *         Client client = new Client(registry, blobCache,
 *         ClientOptions.newBuilder().serverAddress("dex.example.com:8801")
 *                 .credential(ownedCredential).build())) {
 *     client.searchFlows("", 10);
 * }
 * }</pre>
 */
public final class ApiKeyCredential implements RejectedTokenCredential, AutoCloseable {
    private static final long RETRY_NANOS = TimeUnit.SECONDS.toNanos(5);
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Pattern JWT_SHAPED_SUBSTRING = Pattern.compile(
            "(?<![A-Za-z0-9_-])[A-Za-z0-9_-]{10,}(?:\\.[A-Za-z0-9_-]{10,}){2,}+(?![A-Za-z0-9_-])");

    private final URI tokenUrl;
    private final String serverId;
    private final String apiKey;
    private final Duration exchangeTimeout;
    private final HttpConnectionFactory httpConnectionFactory;
    private final LongSupplier clock;
    private Thread exchangeThread;
    private String cachedToken;
    private String lastAccessToken;
    private String previousAccessToken;
    private long refreshAtNanos;
    private long expiresAtNanos;
    private long nextRefreshAttemptNanos;
    private CompletableFuture<String> exchangeInFlight;
    private boolean isClosed;

    private ApiKeyCredential(final Builder builder) {
        this.tokenUrl = builder.tokenUrl;
        this.serverId = builder.serverId;
        this.apiKey = builder.apiKey;
        this.exchangeTimeout = builder.exchangeTimeout;
        this.httpConnectionFactory = builder.httpConnectionFactory;
        this.clock = builder.clock;
    }

    /**
     * Begins configuring an API key exchange without contacting the token endpoint.
     *
     * @param tokenUrl the absolute token endpoint URL, using HTTPS or loopback HTTP
     * @param serverId the exact Dex server ID used to scope the access token
     * @param apiKey the nonempty long-lived API key
     * @return a builder with a ten-second exchange timeout
     * @throws IllegalArgumentException if any input is absent or the URL violates the transport rule
     */
    public static Builder builder(final URI tokenUrl, final String serverId, final String apiKey) {
        validateUrl(tokenUrl);
        if (serverId == null || serverId.isEmpty() || apiKey == null || apiKey.isEmpty()) {
            throw new IllegalArgumentException("serverId and apiKey are required");
        }
        return new Builder(tokenUrl, serverId, apiKey);
    }

    /**
     * Returns a cached access token or waits for one exchange when no valid token exists.
     *
     * <p>The first call and expired-token calls wait no longer than the exchange timeout.
     * Concurrent callers share one exchange. Valid tokens return immediately while a refresh runs.
     *
     * @return a nonempty access token without the Bearer prefix
     * @throws ExchangeException if the endpoint, response, transport, or exchange deadline fails
     * @throws InterruptedException if the waiting caller is interrupted
     * @throws IllegalStateException if the credential has been closed
     */
    @Override
    public String token() throws ExchangeException, InterruptedException {
        return tokenForCall(null);
    }

    String tokenForCall(final Context context) throws ExchangeException, InterruptedException {
        final CompletableFuture<String> pending;
        synchronized (this) {
            if (isClosed) {
                throw new IllegalStateException("API key credential is closed");
            }
            final long now = clock.getAsLong();
            if (cachedToken != null && now < expiresAtNanos) {
                if (now >= refreshAtNanos && now >= nextRefreshAttemptNanos
                        && exchangeInFlight == null) {
                    startExchange();
                }
                return cachedToken;
            }
            if (exchangeInFlight == null) {
                startExchange();
            }
            pending = exchangeInFlight;
        }
        final Deadline exchangeDeadline = Deadline.after(exchangeTimeout.toNanos(), TimeUnit.NANOSECONDS);
        while (true) {
            if (context != null && context.isCancelled()) {
                final Deadline callDeadline = context.getDeadline();
                throw (callDeadline != null && callDeadline.isExpired()
                        ? Status.DEADLINE_EXCEEDED : Status.CANCELLED).asRuntimeException();
            }
            final long remainingExchangeNanos = exchangeDeadline.timeRemaining(TimeUnit.NANOSECONDS);
            if (remainingExchangeNanos <= 0) {
                throw exchangeError(-1, "exchange timed out");
            }
            long waitNanos = remainingExchangeNanos;
            if (context != null) {
                final Deadline callDeadline = context.getDeadline();
                if (callDeadline != null) {
                    final long remainingCallNanos = callDeadline.timeRemaining(TimeUnit.NANOSECONDS);
                    if (remainingCallNanos <= 0) {
                        throw Status.DEADLINE_EXCEEDED.asRuntimeException();
                    }
                    waitNanos = Math.min(waitNanos, remainingCallNanos);
                }
                waitNanos = Math.min(waitNanos, TimeUnit.MILLISECONDS.toNanos(10));
            }
            try {
                return pending.get(waitNanos, TimeUnit.NANOSECONDS);
            } catch (TimeoutException timeout) {
                continue;
            } catch (ExecutionException failure) {
                final Throwable cause = failure.getCause();
                if (cause instanceof ExchangeException) {
                    throw (ExchangeException) cause;
                }
                throw exchangeError(-1, "exchange failed");
            } catch (CancellationException cancelled) {
                throw exchangeError(-1, "exchange cancelled");
            }
        }
    }

    /**
     * Discards a rejected access token only when it is still cached.
     *
     * <p>The SDK invokes this after Dex rejects an authenticated call. An older rejected token
     * leaves a newer cached token intact. This method is safe for concurrent calls.
     *
     * @param token the exact access token rejected by Dex, or null to do nothing
     */
    @Override
    public synchronized void rejectToken(final String token) {
        if (token != null && token.equals(cachedToken)) {
            cachedToken = null;
            nextRefreshAttemptNanos = 0L;
        }
    }

    /**
     * Cancels an exchange and interrupts its daemon thread.
     *
     * <p>The application closes this credential after every Client and Worker sharing it has
     * closed. Closing it earlier causes later token calls to fail. Closing is safe more than once.
     */
    @Override
    public synchronized void close() {
        if (!isClosed) {
            isClosed = true;
            if (exchangeInFlight != null) {
                exchangeInFlight.cancel(true);
            }
            if (exchangeThread != null) {
                exchangeThread.interrupt();
            }
        }
    }

    /**
     * Describes this credential without revealing either secret.
     *
     * @return a fixed redacted description
     */
    @Override
    public String toString() {
        return "ApiKeyCredential(<redacted>)";
    }

    synchronized boolean isExchangeInFlight() {
        return exchangeInFlight != null;
    }

    private synchronized void startExchange() {
        final CompletableFuture<String> pending = new CompletableFuture<String>();
        exchangeInFlight = pending;
        final Thread thread = new Thread(() -> {
            try {
                final long startedAt = clock.getAsLong();
                final ExchangeResult result = exchange();
                synchronized (ApiKeyCredential.this) {
                    if (!isClosed) {
                        final long lifetime = TimeUnit.SECONDS.toNanos(result.expiresIn);
                        final long tokenExpiresAtNanos = saturatingAdd(startedAt, lifetime);
                        if (clock.getAsLong() >= tokenExpiresAtNanos) {
                            throw exchangeError(200, "access token expired before exchange completed");
                        }
                        cachedToken = result.token;
                        previousAccessToken = lastAccessToken;
                        lastAccessToken = result.token;
                        refreshAtNanos = saturatingAdd(startedAt, lifetime - lifetime / 5);
                        expiresAtNanos = tokenExpiresAtNanos;
                        nextRefreshAttemptNanos = 0L;
                    }
                    exchangeInFlight = null;
                    exchangeThread = null;
                }
                pending.complete(result.token);
            } catch (ExchangeException failure) {
                synchronized (ApiKeyCredential.this) {
                    nextRefreshAttemptNanos = saturatingAdd(clock.getAsLong(), RETRY_NANOS);
                    exchangeInFlight = null;
                    exchangeThread = null;
                }
                pending.completeExceptionally(failure);
            }
        }, "dex-api-key-exchange");
        thread.setDaemon(true);
        exchangeThread = thread;
        thread.start();
    }

    private ExchangeResult exchange() throws ExchangeException {
        final Map<String, String> payload = new LinkedHashMap<String, String>();
        payload.put("server_id", serverId);
        payload.put("api_key", apiKey);
        try {
            final HttpURLConnection connection = httpConnectionFactory.openConnection(tokenUrl);
            if (connection == null) {
                throw new IOException("HTTP connection factory returned null");
            }
            final int status;
            final JsonNode body;
            try {
                final int timeoutMillis = (int) Math.min(Integer.MAX_VALUE,
                        Math.max(1L, exchangeTimeout.toMillis()));
                connection.setConnectTimeout(timeoutMillis);
                connection.setReadTimeout(timeoutMillis);
                connection.setInstanceFollowRedirects(false);
                connection.setRequestMethod("POST");
                connection.setRequestProperty("Content-Type", "application/json");
                connection.setRequestProperty("Accept", "application/json");
                connection.setDoOutput(true);
                final byte[] requestBody = JSON.writeValueAsBytes(payload);
                connection.setFixedLengthStreamingMode(requestBody.length);
                try (OutputStream output = connection.getOutputStream()) {
                    output.write(requestBody);
                }
                status = connection.getResponseCode();
                if (status >= 300 && status < 400) {
                    throw exchangeError(status, "redirect response");
                }
                try (InputStream input = status >= 400
                        ? connection.getErrorStream() : connection.getInputStream()) {
                    if (input == null) {
                        throw exchangeError(status, "empty response");
                    }
                    body = JSON.readTree(input);
                } catch (JsonProcessingException malformed) {
                    throw exchangeError(status, "malformed JSON response");
                }
            } finally {
                connection.disconnect();
            }
            if (status != 200) {
                throw exchangeError(status, responseError(body));
            }
            if (body == null || !body.isObject()) {
                throw exchangeError(200, "invalid response object");
            }
            final JsonNode token = body.get("access_token");
            final JsonNode type = body.get("token_type");
            final JsonNode expiry = body.get("expires_in");
            if (token == null || !token.isTextual() || token.textValue().isEmpty()
                    || type == null || !type.isTextual()
                    || !"Bearer".equalsIgnoreCase(type.textValue())
                    || expiry == null || !expiry.isIntegralNumber()
                    || !expiry.canConvertToLong() || expiry.longValue() <= 0) {
                final String responseError = responseError(body);
                throw exchangeError(200, "invalid access_token, token_type, or expires_in"
                        + ("unexpected response".equals(responseError) ? "" : ": " + responseError));
            }
            return new ExchangeResult(token.textValue(), expiry.longValue());
        } catch (IOException transport) {
            if (transport instanceof ExchangeException) {
                throw (ExchangeException) transport;
            }
            throw exchangeError(-1, "transport failure");
        } catch (RuntimeException failure) {
            throw exchangeError(-1, "exchange failed");
        }
    }

    private String responseError(final JsonNode body) {
        if (body == null || !body.isObject()) {
            return "unexpected response";
        }
        final JsonNode token = body.get("access_token");
        final String responseToken = token != null && token.isTextual() ? token.textValue() : null;
        final String error = responseField(body, "error", responseToken);
        final String description = responseField(body, "error_description", responseToken);
        return error.isEmpty() && description.isEmpty()
                ? "unexpected response" : error + (description.isEmpty() ? "" : ": " + description);
    }

    private String responseField(final JsonNode body, final String name, final String responseToken) {
        final JsonNode value = body.get(name);
        if (value == null || !value.isTextual()) {
            return "";
        }
        final String safe = redact(value.textValue(), responseToken);
        return safe.substring(0, Math.min(200, safe.length()));
    }

    private ExchangeException exchangeError(final int status, final String detail) {
        return new ExchangeException(status, "API key exchange at "
                + redact(tokenUrl.toString()) + " failed (HTTP "
                + (status < 0 ? "status unavailable" : status) + "): " + redact(detail));
    }

    private String redact(final String value) {
        return redact(value, null);
    }

    private String redact(final String value, final String responseToken) {
        final String[] secrets;
        synchronized (this) {
            secrets = new String[] {apiKey, cachedToken, lastAccessToken, previousAccessToken, responseToken};
        }
        Arrays.sort(secrets, (first, second) -> Integer.compare(
                second == null ? 0 : second.length(), first == null ? 0 : first.length()));
        String safe = value;
        for (String secret : secrets) {
            if (secret != null && !secret.isEmpty()) {
                safe = safe.replace(secret, "<redacted>");
            }
        }
        return JWT_SHAPED_SUBSTRING.matcher(safe).replaceAll("<redacted>");
    }

    private static long saturatingAdd(final long start, final long duration) {
        return duration > 0 && start > Long.MAX_VALUE - duration
                ? Long.MAX_VALUE : start + duration;
    }

    private static void validateUrl(final URI tokenUrl) {
        if (tokenUrl == null || !tokenUrl.isAbsolute() || tokenUrl.getHost() == null
                || tokenUrl.getRawUserInfo() != null || tokenUrl.getRawFragment() != null) {
            throw new IllegalArgumentException("tokenUrl must be an absolute HTTP URL without user info");
        }
        final String scheme = tokenUrl.getScheme();
        if ("https".equalsIgnoreCase(scheme)) {
            return;
        }
        if (!"http".equalsIgnoreCase(scheme) || !isLoopback(tokenUrl.getHost())) {
            throw new IllegalArgumentException("tokenUrl must use HTTPS except for loopback HTTP");
        }
    }

    private static boolean isLoopback(final String host) {
        final String address = host.startsWith("[") && host.endsWith("]")
                ? host.substring(1, host.length() - 1) : host;
        if ("localhost".equalsIgnoreCase(address) || "::1".equals(address)) {
            return true;
        }
        final String[] octets = address.split("\\.", -1);
        if (octets.length != 4 || !"127".equals(octets[0])) {
            return false;
        }
        for (String octet : octets) {
            if (octet.isEmpty() || octet.length() > 3) {
                return false;
            }
            for (int index = 0; index < octet.length(); index++) {
                if (!Character.isDigit(octet.charAt(index))) {
                    return false;
                }
            }
            if (Integer.parseInt(octet) > 255) {
                return false;
            }
        }
        return true;
    }

    private static final class ExchangeResult {
        private final String token;
        private final long expiresIn;

        private ExchangeResult(final String token, final long expiresIn) {
            this.token = token;
            this.expiresIn = expiresIn;
        }
    }

    /**
     * Reports a token endpoint, response, transport, or timeout failure.
     *
     * <p>The message identifies the token URL and HTTP status when available. It includes at most
     * 200 characters from each response error field after redacting the API key, cached and
     * two most recently issued access tokens, the failed response's access token, and
     * JWT-shaped substrings. A JWT-shaped substring has three or more base64url segments
     * of at least 10 characters each. The rule also applies to the token URL and can redact
     * other text of that shape, such as a long dotted host name. It omits raw responses and
     * transport causes. A status of -1 means the exchange did not receive an HTTP response.
     */
    public static final class ExchangeException extends IOException {
        private final int httpStatus;

        private ExchangeException(final int httpStatus, final String message) {
            super(message);
            this.httpStatus = httpStatus;
        }

        /**
         * Returns the token endpoint's HTTP status.
         *
         * @return the status, or -1 when no response arrived
         */
        public int getHttpStatus() {
            return httpStatus;
        }
    }

    /**
     * Opens the HTTP connection used for an API key exchange.
     *
     * <p>Implement this interface to configure a proxy or supply a connection in tests. The
     * credential sets its timeout, method, headers, and request body, then disconnects it after
     * the exchange. For example, {@code uri -> (HttpURLConnection)
     * uri.toURL().openConnection(proxy)} uses an application-selected proxy.
     */
    @FunctionalInterface
    public interface HttpConnectionFactory {
        /**
         * Opens an unconnected HTTP connection to the validated token URL.
         *
         * @param tokenUrl the HTTPS token URL, or a permitted loopback HTTP URL
         * @return an unconnected HTTP connection owned by the credential for this exchange
         * @throws IOException if a connection cannot be created
         */
        HttpURLConnection openConnection(URI tokenUrl) throws IOException;
    }

    /**
     * Configures the timeout and HTTP transport for an API key credential.
     *
     * <p>Obtain a builder from {@link ApiKeyCredential#builder(URI, String, String)}. The enclosing type shows a
     * complete Client example. Build does not contact the token endpoint. The default timeout is
     * ten seconds, and a supplied HTTP connection factory remains the caller's property.
     */
    public static final class Builder {
        private final URI tokenUrl;
        private final String serverId;
        private final String apiKey;
        private Duration exchangeTimeout = Duration.ofSeconds(10);
        private HttpConnectionFactory httpConnectionFactory =
                tokenUrl -> (HttpURLConnection) tokenUrl.toURL().openConnection();
        private LongSupplier clock = System::nanoTime;

        private Builder(final URI tokenUrl, final String serverId, final String apiKey) {
            this.tokenUrl = tokenUrl;
            this.serverId = serverId;
            this.apiKey = apiKey;
        }

        /**
         * Sets the maximum duration for one exchange, including connection and response.
         *
         * @param value a positive duration; the default is ten seconds
         * @return this builder
         * @throws IllegalArgumentException if the duration is null, zero, negative, or too large
         */
        public Builder exchangeTimeout(final Duration value) {
            if (value == null || value.isZero() || value.isNegative()) {
                throw new IllegalArgumentException("exchangeTimeout must be positive");
            }
            try {
                value.toNanos();
            } catch (ArithmeticException overflow) {
                throw new IllegalArgumentException("exchangeTimeout is too large");
            }
            this.exchangeTimeout = value;
            return this;
        }

        /**
         * Uses a connection factory for token exchanges and proxy configuration.
         *
         * <p>The default opens a connection through Java's standard proxy configuration.
         * See {@link HttpConnectionFactory} for an explicit proxy example.
         *
         * @param value the nonnull factory; the caller retains ownership
         * @return this builder
         * @throws IllegalArgumentException if the factory is null
         */
        public Builder httpConnectionFactory(final HttpConnectionFactory value) {
            if (value == null) {
                throw new IllegalArgumentException("httpConnectionFactory is required");
            }
            this.httpConnectionFactory = value;
            return this;
        }

        Builder clock(final LongSupplier value) {
            this.clock = value;
            return this;
        }

        /**
         * Builds the thread-safe credential without contacting the token endpoint.
         *
         * @return a credential owned by the application and shared with its Clients or Workers
         */
        public ApiKeyCredential build() {
            return new ApiKeyCredential(this);
        }
    }
}
