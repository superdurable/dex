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

/**
 * Contains one page of Flow search results and its continuation token.
 *
 * <p>Pass a nonempty continuation token to the next {@link Client#searchFlows} call with the same
 * query and page size. The returned Flow list is immutable.
 */
public final class SearchFlowsPage {
    private final List<SearchFlowEntry> flows;
    private final String nextPageToken;

    SearchFlowsPage(final List<SearchFlowEntry> flows, final String nextPageToken) {
        this.flows = Collections.unmodifiableList(
                new ArrayList<SearchFlowEntry>(flows));
        this.nextPageToken = nextPageToken;
    }

    /**
     * Returns the Flow entries in this page.
     *
     * @return an immutable list of search entries
     */
    public List<SearchFlowEntry> getFlows() {
        return flows;
    }

    /**
     * Returns the token for fetching the next page.
     *
     * @return the next-page token, or an empty string when no next page exists
     */
    public String getNextPageToken() {
        return nextPageToken;
    }
}
