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

package io.superdurable.dex.core.persistence;

import java.util.List;

public interface SearchAttributesRW {

    Long getSearchAttributeInt64(String key);

    void setSearchAttributeInt64(String key, Long value);

    Double getSearchAttributeDouble(String key);

    void setSearchAttributeDouble(String key, Double value);

    Boolean getSearchAttributeBoolean(String key);

    void setSearchAttributeBoolean(String key, Boolean value);

    String getSearchAttributeKeyword(String key);

    void setSearchAttributeKeyword(String key, String value);

    String getSearchAttributeText(String key);

    void setSearchAttributeText(String key, String value);

    String getSearchAttributeDatetime(String key);

    String DateTimeFormat = "2006-01-02T15:04:05-07:00";
    /**
     * @param key the search attribute key
     * @param value must be timestamp seconds, or in the {@link #DateTimeFormat}
     */
    void setSearchAttributeDatetime(String key, String value);

    List<String> getSearchAttributeKeywordArray(String key);

    void setSearchAttributeKeywordArray(String key, List<String> value);
}
