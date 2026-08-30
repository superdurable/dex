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

package io.superdurable.dex.products.jobpost;

public class PostingUpdate {
    public int version;
    public String idempotencyKey;
    public JobInfo posting;

    public PostingUpdate() {
    }

    public PostingUpdate(
            final int version,
            final String idempotencyKey,
            final JobInfo posting) {
        this.version = version;
        this.idempotencyKey = idempotencyKey;
        this.posting = posting;
    }
}
