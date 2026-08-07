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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class SearchFlowsPage {
    private final List<SearchFlowEntry> flows;
    private final String nextPageToken;

    SearchFlowsPage(final List<SearchFlowEntry> flows, final String nextPageToken) {
        this.flows = Collections.unmodifiableList(
                new ArrayList<SearchFlowEntry>(flows));
        this.nextPageToken = nextPageToken;
    }

    public List<SearchFlowEntry> getFlows() {
        return flows;
    }

    public String getNextPageToken() {
        return nextPageToken;
    }
}
