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

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.ByteString;
import com.google.protobuf.NullValue;
import io.superdurable.dex.exceptions.ValueMappingException;
import io.superdurable.gen.EncodedObject;
import io.superdurable.gen.IndexConfig;
import io.superdurable.gen.IndexType;
import io.superdurable.gen.Value;

import java.io.IOException;
import java.time.Instant;

final class ValueMapper {
    private static final String JSON = "json";
    private static final String RAW_BYTES = "rawbytes";

    private final ObjectMapper objectMapper;

    ValueMapper(final ObjectMapper objectMapper) {
        this.objectMapper = objectMapper;
    }

    Value encode(final Object value) {
        if (value instanceof String) {
            return Value.newBuilder().setStringValue((String) value).build();
        }
        if (value instanceof Boolean) {
            return Value.newBuilder().setBoolValue((Boolean) value).build();
        }
        if (value instanceof Byte || value instanceof Short
                || value instanceof Integer || value instanceof Long) {
            return Value.newBuilder().setIntValue(((Number) value).longValue()).build();
        }
        if (value instanceof Float || value instanceof Double) {
            final double number = ((Number) value).doubleValue();
            if (!Double.isFinite(number)) {
                throw new ValueMappingException("Cannot encode a non-finite number");
            }
            return Value.newBuilder().setDoubleValue(number).build();
        }
        if (value instanceof byte[]) {
            return Value.newBuilder()
                    .setObjValue(EncodedObject.newBuilder()
                            .setEncoding(RAW_BYTES)
                            .setPayload(ByteString.copyFrom((byte[]) value)))
                    .build();
        }
        try {
            return Value.newBuilder()
                    .setObjValue(EncodedObject.newBuilder()
                            .setEncoding(JSON)
                            .setPayload(ByteString.copyFrom(objectMapper.writeValueAsBytes(value))))
                    .build();
        } catch (JsonProcessingException exception) {
            throw new ValueMappingException(
                    "Cannot encode JSON value of type " + value.getClass().getName(),
                    exception);
        }
    }

    @SuppressWarnings("unchecked")
    <T> T decode(final Value value, final Class<T> valueType) {
        if (value == null || value.getKindCase() == Value.KindCase.KIND_NOT_SET) {
            throw new ValueMappingException("Cannot decode a Value without a concrete kind");
        }
        final Object decoded;
        switch (value.getKindCase()) {
            case STRING_VALUE:
                decoded = decodeString(value.getStringValue(), valueType);
                break;
            case BOOL_VALUE:
                decoded = requireType(value.getBoolValue(), valueType, Boolean.class);
                break;
            case INT_VALUE:
                decoded = decodeInteger(value.getIntValue(), valueType);
                break;
            case DOUBLE_VALUE:
                decoded = decodeDouble(value.getDoubleValue(), valueType);
                break;
            case OBJ_VALUE:
                decoded = decodeObject(value.getObjValue(), valueType);
                break;
            case INTERNAL_BLOB_ID_FOR_STRING_VALUE:
            case INTERNAL_BLOB_ID_FOR_OBJ_VALUE:
                throw new ValueMappingException("Cannot decode an unhydrated blob-backed Value");
            case NULL_VALUE:
                throw new ValueMappingException("Cannot decode an attribute deletion marker");
            default:
                throw new ValueMappingException("Cannot decode an unsupported Value kind");
        }
        return (T) boxed(valueType).cast(decoded);
    }

    Object decodeToObject(final Value value) {
        if (value == null || value.getKindCase() == Value.KindCase.KIND_NOT_SET) {
            throw new ValueMappingException("Cannot decode a Value without a concrete kind");
        }
        switch (value.getKindCase()) {
            case STRING_VALUE:
                return value.getStringValue();
            case BOOL_VALUE:
                return value.getBoolValue();
            case INT_VALUE:
                return value.getIntValue();
            case DOUBLE_VALUE:
                return value.getDoubleValue();
            case OBJ_VALUE:
                return decodeObjectTree(value.getObjValue());
            case INTERNAL_BLOB_ID_FOR_STRING_VALUE:
            case INTERNAL_BLOB_ID_FOR_OBJ_VALUE:
                throw new ValueMappingException("Cannot decode an unhydrated blob-backed Value");
            case NULL_VALUE:
                return null;
            default:
                throw new ValueMappingException("Cannot decode an unsupported Value kind");
        }
    }

    private Object decodeObjectTree(final EncodedObject object) {
        if (RAW_BYTES.equals(object.getEncoding())) {
            return object.getPayload().toByteArray();
        }
        if (!JSON.equals(object.getEncoding())) {
            throw new ValueMappingException(
                    "Unsupported object encoding: " + object.getEncoding());
        }
        try {
            return objectMapper.readValue(object.getPayload().toByteArray(), Object.class);
        } catch (IOException exception) {
            throw new ValueMappingException("Cannot decode JSON value as Object", exception);
        }
    }

    Value deletion() {
        return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
    }

    IndexConfig indexConfig(final AttributeIndex index, final boolean map) {
        if (index == null) {
            return null;
        }
        final IndexConfig.Builder builder = IndexConfig.newBuilder()
                .setEnable(true)
                .setType(indexType(index.getType()));
        if (index.getIndexKey() != null) {
            builder.setIndexKey(index.getIndexKey());
        } else if (map) {
            builder.setIndexKey("");
        }
        return builder.build();
    }

    private Object decodeString(final String value, final Class<?> valueType) {
        if (valueType == String.class) {
            return value;
        }
        if (valueType == Instant.class) {
            return Instant.parse(value);
        }
        throw incompatible("string", valueType);
    }

    private Object decodeInteger(final long value, final Class<?> valueType) {
        final Class<?> boxed = boxed(valueType);
        if (boxed == Long.class) {
            return value;
        }
        if (boxed == Integer.class && value >= Integer.MIN_VALUE && value <= Integer.MAX_VALUE) {
            return (int) value;
        }
        if (boxed == Short.class && value >= Short.MIN_VALUE && value <= Short.MAX_VALUE) {
            return (short) value;
        }
        if (boxed == Byte.class && value >= Byte.MIN_VALUE && value <= Byte.MAX_VALUE) {
            return (byte) value;
        }
        throw incompatible("int64", valueType);
    }

    private Object decodeDouble(final double value, final Class<?> valueType) {
        final Class<?> boxed = boxed(valueType);
        if (boxed == Double.class) {
            return value;
        }
        if (boxed == Float.class && Double.isFinite(value)
                && value >= -Float.MAX_VALUE && value <= Float.MAX_VALUE) {
            return (float) value;
        }
        throw incompatible("double", valueType);
    }

    private Object decodeObject(final EncodedObject object, final Class<?> valueType) {
        if (RAW_BYTES.equals(object.getEncoding())) {
            if (valueType != byte[].class) {
                throw incompatible("raw bytes", valueType);
            }
            return object.getPayload().toByteArray();
        }
        if (!JSON.equals(object.getEncoding())) {
            throw new ValueMappingException(
                    "Unsupported object encoding: " + object.getEncoding());
        }
        try {
            if (valueType == Void.class || valueType == Void.TYPE) {
                objectMapper.readTree(object.getPayload().toByteArray());
                return null;
            }
            return objectMapper.readValue(object.getPayload().toByteArray(), valueType);
        } catch (IOException exception) {
            throw new ValueMappingException(
                    "Cannot decode JSON value as " + valueType.getName(),
                    exception);
        }
    }

    private static Object requireType(
            final Object value,
            final Class<?> valueType,
            final Class<?> expected) {
        if (boxed(valueType) != expected) {
            throw incompatible(expected.getSimpleName(), valueType);
        }
        return value;
    }

    private static Class<?> boxed(final Class<?> type) {
        if (!type.isPrimitive()) {
            return type;
        }
        if (type == Boolean.TYPE) {
            return Boolean.class;
        }
        if (type == Byte.TYPE) {
            return Byte.class;
        }
        if (type == Short.TYPE) {
            return Short.class;
        }
        if (type == Integer.TYPE) {
            return Integer.class;
        }
        if (type == Long.TYPE) {
            return Long.class;
        }
        if (type == Float.TYPE) {
            return Float.class;
        }
        if (type == Double.TYPE) {
            return Double.class;
        }
        if (type == Void.TYPE) {
            return Void.class;
        }
        return type;
    }

    private static ValueMappingException incompatible(
            final String kind,
            final Class<?> valueType) {
        return new ValueMappingException(
                "Cannot decode " + kind + " as " + valueType.getName());
    }

    private static IndexType indexType(final AttributeIndex.Type type) {
        switch (type) {
            case KEYWORD:
                return IndexType.INDEX_TYPE_KEYWORD;
            case FULL_TEXT:
                return IndexType.INDEX_TYPE_TEXT;
            case KEYWORD_ARRAY:
                return IndexType.INDEX_TYPE_KEYWORD_ARRAY;
            case INT:
                return IndexType.INDEX_TYPE_INT;
            case DOUBLE:
                return IndexType.INDEX_TYPE_DOUBLE;
            case BOOL:
                return IndexType.INDEX_TYPE_BOOL;
            case DATETIME:
                return IndexType.INDEX_TYPE_DATETIME;
            default:
                throw new IllegalStateException("Unsupported Attribute index type: " + type);
        }
    }
}
