/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;

/**
 * Batches text chunks into best-effort Stream messages during one Step invocation.
 *
 * <p>Create one writer with {@link #create(Context, Stream)} and call {@link #write(String)} from
 * the handler or pass the method as a text-delta callback. The first nonempty chunk starts a
 * one-shot timer. The Worker automatically stops the timer and flushes remaining text before the
 * handler result or error is sent. Chunks are concatenated without separators and never split.
 */
public final class BufferedTextStream implements StepOutputFinalizer {
    /** Default one-shot flush interval. */
    public static final Duration DEFAULT_FLUSH_INTERVAL = Duration.ofSeconds(1);
    /** Default soft UTF-8 batch threshold. */
    public static final int DEFAULT_MAX_BUFFERED_BYTES = 16 * 1024;

    private final InvocationContext context;
    private final Stream<String> stream;
    private final Duration flushInterval;
    private final int maxBufferedBytes;
    private final StringBuilder buffer = new StringBuilder();
    private int bufferedBytes;
    private long timerGeneration;
    private ScheduledFuture<?> timer;
    private boolean closed;
    private RuntimeException terminalFailure;

    private BufferedTextStream(
            final InvocationContext context,
            final Stream<String> stream,
            final Duration flushInterval,
            final int maxBufferedBytes) {
        this.context = context;
        this.stream = stream;
        this.flushInterval = flushInterval;
        this.maxBufferedBytes = maxBufferedBytes;
    }

    /**
     * Creates a writer with a one-second interval and 16 KiB soft threshold.
     *
     * @param context current Step Context
     * @param stream registered String Stream
     * @return invocation-managed writer
     * @throws IllegalArgumentException if an argument is null or the Stream is not String-valued
     * @throws IllegalStateException if the Context is not an active Step invocation
     */
    public static BufferedTextStream create(
            final Context context,
            final Stream<String> stream) {
        return create(context, stream, DEFAULT_FLUSH_INTERVAL, DEFAULT_MAX_BUFFERED_BYTES);
    }

    /**
     * Creates a writer with explicit buffering settings.
     *
     * @param context current Step Context
     * @param stream registered String Stream
     * @param flushInterval positive one-shot flush interval
     * @param maxBufferedBytes positive soft UTF-8 batch threshold
     * @return invocation-managed writer
     * @throws IllegalArgumentException if an argument or setting is invalid
     * @throws IllegalStateException if the Context is not an active Step invocation
     */
    public static BufferedTextStream create(
            final Context context,
            final Stream<String> stream,
            final Duration flushInterval,
            final int maxBufferedBytes) {
        if (!(context instanceof InvocationContext)) {
            throw new IllegalArgumentException("Buffered Streams require a Dex Step Context");
        }
        if (stream == null || flushInterval == null) {
            throw new IllegalArgumentException("Stream and flushInterval are required");
        }
        if (flushInterval.isZero() || flushInterval.isNegative()) {
            throw new IllegalArgumentException("Buffered Stream flushInterval must be positive");
        }
        if (maxBufferedBytes <= 0) {
            throw new IllegalArgumentException("Buffered Stream maxBufferedBytes must be positive");
        }
        final InvocationContext invocationContext = (InvocationContext) context;
        invocationContext.prepareBufferedStream(stream);
        final BufferedTextStream writer = new BufferedTextStream(
                invocationContext,
                stream,
                flushInterval,
                maxBufferedBytes);
        invocationContext.registerStepOutputFinalizer(writer);
        return writer;
    }

    /**
     * Appends one chunk and flushes after crossing the soft size threshold.
     *
     * @param chunk text appended without modification; an empty value is ignored
     * @throws IllegalArgumentException if chunk is null
     * @throws IllegalStateException if the invocation ended
     * @throws RuntimeException if an earlier background or current output write failed
     */
    public synchronized void write(final String chunk) {
        requireOpen();
        if (chunk == null) {
            throw new IllegalArgumentException("Buffered Stream chunk is required");
        }
        if (chunk.isEmpty()) {
            return;
        }
        final boolean wasEmpty = buffer.length() == 0;
        buffer.append(chunk);
        bufferedBytes += chunk.getBytes(StandardCharsets.UTF_8).length;
        if (wasEmpty) {
            startTimer();
        }
        if (bufferedBytes >= maxBufferedBytes) {
            flush();
        }
    }

    /**
     * Emits the current nonempty batch immediately.
     *
     * @throws IllegalStateException if the invocation ended
     * @throws RuntimeException if an earlier background or current output write failed
     */
    public synchronized void flush() {
        requireOpen();
        stopTimer();
        flushBuffer();
    }

    /** Flushes the tail and closes this writer during invocation finalization. */
    @Override
    public synchronized void finalizeStepOutput() {
        if (closed) {
            if (terminalFailure != null) {
                throw terminalFailure;
            }
            return;
        }
        stopTimer();
        try {
            if (terminalFailure != null) {
                throw terminalFailure;
            }
            flushBuffer();
        } finally {
            closed = true;
        }
    }

    /** Discards buffered text and stops the timer after invocation cancellation. */
    @Override
    public synchronized void cancelStepOutput() {
        stopTimer();
        buffer.setLength(0);
        bufferedBytes = 0;
        closed = true;
    }

    private void startTimer() {
        final long generation = ++timerGeneration;
        timer = context.getBufferedStreamScheduler().schedule(
                () -> flushFromTimer(generation),
                flushInterval.toNanos(),
                TimeUnit.NANOSECONDS);
    }

    private synchronized void flushFromTimer(final long generation) {
        if (closed || terminalFailure != null || generation != timerGeneration) {
            return;
        }
        timer = null;
        try {
            flushBuffer();
        } catch (RuntimeException failure) {
            terminalFailure = failure;
        }
    }

    private void stopTimer() {
        timerGeneration++;
        if (timer != null) {
            timer.cancel(false);
            timer = null;
        }
    }

    private void flushBuffer() {
        if (buffer.length() == 0) {
            return;
        }
        final String value = buffer.toString();
        buffer.setLength(0);
        bufferedBytes = 0;
        try {
            stream.write(context, value);
        } catch (RuntimeException failure) {
            terminalFailure = failure;
            throw failure;
        }
    }

    private void requireOpen() {
        if (terminalFailure != null) {
            throw terminalFailure;
        }
        if (closed) {
            throw new IllegalStateException("Buffered Stream invocation has finished");
        }
    }
}
