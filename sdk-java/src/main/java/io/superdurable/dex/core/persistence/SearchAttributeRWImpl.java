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

import io.superdurable.dex.core.WorkflowDefinitionException;
import io.superdurable.dex.gen.models.SearchAttribute;
import io.superdurable.dex.gen.models.SearchAttributeValueType;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

public class SearchAttributeRWImpl implements SearchAttributesRW {

    private final Map<String, SearchAttributeValueType> keyToTypeMap;
    private final Map<String, Long> int64AttributeMap;
    private final Map<String, Long> upsertToServerInt64AttributeMap;
    private final Map<String, String> stringAttributeMap;
    private final Map<String, String> upsertToServerStringAttributeMap;

    private final Map<String, Double> doubleAttributeMap = new HashMap<>();
    private final Map<String, Double> upsertToServerDoubleAttributeMap = new HashMap<>();

    private final Map<String, Boolean> boolAttributeMap = new HashMap<>();
    private final Map<String, Boolean> upsertToServerBoolAttributeMap = new HashMap<>();

    private final Map<String, List<String>> stringArrayAttributeMap = new HashMap<>();
    private final Map<String, List<String>> upsertToServerStringArrayAttributeMap = new HashMap<>();

    public SearchAttributeRWImpl(final Map<String, SearchAttributeValueType> keyToTypeMap,
                                 final List<SearchAttribute> searchAttributeMap
    ) {
        this.keyToTypeMap = keyToTypeMap;
        int64AttributeMap = new HashMap<>();
        upsertToServerInt64AttributeMap = new HashMap<>();
        stringAttributeMap = new HashMap<>();
        upsertToServerStringAttributeMap = new HashMap<>();

        if (searchAttributeMap != null) {
            searchAttributeMap.forEach((sa) -> {
                final SearchAttributeValueType type = keyToTypeMap.get(sa.getKey());
                switch (type) {
                    case KEYWORD:
                    case DATETIME:
                    case TEXT:
                        stringAttributeMap.put(sa.getKey(), sa.getStringValue());
                        break;
                    case INT:
                        int64AttributeMap.put(sa.getKey(), sa.getIntegerValue());
                        break;
                    case DOUBLE:
                        doubleAttributeMap.put(sa.getKey(), sa.getDoubleValue());
                    case BOOL:
                        boolAttributeMap.put(sa.getKey(), sa.getBoolValue());
                        break;
                    case KEYWORD_ARRAY:
                        stringArrayAttributeMap.put(sa.getKey(), sa.getStringArrayValue());
                        break;
                    default:
                        throw new IllegalStateException(String.format("empty or not supported search attribute value type, %s", type));
                }
            });
        }
    }

    @Override
    public Long getSearchAttributeInt64(final String key) {
        return int64AttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeInt64(final String key, final Long value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.INT) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as int64", key));
        }
        int64AttributeMap.put(key, value);
        upsertToServerInt64AttributeMap.put(key, value);
    }

    @Override
    public Double getSearchAttributeDouble(final String key) {
        return doubleAttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeDouble(final String key, final Double value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.DOUBLE) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as double", key));
        }
        doubleAttributeMap.put(key, value);
        upsertToServerDoubleAttributeMap.put(key, value);
    }

    @Override
    public Boolean getSearchAttributeBoolean(final String key) {
        return boolAttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeBoolean(final String key, final Boolean value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.BOOL) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as bool", key));
        }
        boolAttributeMap.put(key, value);
        upsertToServerBoolAttributeMap.put(key, value);
    }

    @Override
    public String getSearchAttributeKeyword(final String key) {
        return stringAttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeKeyword(final String key, final String value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.KEYWORD) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as keyword", key));
        }
        stringAttributeMap.put(key, value);
        upsertToServerStringAttributeMap.put(key, value);
    }

    @Override
    public String getSearchAttributeText(final String key) {
        return stringAttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeText(final String key, final String value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.TEXT) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as Text", key));
        }
        stringAttributeMap.put(key, value);
        upsertToServerStringAttributeMap.put(key, value);
    }

    @Override
    public String getSearchAttributeDatetime(final String key) {
        return stringAttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeDatetime(final String key, final String value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.DATETIME) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as datetime", key));
        }
        stringAttributeMap.put(key, value);
        upsertToServerStringAttributeMap.put(key, value);
    }

    @Override
    public List<String> getSearchAttributeKeywordArray(final String key) {
        return stringArrayAttributeMap.get(key);
    }

    @Override
    public void setSearchAttributeKeywordArray(final String key, final List<String> value) {
        if (!keyToTypeMap.containsKey(key) || keyToTypeMap.get(key) != SearchAttributeValueType.KEYWORD_ARRAY) {
            throw new WorkflowDefinitionException(String.format(
                    "key %s is not defined as keyword array", key));
        }
        stringArrayAttributeMap.put(key, value);
        upsertToServerStringArrayAttributeMap.put(key, value);
    }

    public Map<String, Long> getUpsertToServerInt64AttributeMap() {
        return upsertToServerInt64AttributeMap;
    }

    public Map<String, String> getUpsertToServerStringAttributeMap() {
        return upsertToServerStringAttributeMap;
    }

    public Map<String, List<String>> getUpsertToServerStringArrayAttributeMap() {
        return upsertToServerStringArrayAttributeMap;
    }

    public Map<String, Boolean> getUpsertToServerBooleanAttributeMap() {
        return upsertToServerBoolAttributeMap;
    }

    public Map<String, Double> getUpsertToServerDoubleAttributeMap() {
        return upsertToServerDoubleAttributeMap;
    }
}
