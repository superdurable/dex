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

public class PersistenceImpl implements Persistence {

    private final DataAttributesRW dataAttributesRW;
    private final SearchAttributesRW searchAttributesRW;
    private final StateExecutionLocals stateExecutionLocals;

    public PersistenceImpl(final DataAttributesRW dataAttributesRW, final SearchAttributesRW searchAttributesRW, final StateExecutionLocals stateExecutionLocals) {
        this.dataAttributesRW = dataAttributesRW;
        this.searchAttributesRW = searchAttributesRW;
        this.stateExecutionLocals = stateExecutionLocals;
    }

    @Override
    public <T> T getDataAttribute(final String key, final Class<T> type) {
        return dataAttributesRW.getDataAttribute(key, type);
    }

    @Override
    public void setDataAttribute(final String key, final Object value) {
        dataAttributesRW.setDataAttribute(key, value);
    }

    @Override
    public Long getSearchAttributeInt64(final String key) {
        return searchAttributesRW.getSearchAttributeInt64(key);
    }

    @Override
    public void setSearchAttributeInt64(final String key, final Long value) {
        searchAttributesRW.setSearchAttributeInt64(key, value);
    }

    @Override
    public Double getSearchAttributeDouble(final String key) {
        return searchAttributesRW.getSearchAttributeDouble(key);
    }

    @Override
    public void setSearchAttributeDouble(final String key, final Double value) {
        searchAttributesRW.setSearchAttributeDouble(key, value);
    }

    @Override
    public Boolean getSearchAttributeBoolean(final String key) {
        return searchAttributesRW.getSearchAttributeBoolean(key);
    }

    @Override
    public void setSearchAttributeBoolean(final String key, final Boolean value) {
        searchAttributesRW.setSearchAttributeBoolean(key, value);
    }

    @Override
    public String getSearchAttributeKeyword(final String key) {
        return searchAttributesRW.getSearchAttributeKeyword(key);
    }

    @Override
    public void setSearchAttributeKeyword(final String key, final String value) {
        searchAttributesRW.setSearchAttributeKeyword(key, value);
    }

    @Override
    public String getSearchAttributeText(final String key) {
        return searchAttributesRW.getSearchAttributeText(key);
    }

    @Override
    public void setSearchAttributeText(final String key, final String value) {
        searchAttributesRW.setSearchAttributeText(key, value);
    }

    @Override
    public String getSearchAttributeDatetime(final String key) {
        return searchAttributesRW.getSearchAttributeDatetime(key);
    }

    @Override
    public void setSearchAttributeDatetime(final String key, final String value) {
        searchAttributesRW.setSearchAttributeDatetime(key, value);
    }

    @Override
    public List<String> getSearchAttributeKeywordArray(final String key) {
        return searchAttributesRW.getSearchAttributeKeywordArray(key);
    }

    @Override
    public void setSearchAttributeKeywordArray(final String key, final List<String> value) {
        searchAttributesRW.setSearchAttributeKeywordArray(key, value);
    }

    @Override
    public void setStateExecutionLocal(final String key, final Object value) {
        stateExecutionLocals.setStateExecutionLocal(key, value);
    }

    @Override
    public <T> T getStateExecutionLocal(final String key, final Class<T> type) {
        return stateExecutionLocals.getStateExecutionLocal(key, type);
    }

    @Override
    public void recordEvent(final String key, final Object... eventData) {
        stateExecutionLocals.recordEvent(key, eventData);
    }
}
