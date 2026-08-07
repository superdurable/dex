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

import java.time.Instant;
import java.util.List;

public interface Context {
    String getFlowId();

    String getRunId();

    Instant getFlowStartedAt();

    String getStepExecutionId();

    String getFromStepExecutionId();

    Instant getFirstAttemptAt();

    int getAttempt();

    boolean hasTimerFired();

    boolean hasTimerFired(int index);

    boolean waitForMethodFailed();

    <T> void setStepExecutionLocal(String key, T value, Class<T> valueType);

    <T> T getStepExecutionLocal(String key, Class<T> valueType);

    <T> void recordEvent(String name, T value, Class<T> valueType);

    <T> T getAttribute(Attribute<T> attribute);

    <T> T getAttribute(AttributeMap<T> attribute, String instance);

    <T> void setAttribute(Attribute<T> attribute, T value);

    <T> void setAttribute(AttributeMap<T> attribute, String instance, T value);

    void deleteAttribute(Attribute<?> attribute);

    void deleteAttribute(AttributeMap<?> attribute, String instance);

    <T> void publish(Channel<T> channel, T value);

    <T> void publish(ChannelMap<T> channel, String instance, T value);

    int channelSize(Channel<?> channel);

    int channelSize(ChannelMap<?> channel, String instance);

    <T> List<T> channelResults(Channel<T> channel);

    <T> List<T> channelResults(ChannelMap<T> channel, String instance);
}
