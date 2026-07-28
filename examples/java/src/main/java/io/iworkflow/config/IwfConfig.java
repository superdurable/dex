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

package io.iworkflow.config;

import io.iworkflow.core.*;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class IwfConfig {
    @Bean
    public Registry registry() {
        return new Registry();
    }

    @Bean
    public WorkerService workerService(final Registry registry) {
        return new WorkerService(registry, WorkerOptions.defaultOptions);
    }

    @Bean
    public UnregisteredClient unregisteredClient(final @Value("${iwf.worker.url}") String workerUrl,
                                                 final @Value("${iwf.server.url}") String serverUrl) {
        return new UnregisteredClient(
                ClientOptions.builder()
                        .workerUrl(workerUrl)
                        .serverUrl(serverUrl)
                        .objectEncoder(new JacksonJsonObjectEncoder())
                        .build()
        );
    }

    @Bean
    public Client client(Registry registry,
                         final @Value("${iwf.worker.url}") String workerUrl,
                         final @Value("${iwf.server.url}") String serverUrl) {
        return new Client(registry,
                ClientOptions.builder()
                        .workerUrl(workerUrl)
                        .serverUrl(serverUrl)
                        .objectEncoder(new JacksonJsonObjectEncoder())
                        .build()
        );
    }
}
