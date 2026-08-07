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

package io.superdurable.dex.integ;

import org.junit.jupiter.api.extension.AfterAllCallback;
import org.junit.jupiter.api.extension.BeforeAllCallback;
import org.junit.jupiter.api.extension.ExtensionContext;

final class SharedIntegExtension implements BeforeAllCallback, AfterAllCallback {
    private static final Object LOCK = new Object();
    private static IntegEnvironment environment;
    private static int users;

    static IntegEnvironment environment() {
        synchronized (LOCK) {
            if (environment == null) {
                throw new IllegalStateException("integ environment is not started");
            }
            return environment;
        }
    }

    @Override
    public void beforeAll(final ExtensionContext context) throws Exception {
        synchronized (LOCK) {
            if (environment == null) {
                environment = IntegEnvironment.start();
            }
            users++;
        }
    }

    @Override
    public void afterAll(final ExtensionContext context) throws Exception {
        synchronized (LOCK) {
            users--;
            if (users == 0 && environment != null) {
                environment.close();
                environment = null;
            }
        }
    }
}
