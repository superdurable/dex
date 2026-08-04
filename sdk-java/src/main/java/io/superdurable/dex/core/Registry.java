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

package io.superdurable.dex.core;

import io.superdurable.dex.core.communication.InternalChannelDef;
import io.superdurable.dex.core.communication.SignalChannelDef;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.PersistenceOptions;
import io.superdurable.dex.core.persistence.SearchAttributeDef;
import io.superdurable.dex.gen.models.SearchAttributeValueType;

import java.lang.reflect.Method;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.stream.Collectors;

public class Registry {
    private final Map<String, ObjectWorkflow> workflowStore = new HashMap<>();
    // (workflow type, stateId)-> StateDef
    private final Map<String, StateDef> workflowStateStore = new HashMap<>(); // TODO refactor to use Map<String, Map<String, StateDef>> to be more clear

    private final Map<String, StateDef> workflowStartStateStore = new HashMap<>();

    private final Map<String, TypeStore> signalTypeStore = new HashMap<>();
    private final Map<String, TypeStore> internalChannelTypeStore = new HashMap<>();
    private final Map<String, TypeStore> dataAttributeTypeStore = new HashMap<>();

    private final Map<String, Map<String, SearchAttributeValueType>> searchAttributeTypeStore = new HashMap<>();

    private final Map<String, PersistenceOptions> persistenceOptionsMap = new HashMap<>();
    private final Map<String, Map<String, Method>> rpcMethodStore = new HashMap<>();

    private static final String DELIMITER = "_";

    public void addWorkflows(final ObjectWorkflow... wfs) {
        Arrays.stream(wfs).forEach(this::addWorkflow);
    }

    public void addWorkflows(final List<ObjectWorkflow> wfs) {
        wfs.forEach(this::addWorkflow);
    }

    public void addWorkflow(final ObjectWorkflow wf) {
        registerWorkflow(wf);
        registerWorkflowState(wf);
        registerWorkflowSignal(wf);
        registerWorkflowInternalChannel(wf);
        registerWorkflowDataAttributes(wf);
        registerWorkflowSearchAttributes(wf);
        registerPersistenceOptions(wf);
        registerWorkflowRPCs(wf);
    }
    public static String getWorkflowType(final ObjectWorkflow wf) {
        return wf.getWorkflowType();
    }

    private void registerWorkflow(final ObjectWorkflow wf) {
        String workflowType = getWorkflowType(wf);

        if (workflowStore.containsKey(workflowType)) {
            throw new WorkflowDefinitionException(String.format("Workflow type %s already exists", workflowType));
        }
        workflowStore.put(workflowType, wf);
    }
    private void registerWorkflowState(final ObjectWorkflow wf) {
        String workflowType = getWorkflowType(wf);
        int startingStates = 0;
        StateDef startState = null;
        for (StateDef stateDef : wf.getWorkflowStates()) {
            String key = getStateDefKey(workflowType, stateDef.getWorkflowState().getStateId());
            if (workflowStateStore.containsKey(key)) {
                throw new WorkflowDefinitionException(String.format("Workflow state definition %s already exists", key));
            } else {
                workflowStateStore.put(key, stateDef);
                if (stateDef.getCanStartWorkflow()) {
                    startingStates++;
                    startState = stateDef;
                }
            }
        }
        if (startingStates > 1) {
            throw new WorkflowDefinitionException(String.format("Workflow cannot have more than one starting states, found %d", startingStates));
        }
        workflowStartStateStore.put(workflowType, startState);
    }

    private void registerWorkflowRPCs(final ObjectWorkflow wf) {
        String workflowType = getWorkflowType(wf);
        final Method[] methods = wf.getClass().getMethods();
        if (methods.length == 0) {
            rpcMethodStore.put(workflowType, new HashMap<>());
            return;
        }

        Arrays.stream(methods).forEach(method -> {
            final RPC rpcAnno = method.getAnnotation(RPC.class);
            if (rpcAnno == null) {
                // skip methods that are not RPC
                return;
            }
            RpcDefinitions.validateRpcMethod(method);
            Map<String, Method> rpcMap =
                    rpcMethodStore.computeIfAbsent(workflowType, s -> new HashMap<>());
            rpcMap.put(method.getName(), method);
        });
    }

    private void registerWorkflowSignal(final ObjectWorkflow wf) {
        final String workflowType = getWorkflowType(wf);
        final List<SignalChannelDef> channels = getSignalChannels(wf);

        final TypeStore typeStore = signalTypeStore.computeIfAbsent(workflowType,
                s -> TypeStore.defaultBuilder(TypeStore.Type.SIGNAL_CHANNEL));

        if (channels == null || channels.isEmpty()) {
            return;
        }

        for (final SignalChannelDef signalChannelDef : channels) {
            typeStore.addToStore(signalChannelDef);
        }
    }

    private void registerWorkflowInternalChannel(final ObjectWorkflow wf) {
        final String workflowType = getWorkflowType(wf);
        final List<InternalChannelDef> channels = getInternalChannels(wf);

        final TypeStore typeStore = internalChannelTypeStore.computeIfAbsent(workflowType,
                s -> TypeStore.defaultBuilder(TypeStore.Type.INTERNAL_CHANNEL));

        if (channels == null || channels.isEmpty()) {
            return;
        }

        for (final InternalChannelDef internalChannelDef : channels) {
            typeStore.addToStore(internalChannelDef);
        }
    }

    private void registerWorkflowDataAttributes(final ObjectWorkflow wf) {
        final String workflowType = getWorkflowType(wf);
        final List<DataAttributeDef> fields = getDataAttributeFields(wf);

        final TypeStore typeStore = dataAttributeTypeStore.computeIfAbsent(workflowType,
                s -> TypeStore.defaultBuilder(TypeStore.Type.DATA_ATTRIBUTE));

        if (fields == null || fields.isEmpty()) {
            return;
        }

        for (final DataAttributeDef dataAttributeField : fields) {
            typeStore.addToStore(dataAttributeField);
        }
    }

    private void registerPersistenceOptions(final ObjectWorkflow wf) {
        String workflowType = getWorkflowType(wf);
        this.persistenceOptionsMap.put(workflowType, wf.getPersistenceOptions());
    }

    private List<DataAttributeDef> getDataAttributeFields(final ObjectWorkflow wf) {
        return getAttributeFields(wf, DataAttributeDef.class);
    }

    private List<SearchAttributeDef> getSearchAttributeFields(final ObjectWorkflow wf) {
        return getAttributeFields(wf, SearchAttributeDef.class);
    }

    private <T extends PersistenceFieldDef> List<T> getAttributeFields(final ObjectWorkflow wf, final Class<T> attributeDef) {
        // All the search attributes, data attributes, and data attribute prefixes can not have the same `key`
        final Set<String> keySet = wf.getPersistenceSchema().stream().map(PersistenceFieldDef::getKey).collect(Collectors.toSet());
        if (keySet.size() != wf.getPersistenceSchema().size()) {
            throw new WorkflowDefinitionException("cannot have conflict key/prefix definition in persistence schema");
        }

        return wf.getPersistenceSchema().stream()
                .filter(attributeDef::isInstance)
                .map(attributeDef::cast)
                .collect(Collectors.toList());
    }

    private List<InternalChannelDef> getInternalChannels(final ObjectWorkflow wf) {
        return wf.getCommunicationSchema().stream().filter((f) -> f instanceof InternalChannelDef).map(f -> (InternalChannelDef) f).collect(Collectors.toList());
    }

    private List<SignalChannelDef> getSignalChannels(final ObjectWorkflow wf) {
        return wf.getCommunicationSchema().stream().filter((f) -> f instanceof SignalChannelDef).map(f -> (SignalChannelDef) f).collect(Collectors.toList());
    }

    private void registerWorkflowSearchAttributes(final ObjectWorkflow wf) {
        String workflowType = getWorkflowType(wf);
        final List<SearchAttributeDef> fields = getSearchAttributeFields(wf);
        if (fields == null || fields.isEmpty()) {
            searchAttributeTypeStore.put(workflowType, new HashMap<>());
            return;
        }

        for (SearchAttributeDef searchAttributeField : fields) {
            Map<String, SearchAttributeValueType> searchAttributeKeyToTypeMap =
                    searchAttributeTypeStore.computeIfAbsent(workflowType, s -> new HashMap<>());

            if (searchAttributeKeyToTypeMap.containsKey(searchAttributeField.getKey())) {
                throw new WorkflowDefinitionException(
                        String.format(
                                "Search attribute key %s already exists",
                                searchAttributeField.getKey())
                );
            }
            searchAttributeKeyToTypeMap.put(
                    searchAttributeField.getKey(),
                    searchAttributeField.getSearchAttributeType()
            );
        }
    }

    public ObjectWorkflow getWorkflow(final String workflowType) {
        return workflowStore.get(workflowType);
    }

    public Method getWorkflowRpcMethod(final String workflowType, final String rpcName) {
        final Map<String, Method> wfRRPCs = rpcMethodStore.get(workflowType);
        if (wfRRPCs == null) {
            throw new WorkflowDefinitionException(String.format("workflow type %s is not registered, all registered types are: %s", workflowType, workflowStore.keySet()));
        }
        return wfRRPCs.get(rpcName);
    }

    public StateDef getWorkflowState(final String workflowType, final String stateId) {
        return workflowStateStore.get(getStateDefKey(workflowType, stateId));
    }

    public Optional<StateDef> getWorkflowStartingState(final String workflowType) {
        final StateDef state = workflowStartStateStore.get(workflowType);
        return Optional.ofNullable(state);
    }

    public TypeStore getSignalChannelTypeStore(final String workflowType) {
        return signalTypeStore.get(workflowType);
    }

    public TypeStore getInternalChannelTypeStore(final String workflowType) {
        return internalChannelTypeStore.get(workflowType);
    }

    public TypeStore getDataAttributeTypeStore(final String workflowType) {
        return dataAttributeTypeStore.get(workflowType);
    }

    public Map<String, SearchAttributeValueType> getSearchAttributeKeyToTypeMap(final String workflowType) {
        return searchAttributeTypeStore.get(workflowType);
    }

    public PersistenceOptions getPersistenceOptions(final String workflowType) {
        return persistenceOptionsMap.get(workflowType);
    }

    private String getStateDefKey(final String workflowType, final String stateId) {
        return workflowType + DELIMITER + stateId;
    }
}
