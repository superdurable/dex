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

import com.google.protobuf.InvalidProtocolBufferException;
import io.superdurable.gen.ChannelResult;
import io.superdurable.gen.ConditionResults;
import io.superdurable.gen.EncodedObject;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.KV;
import io.superdurable.gen.LoadBlobsRequest;
import io.superdurable.gen.LoadBlobsResponse;
import io.superdurable.gen.StepCompletionOutput;
import io.superdurable.gen.Value;

import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.logging.Level;
import java.util.logging.Logger;

final class ValueHydrator {
    private static final Logger LOGGER = Logger.getLogger(ValueHydrator.class.getName());

    private final FlowServiceGrpc.FlowServiceBlockingStub service;
    private final BlobCache cache;

    ValueHydrator(
            final FlowServiceGrpc.FlowServiceBlockingStub service,
            final BlobCache cache) {
        if (service == null || cache == null) {
            throw new IllegalArgumentException("FlowService and BlobCache are required");
        }
        this.service = service;
        this.cache = cache;
    }

    Value hydrate(final Value value) {
        return hydrateAll(Collections.singletonList(value)).get(0);
    }

    List<StepCompletionOutput> hydrateStepOutputs(
            final List<StepCompletionOutput> outputs) {
        final List<Value> source = new ArrayList<Value>();
        for (StepCompletionOutput output : outputs) {
            if (output.hasCompletedStepOutput()) {
                source.add(output.getCompletedStepOutput());
            }
        }
        final List<Value> hydrated = hydrateAll(source);
        final List<StepCompletionOutput> results =
                new ArrayList<StepCompletionOutput>(outputs.size());
        int index = 0;
        for (StepCompletionOutput output : outputs) {
            if (output.hasCompletedStepOutput()) {
                results.add(output.toBuilder()
                        .setCompletedStepOutput(hydrated.get(index++))
                        .build());
            } else {
                results.add(output);
            }
        }
        return results;
    }

    InvokeWaitForMethodRequest hydrate(final InvokeWaitForMethodRequest request) {
        final List<Value> source = new ArrayList<Value>();
        source.add(request.getStepInput());
        addValues(source, request.getAttributesList());
        final List<Value> hydrated = hydrateAll(source);
        int index = 0;
        final InvokeWaitForMethodRequest.Builder builder = request.toBuilder()
                .setStepInput(hydrated.get(index++))
                .clearAttributes();
        for (KV entry : request.getAttributesList()) {
            builder.addAttributes(entry.toBuilder().setValue(hydrated.get(index++)));
        }
        return builder.build();
    }

    InvokeExecuteMethodRequest hydrate(final InvokeExecuteMethodRequest request) {
        final List<Value> source = new ArrayList<Value>();
        if (request.hasStepInput()) {
            source.add(request.getStepInput());
        }
        addValues(source, request.getAttributesList());
        addValues(source, request.getStepExeLocalsList());
        if (request.hasConditionResults()) {
            for (ChannelResult result : request.getConditionResults().getChannelResultsList()) {
                source.addAll(result.getValuesList());
            }
        }
        final List<Value> hydrated = hydrateAll(source);
        int index = 0;
        final InvokeExecuteMethodRequest.Builder builder = request.toBuilder()
                .clearAttributes()
                .clearStepExeLocals();
        if (request.hasStepInput()) {
            builder.setStepInput(hydrated.get(index++));
        }
        for (KV entry : request.getAttributesList()) {
            builder.addAttributes(entry.toBuilder().setValue(hydrated.get(index++)));
        }
        for (KV entry : request.getStepExeLocalsList()) {
            builder.addStepExeLocals(entry.toBuilder().setValue(hydrated.get(index++)));
        }
        if (request.hasConditionResults()) {
            final ConditionResults.Builder conditions = request.getConditionResults()
                    .toBuilder()
                    .clearChannelResults();
            for (ChannelResult result : request.getConditionResults().getChannelResultsList()) {
                final ChannelResult.Builder channel = result.toBuilder().clearValues();
                for (int valueIndex = 0; valueIndex < result.getValuesCount(); valueIndex++) {
                    channel.addValues(hydrated.get(index++));
                }
                conditions.addChannelResults(channel);
            }
            builder.setConditionResults(conditions);
        }
        return builder.build();
    }

    InvokeWorkerRPCRequest hydrate(final InvokeWorkerRPCRequest request) {
        final List<Value> source = new ArrayList<Value>();
        source.add(request.getInput());
        addValues(source, request.getAttributesList());
        final List<Value> hydrated = hydrateAll(source);
        int index = 0;
        final InvokeWorkerRPCRequest.Builder builder = request.toBuilder()
                .setInput(hydrated.get(index++))
                .clearAttributes();
        for (KV entry : request.getAttributesList()) {
            builder.addAttributes(entry.toBuilder().setValue(hydrated.get(index++)));
        }
        return builder.build();
    }

    private List<Value> hydrateAll(final List<Value> values) {
        final List<Value> hydrated = new ArrayList<Value>(values);
        final Map<BlobKey, PendingBlob> pending = new LinkedHashMap<BlobKey, PendingBlob>();
        for (int index = 0; index < values.size(); index++) {
            final Value value = values.get(index);
            final BlobKey key = blobKey(value);
            if (key == null) {
                validateConcrete(value);
                continue;
            }
            PendingBlob blob = pending.get(key);
            if (blob == null) {
                blob = new PendingBlob(key, value);
                pending.put(key, blob);
            }
            blob.indexes.add(index);
        }
        if (pending.isEmpty()) {
            return hydrated;
        }

        final List<PendingBlob> misses = new ArrayList<PendingBlob>();
        for (PendingBlob blob : pending.values()) {
            final Value cached = readCache(blob);
            if (cached == null) {
                misses.add(blob);
            } else {
                blob.hydrated = cached;
            }
        }
        loadMisses(misses);
        for (PendingBlob blob : pending.values()) {
            for (Integer index : blob.indexes) {
                hydrated.set(index, blob.hydrated);
            }
        }
        return hydrated;
    }

    private void loadMisses(final List<PendingBlob> misses) {
        if (misses.isEmpty()) {
            return;
        }
        final LoadBlobsRequest.Builder request = LoadBlobsRequest.newBuilder();
        for (PendingBlob miss : misses) {
            request.addValues(miss.request);
        }
        final LoadBlobsResponse response = service.loadBlobs(request.build());
        for (PendingBlob miss : misses) {
            final Value concrete = response.getValuesMap().get(miss.key.id);
            if (concrete == null) {
                throw new IllegalStateException("LoadBlobs omitted blob " + miss.key.id);
            }
            validateHydrated(miss.key, concrete);
            miss.hydrated = concrete;
            writeCache(miss, concrete);
        }
    }

    private Value readCache(final PendingBlob blob) {
        try {
            final Optional<byte[]> payload = cache.get(blob.key.id);
            if (!payload.isPresent()) {
                return null;
            }
            return decodeCachePayload(blob.key, payload.get());
        } catch (RuntimeException | InvalidProtocolBufferException
                | CharacterCodingException failure) {
            LOGGER.log(Level.WARNING, "cannot read cached blob " + blob.key.id, failure);
            try {
                cache.delete(blob.key.id);
            } catch (RuntimeException deleteFailure) {
                LOGGER.log(Level.WARNING, "cannot delete cached blob " + blob.key.id, deleteFailure);
            }
            return null;
        }
    }

    private void writeCache(final PendingBlob blob, final Value concrete) {
        try {
            final byte[] payload = blob.key.object
                    ? concrete.getObjValue().toByteArray()
                    : concrete.getStringValue().getBytes(StandardCharsets.UTF_8);
            cache.put(blob.key.id, payload);
        } catch (RuntimeException failure) {
            LOGGER.log(Level.WARNING, "cannot write cached blob " + blob.key.id, failure);
        }
    }

    private static Value decodeCachePayload(final BlobKey key, final byte[] payload)
            throws InvalidProtocolBufferException, CharacterCodingException {
        if (key.object) {
            final Value value = Value.newBuilder()
                    .setObjValue(EncodedObject.parseFrom(payload))
                    .build();
            validateConcrete(value);
            return value;
        }
        final String text = StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(payload))
                .toString();
        return Value.newBuilder().setStringValue(text).build();
    }

    private static BlobKey blobKey(final Value value) {
        if (value == null || value.getKindCase() == Value.KindCase.KIND_NOT_SET) {
            throw new IllegalArgumentException("Value has no concrete kind");
        }
        if (value.getKindCase() == Value.KindCase.INTERNAL_BLOB_ID_FOR_STRING_VALUE) {
            return new BlobKey(requireBlobId(value.getInternalBlobIdForStringValue()), false);
        }
        if (value.getKindCase() == Value.KindCase.INTERNAL_BLOB_ID_FOR_OBJ_VALUE) {
            return new BlobKey(requireBlobId(value.getInternalBlobIdForObjValue()), true);
        }
        return null;
    }

    private static void validateHydrated(final BlobKey key, final Value value) {
        if (key.object && value.getKindCase() != Value.KindCase.OBJ_VALUE) {
            throw new IllegalStateException("object blob hydrated to " + value.getKindCase());
        }
        if (!key.object && value.getKindCase() != Value.KindCase.STRING_VALUE) {
            throw new IllegalStateException("string blob hydrated to " + value.getKindCase());
        }
        validateConcrete(value);
    }

    private static void validateConcrete(final Value value) {
        if (value == null) {
            throw new IllegalArgumentException("Value is required");
        }
        switch (value.getKindCase()) {
            case STRING_VALUE:
            case INT_VALUE:
            case BOOL_VALUE:
                return;
            case DOUBLE_VALUE:
                if (!Double.isFinite(value.getDoubleValue())) {
                    throw new IllegalArgumentException("non-finite numbers are unsupported");
                }
                return;
            case OBJ_VALUE:
                final String encoding = value.getObjValue().getEncoding();
                if (!"json".equals(encoding) && !"rawbytes".equals(encoding)) {
                    throw new IllegalArgumentException("unsupported object encoding " + encoding);
                }
                return;
            case INTERNAL_BLOB_ID_FOR_STRING_VALUE:
            case INTERNAL_BLOB_ID_FOR_OBJ_VALUE:
                throw new IllegalArgumentException("blob-backed Value was not hydrated");
            case NULL_VALUE:
                throw new IllegalArgumentException("attribute deletion marker cannot be hydrated");
            default:
                throw new IllegalArgumentException("Value has no concrete kind");
        }
    }

    private static String requireBlobId(final String blobId) {
        if (blobId == null || blobId.isEmpty()) {
            throw new IllegalArgumentException("blob ID is required");
        }
        return blobId;
    }

    private static void addValues(final List<Value> values, final List<KV> entries) {
        for (KV entry : entries) {
            values.add(entry.getValue());
        }
    }

    private static final class BlobKey {
        private final String id;
        private final boolean object;

        private BlobKey(final String id, final boolean object) {
            this.id = id;
            this.object = object;
        }

        @Override
        public boolean equals(final Object other) {
            if (!(other instanceof BlobKey)) {
                return false;
            }
            final BlobKey key = (BlobKey) other;
            return object == key.object && id.equals(key.id);
        }

        @Override
        public int hashCode() {
            return 31 * id.hashCode() + (object ? 1 : 0);
        }
    }

    private static final class PendingBlob {
        private final BlobKey key;
        private final Value request;
        private final List<Integer> indexes = new ArrayList<Integer>();
        private Value hydrated;

        private PendingBlob(final BlobKey key, final Value request) {
            this.key = key;
            this.request = request;
        }
    }
}
