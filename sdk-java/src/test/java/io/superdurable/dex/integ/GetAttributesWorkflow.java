/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;

import java.util.Objects;

class GetAttributesWorkflow implements Flow<Void> {
    static final String LABEL_NAME = "get-attributes-label";
    static final String COUNT_NAME = "get-attributes-count";
    static final String QUANTITIES_NAME = "get-attributes-quantities";
    static final String SECRET_NAME = "get-attributes-secret";
    static final String SECRET_NOTES_NAME = "get-attributes-secret-notes";
    static final String SECRET_NOTE_INSTANCE = "note 1";
    static final String LARGE_SECRET_NAME = "get-attributes-large-secret";
    static final String LARGE_VALUE_NAME = "get-attributes-large-value";
    static final String CLOSED_RUN_LARGE_VALUE_NAME = "get-attributes-closed-run-large-value";

    final Attribute<String> label = Attribute.define(LABEL_NAME, String.class);
    final Attribute<Integer> count = Attribute.define(COUNT_NAME, Integer.class);
    final Attribute<GetAttributesNote> note =
            Attribute.define("get-attributes-note", GetAttributesNote.class);
    final Attribute<String> emptyLabel =
            Attribute.define("get-attributes-empty-label", String.class);
    final Attribute<Integer> zeroCount = Attribute.define("get-attributes-zero-count", Integer.class);
    final Attribute<Integer> unsetCount =
            Attribute.define("get-attributes-unset-count", Integer.class);
    final AttributeMap<Integer> quantities =
            AttributeMap.define(QUANTITIES_NAME, Integer.class);
    final Attribute<String> secret =
            Attribute.define(SECRET_NAME, String.class).hardDeleteOnNull();
    final AttributeMap<String> secretNotes =
            AttributeMap.define(SECRET_NOTES_NAME, String.class).hardDeleteOnNull();
    final Attribute<String> largeSecret =
            Attribute.define(LARGE_SECRET_NAME, String.class).hardDeleteOnNull();
    final Attribute<String> largeValue = Attribute.define(LARGE_VALUE_NAME, String.class);
    final Attribute<String> closedRunLargeValue =
            Attribute.define(CLOSED_RUN_LARGE_VALUE_NAME, String.class);

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                label,
                count,
                note,
                emptyLabel,
                zeroCount,
                unsetCount,
                quantities,
                secret,
                secretNotes,
                largeSecret,
                largeValue,
                closedRunLargeValue);
    }

    @RPC
    public void setSecret(final Context context, final String value) {
        secret.set(context, value);
    }

    @RPC
    public void clearSecretWithNull(final Context context) {
        secret.set(context, null);
    }

    @RPC
    public void setSecretNote(final Context context, final String value) {
        secretNotes.set(context, SECRET_NOTE_INSTANCE, value);
    }

    @RPC
    public void clearSecretNoteWithNull(final Context context) {
        secretNotes.set(context, SECRET_NOTE_INSTANCE, null);
    }

    static final class GetAttributesNote {
        public String text;
        public int priority;

        GetAttributesNote() {
        }

        GetAttributesNote(final String text, final int priority) {
            this.text = text;
            this.priority = priority;
        }

        @Override
        public boolean equals(final Object other) {
            if (!(other instanceof GetAttributesNote)) {
                return false;
            }
            final GetAttributesNote note = (GetAttributesNote) other;
            return priority == note.priority && Objects.equals(text, note.text);
        }

        @Override
        public int hashCode() {
            return Objects.hash(text, priority);
        }
    }
}
