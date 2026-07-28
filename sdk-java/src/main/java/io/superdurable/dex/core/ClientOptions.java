/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.core;

import org.immutables.value.Value;

import java.util.Map;
import java.util.Optional;

import static java.util.concurrent.TimeUnit.SECONDS;

@Value.Immutable
public abstract class ClientOptions {
    public abstract String getServerUrl();

    public abstract String getWorkerUrl();

    public abstract ObjectEncoder getObjectEncoder();

    public abstract Optional<Integer> getLongPollApiMaxWaitTimeSeconds();

    public abstract Map<String,String> getRequestHeaders();

    @Value.Default
    public ServiceApiRetryConfig getServiceApiRetryConfig() {
        return ImmutableServiceApiRetryConfig.builder()
                .initialIntervalMills(100)
                .maximumIntervalMills(SECONDS.toMillis(1))
                .maximumAttempts(10)
                .build();
    }
    public static final String defaultWorkerUrl = "http://localhost:8802";

    public static final String workerUrlFromDocker = "http://host.docker.internal:8802";
    public static final String defaultServerUrl = "http://localhost:8801";

    public static final ClientOptions localDefault = minimum(defaultWorkerUrl, defaultServerUrl);

    // use this when running with docker-compose of Dex server
    public static final ClientOptions dockerDefault = minimum(workerUrlFromDocker, defaultServerUrl);


    public static ClientOptions minimum(final String workerUrl, final String serverUrl) {
        return ImmutableClientOptions.builder()
                .workerUrl(workerUrl)
                .serverUrl(serverUrl)
                .objectEncoder(new JacksonJsonObjectEncoder())
                .build();
    }

    public static ImmutableClientOptions.Builder builder() {
        return ImmutableClientOptions.builder();
    }
}
