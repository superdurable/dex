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
