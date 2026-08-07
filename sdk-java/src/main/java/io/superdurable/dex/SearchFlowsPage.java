/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
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
