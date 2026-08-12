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

package io.superdurable.dex.patterns.workflow.entitystore;

import java.util.List;

/** Structured metadata stored in PostgreSQL as JSON. */
public class UserProfileMetadata {
    public String source;
    public List<String> tags;

    public UserProfileMetadata() {
    }

    public UserProfileMetadata(final String source, final List<String> tags) {
        this.source = source;
        this.tags = tags;
        validate();
    }

    public void validate() {
        if (source == null || source.isEmpty()) {
            throw new IllegalArgumentException("metadata.source is required");
        }
        if (tags == null) {
            throw new IllegalArgumentException("metadata.tags is required");
        }
    }
}
