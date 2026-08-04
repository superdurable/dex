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

package io.superdurable.dex.core.communication;

import io.superdurable.dex.core.ObjectEncoder;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.TypeStore;
import io.superdurable.dex.core.WorkflowDefinitionException;
import io.superdurable.dex.gen.models.ChannelInfo;
import io.superdurable.dex.gen.models.EncodedObject;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

public class CommunicationImpl implements Communication {

    final TypeStore internalChannelTypeStore;
    final TypeStore signalChannelTypeStore;

    final Map<String, ChannelInfo> internalChannelInfos;

    final Map<String, ChannelInfo> signalChannelInfos;
    final Map<String, List<EncodedObject>> toPublish = new HashMap<>();

    final List<StateMovement> stateMovements = new ArrayList<>();

    final boolean allowTriggerStateMovements;
    final ObjectEncoder objectEncoder;

    public CommunicationImpl(
            final Map<String, ChannelInfo> internalChannelInfos,
            final Map<String, ChannelInfo> signalChannelInfos,
            final TypeStore internalChannelTypeStore,
            final TypeStore signalChannelTypeStore,
            final ObjectEncoder objectEncoder,
            final boolean allowTriggerStateMovements) {
        this.internalChannelInfos = internalChannelInfos;
        this.signalChannelInfos = signalChannelInfos;
        this.internalChannelTypeStore = internalChannelTypeStore;
        this.signalChannelTypeStore = signalChannelTypeStore;
        this.objectEncoder = objectEncoder;
        this.allowTriggerStateMovements = allowTriggerStateMovements;
    }

    @Override
    public int getInternalChannelSize(final String channelName) {
        checkInternalChannelNameValid(channelName, null);
        int size = 0;
        if(internalChannelInfos.containsKey(channelName)){
            size += internalChannelInfos.get(channelName).getSize();
        }
        if(toPublish.containsKey(channelName)){
            size += toPublish.get(channelName).size();
        }
        return size;
    }

    @Override
    public int getSignalChannelSize(final String channelName) {
        checkSignalChannelNameValid(channelName, null);
        if(signalChannelInfos.containsKey(channelName)){
            return signalChannelInfos.get(channelName).getSize();
        }
        return 0;
    }

    @Override
    public void publishInternalChannel(final String channelName, final Object value) {
        checkInternalChannelNameValid(channelName, value);
        final List<EncodedObject> publish = toPublish.computeIfAbsent(channelName, s -> new ArrayList<>());
        publish.add(objectEncoder.encode(value));
    }

    private void checkInternalChannelNameValid(final String channelName, final Object value) {
        final Class<?> type = internalChannelTypeStore.getType(channelName);

        if (value != null && !type.isInstance(value)) {
            throw new WorkflowDefinitionException(String.format("InternalChannel value is not of type %s", type.getName()));
        }
    }

    private void checkSignalChannelNameValid(final String channelName, final Object value) {
        final Class<?> type = signalChannelTypeStore.getType(channelName);

        if (value != null && !type.isInstance(value)) {
            throw new WorkflowDefinitionException(String.format("SignalChannel value is not of type %s", type.getName()));
        }
    }

    @Override
    public void triggerStateMovements(final StateMovement... stateMovements) {
        if (!allowTriggerStateMovements) {
            throw new WorkflowDefinitionException("triggerStateMovements is not allowed to be used here");
        }
        this.stateMovements.addAll(Arrays.asList(stateMovements));
    }

    public Map<String, List<EncodedObject>> getToPublishInternalChannels() {
        return toPublish;
    }

    public List<StateMovement> getStateMovements() {
        return this.stateMovements;
    }
}
