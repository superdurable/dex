/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.util.Objects;

/**
 * Identifies one round of a {@link Join} and the number of branches started in that round.
 *
 * <p>{@link Join#fork} and {@link Join#forkWithRoundKey} create the round and pass the same value
 * to every branch builder and to the join builder. Put it in the input of each branch Step, of the
 * join Step, and of the next round's split Step. Treat it as an opaque token: a branch passes it
 * to {@link Join#endBranch} or {@link Join#reportBranchResult}, the join Step passes it to
 * {@link Join#forAllBranches}, {@link Join#forFirstBranch}, or {@link Join#forBranches}, and the
 * next split Step passes it to {@link Join#forRemainingBranches}. The application never counts
 * branches or invents round keys itself.
 *
 * <p>The round is persisted inside Step inputs as the JSON object
 * {@code {"roundKey": "<key>", "branchCount": <count>}}. These field names are stable in every
 * Dex SDK, so a Flow can mix Steps written against different SDK versions. Unknown JSON fields are
 * ignored on decode, so later SDK versions may add fields.
 *
 * <p>See {@link Join} for a complete split, branch, join, and reset example.
 *
 * @since 1.44.0
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public final class JoinRound {
    private final String roundKey;
    private final int branchCount;

    /**
     * Creates a round token.
     *
     * <p>Applications normally receive rounds from {@link Join#fork} or
     * {@link Join#forkWithRoundKey}. Construct one directly only when an RPC must report for a
     * known round, for example to report a result on behalf of a canceled branch. The Join methods
     * validate the values when they use the round, not here, so decoding stored input never fails
     * because of them.
     *
     * @param roundKey the ChannelMap instance key of the round; Join methods reject a blank key or
     *     a key containing {@code /}
     * @param branchCount the number of branches started in the round; Join methods reject a
     *     negative count
     */
    @JsonCreator
    public JoinRound(
            @JsonProperty("roundKey") final String roundKey,
            @JsonProperty("branchCount") final int branchCount) {
        this.roundKey = roundKey;
        this.branchCount = branchCount;
    }

    /**
     * Returns the round key, which is the ChannelMap instance key that holds this round's branch
     * results.
     *
     * <p>{@link Join#fork} uses the split Step's execution ID, such as {@code RequestQuotes-2}, so
     * Dex Web shows the round under the same name as the split Step in history.
     *
     * @return the round key
     */
    @JsonProperty("roundKey")
    public String getRoundKey() {
        return roundKey;
    }

    /**
     * Returns the number of branches started in this round.
     *
     * <p>This equals the number of branch builders passed to {@link Join#fork} or
     * {@link Join#forkWithRoundKey}. It may be zero.
     *
     * @return the branch count
     */
    @JsonProperty("branchCount")
    public int getBranchCount() {
        return branchCount;
    }

    /**
     * Compares the round key and branch count.
     *
     * @param other the object to compare
     * @return {@code true} when {@code other} is a round with the same key and branch count
     */
    @Override
    public boolean equals(final Object other) {
        if (this == other) {
            return true;
        }
        if (!(other instanceof JoinRound)) {
            return false;
        }
        final JoinRound that = (JoinRound) other;
        return branchCount == that.branchCount && Objects.equals(roundKey, that.roundKey);
    }

    /**
     * Returns a hash code consistent with {@link #equals(Object)}.
     *
     * @return the hash code
     */
    @Override
    public int hashCode() {
        return Objects.hash(roundKey, branchCount);
    }

    /**
     * Returns a diagnostic representation with the round key and branch count.
     *
     * @return the diagnostic string
     */
    @Override
    public String toString() {
        return "JoinRound{roundKey=" + roundKey + ", branchCount=" + branchCount + "}";
    }
}
