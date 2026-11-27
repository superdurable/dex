/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

import com.sun.source.tree.*;
import com.sun.source.util.JavacTask;
import com.sun.source.util.SourcePositions;
import com.sun.source.util.TreeScanner;
import com.sun.source.util.Trees;
import javax.lang.model.element.Modifier;
import javax.tools.*;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.function.Consumer;

class FlowvizJavaAnalyzer {
    public static void main(String[] arguments) {
        Map<String, String> request = Map.of();
        String result;
        try {
            request = new JsonStrings(new String(System.in.readAllBytes(), StandardCharsets.UTF_8)).readObject();
            result = json(analyzeRequest(request));
        } catch (Throwable failure) {
            var message = failure.getMessage();
            var detail = message == null ? "" : message.lines().findFirst().orElse("");
            var diagnostic = object("severity", "error", "code", "java_analyzer_failed", "message",
                    failure.getClass().getSimpleName() + (detail.isEmpty() ? "" : ": " + detail.substring(0, Math.min(240, detail.length()))));
            result = json(object("graph", object("schemaVersion", "2.0", "valid", false,
                    "source", object("language", "java", "path", request.getOrDefault("path", "")),
                    "flow", object("name", ""), "nodes", List.of(), "edges", List.of(), "diagnostics", List.of(diagnostic))));
        }
        System.out.writeBytes(result.getBytes(StandardCharsets.UTF_8));
    }

    private static Map<String, Object> analyzeRequest(Map<String, String> request) throws Exception {
        var compiler = ToolProvider.getSystemJavaCompiler();
        if (compiler == null || Runtime.version().feature() < 17) {
            throw new IllegalStateException("jdk.compiler is required on JDK 17 or later");
        }
        var source = request.get("source");
        var input = new SimpleJavaFileObject(URI.create("memory:///Workflow.java"), JavaFileObject.Kind.SOURCE) {
            @Override public CharSequence getCharContent(boolean ignoreEncodingErrors) { return source; }
        };
        var messages = new DiagnosticCollector<JavaFileObject>();
        var task = (JavacTask) compiler.getTask(null, null, messages, List.of("-proc:none"), null, List.of(input));
        var unit = task.parse().iterator().next();
        var analyzer = new FlowvizJavaAnalyzer(request.get("path"), source, unit, Trees.instance(task).getSourcePositions());
        for (var message : messages.getDiagnostics()) {
            if (message.getKind() == Diagnostic.Kind.ERROR) {
                analyzer.diagnostic("error", "java_parse_failed", message.getMessage(Locale.ROOT),
                        analyzer.span(message.getStartPosition(), message.getEndPosition()));
            }
        }
        if (analyzer.diagnostics.isEmpty()) analyzer.analyze(request.getOrDefault("flowType", ""));
        return analyzer.result();
    }

    private static final Set<String> TERMINALS = Set.of(
            "gracefulComplete", "forceComplete", "forceFail", "deadEnd", "forceCompleteIfChannelsEmpty");
    private static final Set<String> CANCELLATIONS = Set.of("withCancelingSteps", "withCancelingSiblingSteps");
    private static final Map<String, String> WAIT_TYPES = Map.of(
            "skipImmediately", "skipWaitImmediately", "until", "until", "allOf", "allOf",
            "anyOf", "anyOf", "anyCombinationOf", "anyComboOf");
    private static final Map<String, String> RESOURCE_KINDS = Map.of(
            "Attribute", "attribute", "AttributeMap", "attribute", "Channel", "channel",
            "ChannelMap", "channel", "Stream", "stream", "Join", "channel");
    private static final Set<String> CHANNEL_CONDITIONS = Set.of(
            "forOne", "forN", "atLeast", "atMost", "range",
            "forAllBranches", "forFirstBranch", "forBranches", "forRemainingBranches");

    private final String path;
    private final String source;
    private final CompilationUnitTree unit;
    private final SourcePositions positions;
    private final Map<String, Declaration> classes = new LinkedHashMap<>();
    private final Map<String, Map<String, Object>> nodes = new LinkedHashMap<>();
    private final List<Map<String, Object>> edges = new ArrayList<>();
    private final List<Map<String, Object>> diagnostics = new ArrayList<>();
    private final List<Map<String, Object>> annotations = new ArrayList<>();
    private final List<Map<String, Object>> views = new ArrayList<>();
    private final Map<VariableTree, Resource> resources = new LinkedHashMap<>();
    private final Map<String, Resource> visibleResources = new LinkedHashMap<>();
    private final Map<String, Declaration> registeredSteps = new LinkedHashMap<>();
    private final Set<MethodTree> unresolvedTypeNames = new HashSet<>();
    private final Map<String, Object> flowDefinition = object("name", "");
    private final ExternalCallFinder externalCalls = new ExternalCallFinder();
    private Map<String, Object> startInput;

    private FlowvizJavaAnalyzer(String path, String source, CompilationUnitTree unit, SourcePositions positions) {
        this.path = path;
        this.source = source;
        this.unit = unit;
        this.positions = positions;
    }

    private Map<String, Object> result() {
        var attributes = visibleResources.values().stream().map(Resource::attribute).filter(Objects::nonNull).toList();
        var graph = object("schemaVersion", "2.0", "valid", diagnostics.stream().noneMatch(d -> d.get("severity").equals("error")),
                "source", object("language", "java", "path", path), "flow", flowDefinition,
                "nodes", nodes.values(), "edges", edges, "diagnostics", diagnostics);
        return object("graph", graph, "annotations", annotations, "attributes", attributes, "viewRPCs", views,
                "startInput", startInput, "persistenceResourceIds", visibleResources.keySet(),
                "methodExternalCalls", externalCalls.markers);
    }

    private void analyze(String requestedFlow) {
        for (Tree tree : unit.getTypeDecls()) {
            if (tree instanceof ClassTree declaration) collectClasses(declaration, null);
        }
        var candidates = classes.values().stream().filter(declaration -> declaration.implementsType("Flow")).toList();
        var selected = candidates.stream().filter(candidate -> requestedFlow.isEmpty() ||
                requestedFlow.equals(typeName(candidate, "getFlowType", false))).toList();
        if (selected.size() != 1) {
            reportFlowSelection(requestedFlow, candidates, selected);
            return;
        }
        var declaration = selected.get(0);
        flowDefinition.put("name", typeName(declaration, "getFlowType"));
        flowDefinition.put("span", span(declaration.tree));
        collectResources();
        analyzeRegistration(declaration);
        analyzePersistence(declaration);
        for (var entry : registeredSteps.entrySet()) {
            if (entry.getValue() != null) {
                analyzeStep(entry.getKey(), entry.getValue());
                continue;
            }
            var reason = "Step " + nodes.get(entry.getKey()).get("name") + " declared outside this source file";
            for (var phase : List.of("execute", "wait_for")) externalCalls.addUncheckedMethod(entry.getKey(), phase, reason);
        }
        analyzeTimeout(declaration);
        analyzeRPCs(declaration);
        collectStartInput(declaration);
        externalCalls.scanLockedMethods();
    }

    private void reportFlowSelection(String requested, List<Declaration> candidates, List<Declaration> selected) {
        var names = String.join(", ", candidates.stream().map(candidate -> typeName(candidate, "getFlowType", false)).sorted().toList());
        if (requested.isEmpty() && candidates.isEmpty()) {
            diagnostic("error", "flow_not_found", "source does not declare a class implementing Flow", null);
        } else if (requested.isEmpty()) {
            diagnostic("error", "multiple_flows", "source defines multiple Flows; use --flow with one of: " + names, null);
        } else if (selected.isEmpty()) {
            diagnostic("error", "flow_not_found", "Flow type " + json(requested) +
                    " was not found; available Flow types: " + (names.isEmpty() ? "(none)" : names), null);
        } else {
            diagnostic("error", "multiple_flows", "multiple Flows have type " + json(requested) +
                    "; available Flow types: " + names, null);
        }
    }

    private void collectClasses(ClassTree tree, Declaration enclosing) {
        var declaration = new Declaration(tree, enclosing);
        classes.put(declaration.qualifiedName(), declaration);
        for (Tree member : tree.getMembers()) {
            if (member instanceof ClassTree nested) collectClasses(nested, declaration);
        }
    }

    private void analyzeRegistration(Declaration declaration) {
        var handler = declaration.method("getSteps");
        if (handler == null) return;
        var scope = new MethodScope(declaration, handler);
        Set<Tree> visited = new HashSet<>();
        for (var outcome : scope.outcomes()) {
            collectStepList(outcome.expression, scope, visited);
        }
    }

    private void collectStepList(ExpressionTree expression, MethodScope scope, Set<Tree> visited) {
        expression = scope.initializer(expression);
        if (expression == null || !visited.add(expression)) return;
        if (!(expression instanceof MethodInvocationTree call)) {
            dynamicTarget(expression);
            return;
        }
        var method = callName(call);
        if (staticCall(call, "StepList", "empty")) return;
        if (method.equals("otherSteps")) collectStepList(receiver(call), scope, visited);
        else if (!staticCall(call, "StepList", "startStep") && !staticCall(call, "StepList", "withoutStartStep")) {
            dynamicTarget(expression);
            return;
        }
        for (var argument : call.getArguments()) {
            var type = stepInstanceType(argument, scope, new HashSet<>());
            if (type == null) {
                dynamicTarget(argument);
                continue;
            }
            var step = resolveClass(type, scope.owner);
            var name = step == null ? simpleName(type) : typeName(step, "getStepType");
            var id = "step:" + name;
            if (registeredSteps.containsKey(id)) {
                diagnostic("error", "duplicate_step_type", "Step " + name + " is registered more than once", span(argument));
                continue;
            }
            var isStart = method.equals("startStep");
            registeredSteps.put(id, step);
            node(id, "step", name, argument, "phase", step != null && step.method("waitFor") != null ? "wait_for+execute" : "execute",
                    "start", isStart, "external", step == null);
            if (step == null) unresolvedStep(type, argument);
            if (isStart) flowDefinition.put("startStepId", id);
        }
    }

    private String stepInstanceType(ExpressionTree expression, MethodScope scope, Set<VariableTree> visited) {
        expression = unwrap(expression);
        if (expression instanceof NewClassTree created) return rawType(created.getIdentifier());
        var variable = scope.variable(expression);
        if (variable == null || !visited.add(variable)) return null;
        var fromInitializer = stepInstanceType(variable.getInitializer(), scope, visited);
        if (fromInitializer != null) return fromInitializer;
        var type = rawType(variable.getType());
        if (type.isEmpty() || Set.of("var", "Step").contains(simpleName(type))) return null;
        var declaration = resolveClass(type, scope.owner);
        if (declaration == null) return type;
        return declaration.implementsType("Step") ? declaration.qualifiedName() : null;
    }

    private void analyzeStep(String id, Declaration step) {
        annotations.add(object("stepId", id, "directives", directives(step.tree).stream().map(d -> d.get("text")).toList(),
                "span", span(step.tree)));
        var execute = step.method("execute");
        if (execute != null) {
            var scope = new MethodScope(step, execute);
            analyzeDecisions(id, "execute", scope);
            analyzeAccess(id, "execute", scope, new HashSet<>(), new HashSet<>());
            externalCalls.addStepMethod(id, "execute", scope);
        } else {
            diagnostic("error", "step_handler_not_in_file", "Step " + nodes.get(id).get("name") +
                    " execute must be defined in the source file", span(step.tree));
            externalCalls.addUncheckedMethod(id, "execute", "an execute declared outside this source file");
        }
        var waitFor = step.method("waitFor");
        if (waitFor != null) {
            var scope = new MethodScope(step, waitFor);
            analyzeWait(id, scope);
            externalCalls.addStepMethod(id, "wait_for", scope);
        }
        analyzeOptions(id, step, step.method("getStepOptions"), "execute");
    }

    private void analyzeTimeout(Declaration declaration) {
        var handler = declaration.method("handleTimeout");
        if (handler == null) return;
        var id = "timeout_handler:" + flowDefinition.get("name");
        node(id, "timeout_handler", "handleTimeout", handler);
        var scope = new MethodScope(declaration, handler);
        analyzeDecisions(id, "timeout", scope);
        analyzeAccess(id, "timeout", scope, new HashSet<>(), new HashSet<>());
        for (var member : declaration.tree.getMembers()) {
            if (member instanceof MethodTree method && method != handler) {
                analyzeTimeoutOptions(id, declaration, method);
            }
        }
    }

    private void analyzeDecisions(String owner, String phase, MethodScope scope) {
        var outcomes = scope.outcomes();
        analyzeDecisions(owner, phase, scope, outcomes, new HashSet<>(), "");
        var count = nodes.values().stream().filter(node -> owner.equals(node.get("parentId")) &&
                node.get("kind").equals("decision")).count();
        if (count > 1) {
            node("decision-dispatch:" + owner, "decision_dispatch", "Decision", scope.method, "parentId", owner, "phase", phase);
        }
    }

    private void analyzeDecisions(String owner, String phase, MethodScope scope, List<Outcome> outcomes,
                                  Set<MethodTree> callChain, String callSite) {
        callChain.add(scope.method);
        for (var outcome : outcomes) {
            var expression = scope.initializer(outcome.expression);
            if (!(expression instanceof MethodInvocationTree call)) {
                hiddenDecision(owner, phase, outcome);
                continue;
            }
            var base = call;
            List<MethodInvocationTree> cancellations = new ArrayList<>();
            while (CANCELLATIONS.contains(callName(base)) && receiver(base) instanceof MethodInvocationTree inner) {
                cancellations.add(base);
                base = inner;
            }
            var method = callName(base);
            if (resource(receiver(base), scope) != null && method.equals("endBranch")) method = "deadEnd";
            else if (!staticCall(base, "StepDecision", method) ||
                    !(TERMINALS.contains(method) || Set.of("goTo", "goToMany").contains(method))) {
                var helperScope = scope.helperScope(base);
                if (helperScope != null && !callChain.contains(helperScope.method) && callChain.size() < 8) {
                    var helperOutcomes = helperScope.outcomes(outcome.condition);
                    analyzeDecisions(owner, phase, helperScope, helperOutcomes, callChain,
                            locationID(callSite + ":call", outcome.expression));
                } else hiddenDecision(owner, phase, outcome);
                continue;
            }
            var id = locationID("decision:" + owner + callSite, outcome.expression);
            List<Map<String, Object>> canceled = new ArrayList<>();
            var decision = object("type", method);
            node(id, "decision", method, call, "parentId", owner, "phase", phase,
                    "condition", outcome.condition, "decision", decision);
            if (method.equals("goTo")) {
                var target = stepTarget(argument(base, 0), scope);
                if (target != null) edge("transition", id, target, "goTo", base, "condition", outcome.condition);
            } else if (method.equals("goToMany")) {
                addMovements(id, base.getArguments(), outcome.condition, scope);
            } else if (method.equals("forceCompleteIfChannelsEmpty")) {
                var arguments = base.getArguments();
                var checked = arguments.stream().skip(2).map(argument -> resource(argument, scope))
                        .filter(Objects::nonNull).map(resource -> retain(resource)).distinct().toList();
                decision.put("checkedChannels", checked);
                if (arguments.size() >= 2) addMovements(id, List.of(arguments.get(1)), outcome.condition, scope);
            }
            addCancellations(id, cancellations, outcome.condition, scope, canceled);
            if (!canceled.isEmpty()) decision.put("cancellations", canceled);
        }
        callChain.remove(scope.method);
    }

    private void hiddenDecision(String owner, String phase, Outcome outcome) {
        node(locationID("unknown:decision:" + owner, outcome.expression), "unknown", "Dynamic decision",
                outcome.expression, "parentId", owner, "phase", phase);
        diagnostic("error", "hidden_dex_decision", phase +
                " must directly return a Dex decision such as StepDecision.goTo or StepDecision.gracefulComplete",
                span(outcome.expression));
    }

    private void addCancellations(String owner, List<MethodInvocationTree> calls, String condition,
                                  MethodScope scope, List<Map<String, Object>> details) {
        for (var call : calls) {
            var cancellationScope = callName(call).equals("withCancelingSiblingSteps") ? "siblings" : "all";
            for (var argument : call.getArguments()) {
                var target = stepTarget(argument, scope);
                if (target == null) continue;
                details.add(object("stepId", target, "scope", cancellationScope));
                edge("cancel", owner, target, callName(call), argument, "condition", condition,
                        "metadata", object("scope", cancellationScope));
            }
        }
    }

    private void addMovements(String owner, List<? extends ExpressionTree> arguments, String condition, MethodScope scope) {
        Set<MethodInvocationTree> movements = new LinkedHashSet<>();
        for (var argument : arguments) {
            Set<Tree> visited = Collections.newSetFromMap(new IdentityHashMap<>());
            Set<MethodInvocationTree> argumentMovements = new LinkedHashSet<>();
            collectMovements(argument, scope, visited, argumentMovements);
            if (argumentMovements.isEmpty() && !isEmptyMovements(argument)) dynamicTarget(argument);
            movements.addAll(argumentMovements);
        }
        for (var movement : movements) {
            var target = stepTarget(argument(movement, 0), scope);
            if (target == null) continue;
            edge("transition", owner, target, "fan-out", movement, "condition", condition);
            var options = argument(movement, 2);
            if (options != null) {
                walk(scope.initializer(options), tree -> {
                    if (tree instanceof MethodInvocationTree call) analyzeOptionCall(target, "execute", call, scope);
                });
            }
        }
    }

    private boolean isEmptyMovements(ExpressionTree expression) {
        return unwrap(expression) instanceof MethodInvocationTree call &&
                (staticCall(call, "Collections", "emptyList") || staticCall(call, "List", "of") && call.getArguments().isEmpty());
    }

    private void collectMovements(Tree expression, MethodScope scope, Set<Tree> visited, Set<MethodInvocationTree> movements) {
        if (expression == null || !visited.add(expression)) return;
        walk(expression, tree -> {
            if (tree instanceof MethodInvocationTree call && staticCall(call, "StepMovement", "of")) {
                movements.add(call);
            } else if (tree instanceof IdentifierTree identifier) {
                for (var value : scope.values(identifier)) collectMovements(value, scope, visited, movements);
            }
        });
    }

    private String stepTarget(ExpressionTree expression, MethodScope scope) {
        var type = classLiteral(expression);
        if (type == null) {
            dynamicTarget(expression);
            return null;
        }
        var declaration = resolveClass(type, scope.owner);
        var name = declaration == null ? simpleName(type) : typeName(declaration, "getStepType");
        var id = "step:" + name;
        if (registeredSteps.containsKey(id)) return id;
        if (declaration == null) {
            if (!nodes.containsKey(id)) unresolvedStep(type, expression);
            node(id, "step", name, expression, "external", true);
        } else {
            id = "unknown:step:" + name;
            node(id, "unknown", name, expression);
            diagnostic("error", "unknown_step_target", "Step " + name + " is not registered in this Flow", span(expression));
        }
        return id;
    }

    private void unresolvedStep(String type, Tree expression) {
        diagnostic("warning", "step_type_unresolved", "Step " + type + " is not declared in this source file", span(expression));
    }

    private void dynamicTarget(Tree expression) {
        diagnostic("error", "dynamic_step_target", "Dex target must be a Step instance at registration or a Step class literal in a transition",
                span(expression));
    }

    private void analyzeWait(String owner, MethodScope scope) {
        var outcomes = scope.outcomes();
        var name = nodes.get(owner).get("name");
        if (outcomes.size() > 1) {
            node("wait-dispatch:" + name, "wait_dispatch", "WaitFor", scope.method, "parentId", owner, "phase", "wait_for");
        }
        if (outcomes.isEmpty() && scope.method.getBody() != null) {
            node(locationID("wait:" + name, scope.method), "wait", "failure", scope.method,
                    "parentId", owner, "phase", "wait_for", "wait", object("type", "failure", "conditions", List.of()));
        }
        for (var outcome : outcomes) {
            var expression = scope.initializer(outcome.expression);
            if (!(expression instanceof MethodInvocationTree call) || !staticCall(call, "Wait", callName(call)) ||
                    !WAIT_TYPES.containsKey(callName(call))) {
                node(locationID("unknown:wait:" + name, outcome.expression), "unknown", "Dynamic WaitFor",
                        outcome.expression, "parentId", owner, "phase", "wait_for");
                diagnostic("error", "hidden_dex_wait", "waitFor of Step " + name +
                        " must directly return a Wait factory call", span(outcome.expression));
                continue;
            }
            var id = locationID("wait:" + name, outcome.expression);
            var type = WAIT_TYPES.get(callName(call));
            List<Map<String, Object>> conditions = new ArrayList<>();
            var arguments = expandConditions(call.getArguments(), scope, new HashSet<>());
            for (int index = 0; index < arguments.size(); index++) {
                collectCondition(id, index, arguments.get(index), scope, conditions);
            }
            node(id, "wait", type, call, "parentId", owner, "phase", "wait_for",
                    "condition", outcome.condition, "wait", object("type", type, "conditions", conditions));
        }
        analyzeAccess(owner, "wait_for", scope, new HashSet<>(), new HashSet<>());
    }

    private List<ExpressionTree> expandConditions(List<? extends ExpressionTree> arguments, MethodScope scope, Set<Tree> visited) {
        List<ExpressionTree> expanded = new ArrayList<>();
        for (var argument : arguments) {
            if (!visited.add(argument)) continue;
            var values = scope.values(unwrap(argument));
            if (!values.isEmpty()) {
                expanded.addAll(expandConditions(values, scope, visited));
            } else if (argument instanceof NewArrayTree array) {
                if (array.getInitializers() != null) expanded.addAll(expandConditions(array.getInitializers(), scope, visited));
            } else {
                expanded.add(argument);
            }
        }
        return expanded;
    }

    private void collectCondition(String wait, int index, ExpressionTree expression, MethodScope scope,
                                  List<Map<String, Object>> conditions) {
        var details = object("index", index, "span", span(expression));
        if (unwrap(expression) instanceof MethodInvocationTree call) {
            var method = callName(call);
            if (staticCall(call, "ConditionCombination", "of")) {
                var arguments = expandConditions(call.getArguments(), scope, new HashSet<>());
                for (int position = 0; position < arguments.size(); position++) {
                    collectCondition(wait, position, arguments.get(position), scope, conditions);
                }
                return;
            }
            if (staticCall(call, "Timer", "byDuration") && argument(call, 0) != null) {
                var duration = argument(call, 0).toString();
                details.putAll(object("kind", "timer", "label", duration + " timer", "expression", duration));
            } else if (staticCall(call, "GracefulCompletion", "requested")) {
                details.putAll(object("kind", "gracefulCompletion", "label", "Graceful completion requested"));
            } else if (staticCall(call, "SubFlow", "run") && classLiteral(argument(call, 0)) != null) {
                var target = classLiteral(argument(call, 0));
                var declaration = resolveClass(target, scope.owner);
                var name = declaration == null ? simpleName(target) : typeName(declaration, "getFlowType");
                var id = locationID("subflow:" + wait + ":" + index + ":" + name, call);
                node(id, "subflow", name, call, "external", true);
                edge("subflow", wait, id, "start", call, "metadata", object("conditionIndex", index));
                details.putAll(object("kind", "subflow", "label", name, "subFlowId", id));
            } else {
                var resource = resource(receiver(call), scope);
                if (resource != null && resource.kind.equals("channel") && CHANNEL_CONDITIONS.contains(method)) {
                    var label = channelConditionLabel(resource, call);
                    details.putAll(object("kind", "channel", "label", label, "resourceId", retain(resource),
                            "expression", String.join(", ", call.getArguments().stream().map(Object::toString).toList())));
                    edge("wait_condition", resource.id, wait, label, call, "metadata", object("conditionIndex", index));
                }
            }
        }
        if (!details.containsKey("kind")) {
            details.putAll(object("kind", "unknown", "label", expression.toString()));
            diagnostic("error", "dynamic_wait_condition", "Wait condition must call a Dex condition factory", span(expression));
        }
        conditions.add(details);
    }

    private String channelConditionLabel(Resource resource, MethodInvocationTree call) {
        var arguments = call.getArguments();
        var prefix = resource.key;
        if (resource.isMap && !arguments.isEmpty()) {
            prefix += "[" + arguments.get(0) + "]";
            arguments = arguments.subList(1, arguments.size());
        }
        var count = arguments.isEmpty() ? "N" : arguments.get(0).toString();
        return prefix + "." + switch (callName(call)) {
            case "forOne" -> "for 1";
            case "forN" -> "for " + count;
            case "atLeast" -> "at least " + count;
            case "atMost" -> "at most " + count;
            case "range" -> "for " + count + "…" + (arguments.size() < 2 ? "∞" : arguments.get(1));
            case "forAllBranches" -> "for all branches";
            case "forFirstBranch" -> "for first branch";
            case "forBranches" -> "for " + count + " branches";
            default -> "for remaining branches after " + count;
        };
    }

    private void collectResources() {
        for (var declaration : classes.values()) {
            for (var field : declaration.fields.values()) {
                var definition = resourceDefinition(field.getInitializer());
                if (definition == null) continue;
                var factory = dexOwner(definition);
                var key = stringValue(argument(definition, 0), declaration);
                var type = classLiteral(argument(definition, 1));
                if (key == null || type == null) continue;
                var kind = RESOURCE_KINDS.get(factory);
                var id = "resource:" + kind + ":" + key;
                var isMap = factory.endsWith("Map") || factory.equals("Join");
                var valueType = attributeValueType(type);
                Map<String, Object> attribute = null;
                if (kind.equals("attribute")) {
                    attribute = object("resourceId", id, "key", key, "valueType", valueType, "isMap", isMap,
                            "span", span(field), "directives", directives(field));
                    var index = unwrap(argument(definition, 2));
                    var indexField = declaration.field(index);
                    if (indexField != null) index = unwrap(indexField.getInitializer());
                    if (index instanceof NewClassTree configuration && simpleName(rawType(configuration.getIdentifier())).equals("AttributeIndex")) {
                        var arguments = configuration.getArguments();
                        var indexType = arguments.isEmpty() ? "" : simpleName(arguments.get(0).toString());
                        var indexKey = arguments.size() < 2 ? null : stringValue(arguments.get(1), declaration);
                        attribute.putAll(object("isIndexed", true, "indexKey", indexKey == null ? key : indexKey,
                                "indexType", indexType(indexType)));
                    }
                }
                resources.put(field, new Resource(id, key, kind, isMap, valueType, field, attribute));
            }
        }
    }

    private MethodInvocationTree resourceDefinition(ExpressionTree expression) {
        while (unwrap(expression) instanceof MethodInvocationTree call) {
            if (callName(call).equals("define") && RESOURCE_KINDS.containsKey(dexOwner(call))) return call;
            expression = receiver(call);
        }
        return null;
    }

    private void analyzePersistence(Declaration declaration) {
        var handler = declaration.method("getPersistenceSchema");
        if (handler == null) return;
        analyzePersistence(new MethodScope(declaration, handler), new HashSet<>());
    }

    private void analyzePersistence(MethodScope scope, Set<MethodTree> visited) {
        if (!visited.add(scope.method)) return;
        walk(scope.method.getBody(), tree -> {
            if (!(tree instanceof MethodInvocationTree call)) return;
            if (staticCall(call, "PersistenceSchema", "of")) {
                for (var argument : persistenceArguments(call.getArguments(), scope, new HashSet<>())) {
                    var resource = resource(argument, scope);
                    if (resource != null) retain(resource);
                }
            } else {
                var helper = scope.helperScope(call);
                if (helper != null && rawType(helper.method.getReturnType()).endsWith("PersistenceSchema")) {
                    analyzePersistence(helper, visited);
                }
            }
        });
    }

    private List<ExpressionTree> persistenceArguments(List<? extends ExpressionTree> arguments, MethodScope scope,
                                                      Set<ExpressionTree> visited) {
        List<ExpressionTree> definitions = new ArrayList<>();
        for (var argument : arguments) {
            if (!visited.add(argument)) continue;
            var value = scope.initializer(argument);
            if (value instanceof MethodInvocationTree call &&
                    (staticCall(call, "List", "of") || staticCall(call, "Arrays", "asList"))) {
                definitions.addAll(persistenceArguments(call.getArguments(), scope, visited));
            } else {
                definitions.add(argument);
            }
        }
        return definitions;
    }

    private String retain(Resource resource) {
        visibleResources.putIfAbsent(resource.id, resource);
        node(resource.id, resource.kind, resource.key, resource.declaration, "resource",
                object("valueType", resource.valueType, "map", resource.isMap));
        return resource.id;
    }

    private Resource resource(ExpressionTree expression, MethodScope scope) {
        return resource(expression, scope, new HashSet<>());
    }

    private Resource resource(ExpressionTree expression, MethodScope scope, Set<Tree> visited) {
        expression = unwrap(expression);
        if (expression instanceof MethodInvocationTree call) {
            if (callName(call).equals("instance")) return resource(receiver(call), scope, visited);
            if (staticCall(call, "BufferedTextStream", "create")) return resource(argument(call, 1), scope, visited);
            var helper = scope.helperScope(call);
            if (helper != null && simpleName(rawType(helper.method.getReturnType())).equals("BufferedTextStream") &&
                    visited.add(helper.method)) {
                var outcomes = helper.outcomes();
                if (outcomes.size() == 1) return resource(outcomes.get(0).expression, helper, visited);
            }
        }
        var variable = scope.variable(expression);
        if (variable == null || !visited.add(variable)) return null;
        var direct = resources.get(variable);
        return direct == null ? resource(variable.getInitializer(), scope, visited) : direct;
    }

    private void analyzeAccess(String owner, String phase, MethodScope scope, Set<MethodTree> visited, Set<String> accesses) {
        if (!visited.add(scope.method)) return;
        walk(scope.method.getBody(), tree -> {
            if (!(tree instanceof MethodInvocationTree call)) return;
            if (staticCall(call, "BufferedTextStream", "create")) {
                var stream = resource(argument(call, 1), scope);
                if (stream != null) diagnoseStreamOutsideStep(stream, phase, call);
                return;
            }
            var resource = resource(receiver(call), scope);
            var method = callName(call);
            if (resource == null) {
                var helper = scope.helperScope(call);
                if (helper != null) analyzeAccess(owner, phase, helper, visited, accesses);
                return;
            }
            var kind = switch (method) {
                case "get", "size", "getMapSize", "getAllInstanceKeys", "pendingMessages", "findPendingMessage",
                        "getConditionResults", "getBranchResults", "getRemainingBranchResults" -> "resource_read";
                case "set", "delete", "write" -> "resource_write";
                case "publish", "endBranch", "reportBranchResult" -> "resource_publish";
                default -> null;
            };
            if (resource.kind.equals("stream") && method.equals("write")) diagnoseStreamOutsideStep(resource, phase, call);
            if (kind == null || !accesses.add(kind + ":" + resource.id + ":" + method)) return;
            var read = kind.equals("resource_read");
            var metadata = object("phase", phase);
            if (resource.kind.equals("stream") && method.equals("write")) {
                metadata.putAll(object("bestEffort", true, "repeatable", true, "role", "progress"));
            }
            edge(kind, read ? retain(resource) : owner, read ? owner : retain(resource), method, call, "metadata", metadata);
        });
    }

    private void diagnoseStreamOutsideStep(Resource resource, String phase, MethodInvocationTree call) {
        if (resource.kind.equals("stream") && !phase.equals("execute") && !phase.equals("wait_for")) {
            diagnostic("error", "step_progress_outside_step", "Stream " + resource.key +
                    " uses " + call.getMethodSelect() + " outside a Step", span(call));
        }
    }

    private void analyzeOptions(String owner, Declaration declaration, MethodTree handler, String phase) {
        if (handler == null) return;
        analyzeOptions(owner, phase, new MethodScope(declaration, handler), new HashSet<>());
    }

    private void analyzeOptions(String owner, String phase, MethodScope scope, Set<MethodTree> visited) {
        if (!visited.add(scope.method)) return;
        walk(scope.method.getBody(), tree -> {
            if (!(tree instanceof MethodInvocationTree call)) return;
            analyzeOptionCall(owner, phase, call, scope);
            var helper = scope.helperScope(call);
            if (helper != null && rawType(helper.method.getReturnType()).endsWith("StepOptions"))
                analyzeOptions(owner, phase, helper, visited);
        });
    }

    private void analyzeTimeoutOptions(String owner, Declaration declaration, MethodTree method) {
        var scope = new MethodScope(declaration, method);
        walk(method.getBody(), tree -> {
            if (tree instanceof MethodInvocationTree call && isTimeoutBuilder(receiver(call), scope, new HashSet<>())) {
                analyzeOptionCall(owner, "timeout", call, scope);
            }
        });
    }

    private boolean isTimeoutBuilder(ExpressionTree expression, MethodScope scope, Set<Tree> visited) {
        if (expression == null || !visited.add(expression)) return false;
        expression = scope.initializer(expression);
        return expression instanceof MethodInvocationTree call && (staticCall(call, "FlowTimeoutHandlerOptions", "newBuilder") ||
                isTimeoutBuilder(receiver(call), scope, visited));
    }

    private void analyzeOptionCall(String owner, String phase, MethodInvocationTree call, MethodScope scope) {
        var method = callName(call);
        if (Set.of("onExecuteFailureProceedTo", "onFailureProceedTo").contains(method)) {
            var target = stepTarget(argument(call, 0), scope);
            if (target != null) {
                var targetStep = registeredSteps.get(target);
                edge("failure_transition", owner, target, phase.equals("timeout") ? "Timeout handler failure" : "Execute failure",
                        call, "metadata", object("skipWaitFor", targetStep != null && targetStep.method("waitFor") == null));
            }
        } else if (Set.of("addExecuteLock", "addWaitForLock", "addLock").contains(method) &&
                scope.initializer(argument(call, 0)) instanceof MethodInvocationTree lock &&
                staticCall(lock, "AttributeLock", "of")) {
            var resource = resource(argument(lock, 0), scope);
            if (resource == null || !resource.kind.equals("attribute")) return;
            var metadata = object("phase", method.equals("addWaitForLock") ? "wait_for" : phase);
            if (resource.isMap && argument(lock, 1) != null) {
                // A computed instance may equal any RPC instance, so only a constant is recorded.
                var instance = stringValue(scope.initializer(argument(lock, 1)), scope.owner);
                if (instance != null) metadata.put("instance", instance);
            }
            edge("resource_lock", owner, retain(resource), "Lock", lock, "metadata", metadata);
        }
    }

    private void analyzeRPCs(Declaration declaration) {
        for (var member : declaration.tree.getMembers()) {
            if (!(member instanceof MethodTree method) || !method.getModifiers().getFlags().contains(Modifier.PUBLIC)) continue;
            var annotation = method.getModifiers().getAnnotations().stream()
                    .filter(candidate -> simpleName(candidate.getAnnotationType().toString()).equals("RPC")).findFirst().orElse(null);
            if (annotation == null) continue;
            var name = stringValue(annotationArgument(annotation, "name"), declaration);
            if (name == null || name.isEmpty()) name = method.getName().toString();
            var id = "rpc:" + name;
            node(id, "rpc", name, method);
            var scope = new MethodScope(declaration, method);
            analyzeAccess(id, "rpc", scope, new HashSet<>(), new HashSet<>());
            analyzeRPCLocks(id, name, annotation, scope);
            collectRPCResults(id, name, scope, new HashSet<>());
        }
    }

    private void analyzeRPCLocks(String id, String name, AnnotationTree annotation, MethodScope scope) {
        for (var property : List.of("lockAttributes", "lockAttributeMaps")) {
            var expression = annotationArgument(annotation, property);
            if (expression == null) continue;
            var isMap = property.equals("lockAttributeMaps");
            var elements = expression instanceof NewArrayTree array ? array.getInitializers() : List.of(expression);
            for (var element : elements) {
                var key = stringValue(element instanceof AnnotationTree nested ? annotationArgument(nested, "attribute") : element, scope.owner);
                var resource = visibleResources.get("resource:attribute:" + key);
                if (resource == null || resource.isMap != isMap) {
                    diagnostic("error", "rpc_lock", "RPC " + name + " lock must name an Attribute or AttributeMap declared in this Flow",
                            span(element));
                    continue;
                }
                var metadata = object("phase", "rpc", "rpcName", name);
                if (element instanceof AnnotationTree nested) {
                    metadata.put("instance", stringValue(annotationArgument(nested, "instance"), scope.owner));
                }
                edge("resource_lock", id, retain(resource), "lock", element, "metadata", metadata);
            }
        }
    }

    private void collectRPCResults(String id, String name, MethodScope scope, Set<MethodTree> callChain) {
        collectRPCResults(id, name, scope, callChain, "");
    }

    private void collectRPCResults(String id, String name, MethodScope scope, Set<MethodTree> callChain, String condition) {
        var isView = Set.of("GetDexSummary", "GetDexDisplay").contains(name) && callChain.isEmpty();
        Set<String> outputKeys = new LinkedHashSet<>();
        var hasKnownKeys = true;
        callChain.add(scope.method);
        var outcomes = scope.outcomes(condition);
        for (var outcome : outcomes) {
            var expression = scope.initializer(outcome.expression);
            if (!(expression instanceof MethodInvocationTree call)) {
                hasKnownKeys = false;
                continue;
            }
            var base = call;
            List<MethodInvocationTree> cancellations = new ArrayList<>();
            while (CANCELLATIONS.contains(callName(base)) && receiver(base) instanceof MethodInvocationTree inner) {
                cancellations.add(base);
                base = inner;
            }
            if (!staticCall(base, "RPCResult", "of")) {
                hasKnownKeys = false;
                var helper = scope.helperScope(base);
                if (helper != null && !callChain.contains(helper.method) && callChain.size() < 8) {
                    collectRPCResults(id, name, helper, callChain, outcome.condition);
                } else {
                    diagnostic("error", "hidden_dex_decision", "RPC " + name + " must return RPCResult.of or call a method declared in this file",
                            span(expression));
                }
                continue;
            }
            var keys = mapKeys(scope.initializer(argument(base, 0)), scope.owner);
            if (keys == null) hasKnownKeys = false;
            else outputKeys.addAll(keys);
            var arguments = base.getArguments();
            var decisionID = locationID("decision:" + id, call);
            List<Map<String, Object>> canceled = new ArrayList<>();
            var decision = object("type", "rpcResult");
            node(decisionID, "decision", "rpcResult", call, "parentId", id, "phase", "rpc",
                    "condition", outcome.condition, "decision", decision);
            if (arguments.size() > 1) addMovements(decisionID, arguments.subList(1, arguments.size()), outcome.condition, scope);
            addCancellations(decisionID, cancellations, outcome.condition, scope, canceled);
            if (!canceled.isEmpty()) decision.put("cancellations", canceled);
        }
        callChain.remove(scope.method);
        if (isView) {
            var fact = object("protocolName", name, "span", span(scope.method), "directives", directives(scope.method));
            if (hasKnownKeys && !outcomes.isEmpty()) fact.put("outputKeys", outputKeys);
            var modifies = edges.stream().anyMatch(edge -> edge.get("from").equals(id) &&
                    Set.of("resource_write", "resource_publish", "resource_lock").contains(edge.get("kind")));
            var schedules = edges.stream().anyMatch(edge -> Set.of("transition", "cancel").contains(edge.get("kind")) &&
                    id.equals(nodes.get(edge.get("from")).get("parentId")));
            if (!isViewSignature(scope.method)) {
                fact.put("error", "view RPC " + name + " must accept Context without input and return a string-keyed Map in RPCResult");
            } else if (modifies || schedules) {
                fact.put("error", "view RPC " + name + " must be read-only and must not schedule or cancel Steps");
            }
            views.add(fact);
        }
    }

    private boolean isViewSignature(MethodTree method) {
        var parameters = method.getParameters();
        if (parameters.isEmpty() || parameters.size() > 2 ||
                !simpleName(rawType(parameters.get(0).getType())).equals("Context")) return false;
        if (parameters.size() == 2 && !simpleName(rawType(parameters.get(1).getType())).equals("Void")) return false;
        if (!(method.getReturnType() instanceof ParameterizedTypeTree result) ||
                !simpleName(rawType(result)).equals("RPCResult") || result.getTypeArguments().size() != 1) return false;
        if (!(result.getTypeArguments().get(0) instanceof ParameterizedTypeTree map) ||
                !simpleName(rawType(map)).equals("Map") || map.getTypeArguments().size() != 2) return false;
        return simpleName(rawType(map.getTypeArguments().get(0))).equals("String");
    }

    private List<String> mapKeys(ExpressionTree expression, Declaration owner) {
        if (!(expression instanceof MethodInvocationTree call)) return null;
        var arguments = call.getArguments();
        List<String> keys = new ArrayList<>();
        if (staticCall(call, "Map", "of") || staticCall(call, "Collections", "singletonMap")) {
            for (int index = 0; index < arguments.size(); index += 2) {
                var key = stringValue(arguments.get(index), owner);
                if (key == null) return null;
                keys.add(key);
            }
            return keys;
        }
        if (staticCall(call, "Map", "ofEntries")) {
            for (var argument : arguments) {
                if (!(argument instanceof MethodInvocationTree entry) || !staticCall(entry, "Map", "entry")) return null;
                var key = stringValue(argument(entry, 0), owner);
                if (key == null) return null;
                keys.add(key);
            }
            return keys;
        }
        return staticCall(call, "Collections", "emptyMap") ? keys : null;
    }

    private void collectStartInput(Declaration declaration) {
        var id = flowDefinition.get("startStepId");
        if (id == null) return;
        var step = registeredSteps.get(id);
        var override = step == null ? null : step.method("getInputType");
        if (override == null) override = declaration.method("getInputType");
        var type = override == null ? null : returnedClassLiteral(override);
        if (type == null) type = declaration.interfaceArgument("Flow");
        if (type == null && step != null) type = step.interfaceArgument("Step");
        var anchor = override == null ? declaration.tree : override;
        startInput = object("stepType", nodes.get(id).get("name"), "span", span(anchor));
        try {
            if (type == null) throw new IllegalArgumentException("Start input type is missing");
            var schema = new StartSchema().forType(type, step == null ? declaration : step, 0);
            startInput.put("input", schema);
        } catch (IllegalArgumentException error) {
            startInput.put("error", error.getMessage());
            diagnostic("warning", "start_input_unresolved", error.getMessage(), span(anchor));
        }
    }

    private String returnedClassLiteral(MethodTree method) {
        if (method.getBody() == null || method.getBody().getStatements().size() != 1 ||
                !(method.getBody().getStatements().get(0) instanceof ReturnTree returned)) return null;
        return classLiteral(returned.getExpression());
    }

    private final class StartSchema {
        private final Set<Declaration> ancestors = new HashSet<>();

        Map<String, Object> forType(String type, Declaration owner, int depth) {
            if (depth > 32) throw new IllegalArgumentException("Start input nesting exceeds 32 levels");
            type = type.strip();
            if (type.endsWith("[]")) {
                return object("kind", "array", "nullable", true,
                        "items", forType(type.substring(0, type.length() - 2), owner, depth + 1));
            }
            var opening = type.indexOf('<');
            if (opening != -1) return parameterized(type, opening, owner, depth);
            return switch (simpleName(type)) {
                case "Void", "void" -> object("kind", "null");
                case "String", "Character", "char" -> object("kind", "string");
                case "Boolean", "boolean" -> object("kind", "boolean");
                case "Byte", "byte" -> integerSchema(Byte.MIN_VALUE, Byte.MAX_VALUE);
                case "Short", "short" -> integerSchema(Short.MIN_VALUE, Short.MAX_VALUE);
                case "Integer", "int" -> integerSchema(Integer.MIN_VALUE, Integer.MAX_VALUE);
                case "Long", "long" -> integerSchema(Long.MIN_VALUE, Long.MAX_VALUE);
                case "Float", "float", "Double", "double", "BigDecimal" -> object("kind", "number");
                case "Instant", "OffsetDateTime", "ZonedDateTime", "LocalDateTime", "Date" ->
                        object("kind", "string", "format", "date-time");
                default -> declaredType(type, owner, depth);
            };
        }

        private Map<String, Object> parameterized(String type, int opening, Declaration owner, int depth) {
            var base = simpleName(type.substring(0, opening));
            var arguments = type.substring(opening + 1, type.length() - 1).strip();
            return switch (base) {
                case "Optional" -> {
                    var schema = forType(arguments, owner, depth + 1);
                    schema.put("nullable", true);
                    yield schema;
                }
                case "List", "ArrayList", "Collection" -> object("kind", "array", "nullable", true,
                        "items", forType(arguments, owner, depth + 1));
                case "Map", "HashMap", "LinkedHashMap" -> {
                    var separator = arguments.indexOf(',');
                    if (separator == -1 || !Set.of("String", "java.lang.String").contains(arguments.substring(0, separator).strip())) {
                        throw new IllegalArgumentException("Start input map must use String keys");
                    }
                    yield object("kind", "map", "nullable", true,
                            "values", forType(arguments.substring(separator + 1), owner, depth + 1));
                }
                default -> throw new IllegalArgumentException("Unsupported Start input type " + type);
            };
        }

        private Map<String, Object> declaredType(String type, Declaration owner, int depth) {
            var declaration = resolveClass(type, owner);
            if (declaration == null) throw new IllegalArgumentException("Start input type " + type + " is not declared in this source file");
            if (hasCustomJSON(declaration)) {
                throw new IllegalArgumentException("Start input type " + type + " uses custom JSON serialization");
            }
            if (declaration.tree.getKind() == Tree.Kind.ENUM) {
                List<Map<String, Object>> values = new ArrayList<>();
                for (var field : declaration.fields.values()) {
                    if (field.getInitializer() instanceof NewClassTree created && positions.getEndPosition(unit, created.getIdentifier()) < 0) {
                        var name = field.getName().toString();
                        values.add(object("name", name, "value", name));
                    }
                }
                return object("kind", "string", "enumValues", values);
            }
            if (!Set.of(Tree.Kind.CLASS, Tree.Kind.RECORD).contains(declaration.tree.getKind()) ||
                    declaration.tree.getExtendsClause() != null || !ancestors.add(declaration)) {
                throw new IllegalArgumentException("Unsupported or recursive Start input type " + type);
            }
            List<Map<String, Object>> fields = new ArrayList<>();
            for (var field : declaration.fields.values()) {
                if (field.getModifiers().getFlags().stream().anyMatch(flag -> flag == Modifier.STATIC || flag == Modifier.TRANSIENT)) continue;
                var fieldType = field.getType().toString();
                var schema = forType(fieldType, declaration, depth + 1);
                var name = field.getName().toString();
                for (var annotation : field.getModifiers().getAnnotations()) {
                    if (simpleName(annotation.getAnnotationType().toString()).equals("JsonProperty")) {
                        var value = stringValue(annotationArgument(annotation, "value"), declaration);
                        if (value != null && !value.isEmpty()) name = value;
                    }
                }
                var generic = fieldType.indexOf('<');
                var optional = generic >= 0 && simpleName(fieldType.substring(0, generic)).equals("Optional");
                fields.add(object("name", name, "required", !optional, "schema", schema));
            }
            ancestors.remove(declaration);
            return object("kind", "object", "fields", fields);
        }

        private boolean hasCustomJSON(Declaration declaration) {
            if (hasCustomJSONAnnotations(declaration.tree.getModifiers())) return true;
            for (var member : declaration.tree.getMembers()) {
                if (member instanceof MethodTree method && hasCustomJSONAnnotations(method.getModifiers())) return true;
                if (member instanceof VariableTree field && hasCustomJSONAnnotations(field.getModifiers())) return true;
            }
            return false;
        }

        private boolean hasCustomJSONAnnotations(ModifiersTree modifiers) {
            return modifiers.getAnnotations().stream().anyMatch(annotation -> Set.of(
                    "JsonValue", "JsonCreator", "JsonSerialize", "JsonDeserialize", "JsonUnwrapped", "JsonIgnore", "JsonIgnoreProperties")
                    .contains(simpleName(annotation.getAnnotationType().toString())));
        }
    }

    private static final int MAXIMUM_HELPER_DEPTH = 6;
    private static final String SDK_PACKAGE = "io.superdurable.dex";
    // These SDK types open connections to the Dex server or run Workers.
    private static final Set<String> SDK_CONNECTION_TYPES = Set.of("Client", "Worker", "PullWorker", "WorkerDispatcher",
            "BlobCache", "NativeBlobCache", "NativeBlobCacheBridge", "FlowServiceTlsTransport", "Registry");
    // SDK type names a wildcard SDK import can resolve; other names stay unresolved.
    private static final Set<String> SDK_WILDCARD_TYPES = Set.of("Attribute", "AttributeIndex", "AttributeLock", "AttributeMap",
            "AttributeMapInstance", "BufferedTextStream", "CancellationSteps", "Channel", "ChannelMap", "ChannelMessage", "Condition",
            "ConditionCombination", "Context", "Flow", "FlowTimeoutHandlerOptions", "GracefulCompletion", "Join", "JoinRound",
            "PersistenceSchema", "RPC", "RPCResult", "RetryPolicy", "Step", "StepDecision", "StepList", "StepMovement", "StepOptions",
            "Stream", "SubFlow", "SubFlowOptions", "Timer", "Wait", "Client", "Worker", "PullWorker", "Registry");
    // JDK classes that do no I/O, by top-level class; members of other JDK classes count as external.
    private static final Set<String> PURE_JDK_CLASSES = Set.of(
            "java.lang.String", "java.lang.StringBuilder", "java.lang.StringBuffer", "java.lang.Math", "java.lang.StrictMath",
            "java.lang.Integer", "java.lang.Long", "java.lang.Short", "java.lang.Byte", "java.lang.Double", "java.lang.Float",
            "java.lang.Boolean", "java.lang.Character", "java.lang.Number", "java.lang.Object", "java.lang.Enum",
            "java.lang.Record", "java.lang.CharSequence", "java.lang.Iterable", "java.lang.Comparable", "java.lang.Void",
            "java.lang.Class", "java.lang.System", "java.lang.Runnable",
            "java.util.List", "java.util.ArrayList", "java.util.LinkedList", "java.util.Map", "java.util.HashMap",
            "java.util.LinkedHashMap", "java.util.TreeMap", "java.util.SortedMap", "java.util.NavigableMap", "java.util.EnumMap",
            "java.util.IdentityHashMap", "java.util.Set", "java.util.HashSet", "java.util.LinkedHashSet", "java.util.TreeSet",
            "java.util.SortedSet", "java.util.NavigableSet", "java.util.EnumSet", "java.util.Collection", "java.util.Collections",
            "java.util.Arrays", "java.util.Queue", "java.util.Deque", "java.util.ArrayDeque", "java.util.PriorityQueue",
            "java.util.Iterator", "java.util.ListIterator", "java.util.SequencedCollection", "java.util.SequencedMap",
            "java.util.SequencedSet", "java.util.Optional", "java.util.OptionalInt", "java.util.OptionalLong",
            "java.util.OptionalDouble", "java.util.Objects", "java.util.UUID", "java.util.Base64", "java.util.HexFormat",
            "java.util.StringJoiner", "java.util.Locale", "java.util.Random", "java.util.BitSet", "java.util.Comparator",
            "java.util.AbstractMap", "java.util.IntSummaryStatistics", "java.util.LongSummaryStatistics",
            "java.util.DoubleSummaryStatistics",
            "java.util.stream.Stream", "java.util.stream.IntStream", "java.util.stream.LongStream",
            "java.util.stream.DoubleStream", "java.util.stream.Collectors", "java.util.stream.Collector",
            "java.util.regex.Pattern", "java.util.regex.Matcher", "java.util.regex.MatchResult",
            "java.util.concurrent.ConcurrentHashMap", "java.util.concurrent.ConcurrentMap",
            "java.util.concurrent.ConcurrentLinkedQueue", "java.util.concurrent.ConcurrentLinkedDeque",
            "java.util.concurrent.ConcurrentSkipListMap", "java.util.concurrent.ConcurrentSkipListSet",
            "java.util.concurrent.CopyOnWriteArrayList", "java.util.concurrent.CopyOnWriteArraySet",
            "java.util.concurrent.ThreadLocalRandom", "java.util.concurrent.TimeUnit",
            "java.util.concurrent.atomic.AtomicInteger", "java.util.concurrent.atomic.AtomicLong",
            "java.util.concurrent.atomic.AtomicBoolean", "java.util.concurrent.atomic.AtomicReference",
            "java.util.concurrent.atomic.LongAdder",
            "java.util.logging.Logger", "java.util.logging.Level",
            "java.time.Instant", "java.time.Duration", "java.time.Period", "java.time.LocalDate", "java.time.LocalTime",
            "java.time.LocalDateTime", "java.time.ZonedDateTime", "java.time.OffsetDateTime", "java.time.OffsetTime",
            "java.time.ZoneId", "java.time.ZoneOffset", "java.time.DayOfWeek", "java.time.Month", "java.time.Year",
            "java.time.YearMonth", "java.time.MonthDay", "java.time.Clock",
            "java.time.format.DateTimeFormatter", "java.time.format.FormatStyle",
            "java.time.temporal.ChronoUnit", "java.time.temporal.ChronoField", "java.time.temporal.TemporalAdjusters",
            "java.time.temporal.Temporal", "java.time.temporal.TemporalAmount", "java.time.temporal.TemporalUnit",
            "java.math.BigDecimal", "java.math.BigInteger", "java.math.MathContext", "java.math.RoundingMode",
            "java.text.NumberFormat", "java.text.DecimalFormat", "java.text.Normalizer",
            "java.nio.ByteBuffer", "java.nio.CharBuffer", "java.nio.ByteOrder",
            "java.nio.charset.StandardCharsets", "java.nio.charset.Charset",
            "java.net.URI", "java.net.URLEncoder", "java.net.URLDecoder", "java.security.MessageDigest",
            "org.slf4j.Logger", "org.slf4j.LoggerFactory");
    // Class metadata that never initializes the class; getEnumConstants runs enum constructors.
    private static final Set<String> ALLOWED_CLASS_METHODS = Set.of("getName", "getSimpleName", "getCanonicalName",
            "getTypeName", "getPackageName", "isInstance", "cast", "isEnum", "isRecord", "isInterface", "isArray",
            "isPrimitive", "isAssignableFrom", "getModifiers", "getComponentType", "equals", "hashCode", "toString");
    private static final Set<String> ALLOWED_SYSTEM_METHODS = Set.of("currentTimeMillis", "nanoTime", "arraycopy",
            "identityHashCode", "lineSeparator", "getenv", "getProperty");
    private static final Map<String, Set<String>> DENIED_JDK_METHODS = Map.of(
            "TimeUnit", Set.of("sleep", "timedJoin", "timedWait"));
    // Known SDK return types; T is the receiver's type argument, A the first argument's, C a class literal's.
    private static final Map<String, String> SDK_RETURN_TYPES = Map.ofEntries(
            Map.entry("Context.getAttribute", "A"), Map.entry("Context.channelResults", "List<A>"),
            Map.entry("Context.pendingChannelMessages", "List<ChannelMessage<A>>"),
            Map.entry("Context.getLastHeartbeatValue", "C"), Map.entry("Context.getStepExecutionLocal", "C"),
            Map.entry("Context.getFlowId", "String"), Map.entry("Context.getRunId", "String"),
            Map.entry("Context.getStepExecutionId", "String"), Map.entry("Context.getFromStepExecutionId", "String"),
            Map.entry("Context.getFlowStartedAt", "Instant"), Map.entry("Context.getFirstAttemptAt", "Instant"),
            Map.entry("Context.getAttempt", "int"), Map.entry("Context.channelSize", "int"),
            Map.entry("Context.hasTimerFired", "boolean"), Map.entry("Context.isCancellationRequested", "boolean"),
            Map.entry("Context.hasLastHeartbeatValue", "boolean"), Map.entry("Context.waitForMethodFailed", "boolean"),
            Map.entry("Context.isGracefulCompletionConditionCompleted", "boolean"),
            Map.entry("Context.supportsGracefulCompletionCondition", "boolean"),
            Map.entry("Attribute.get", "T"), Map.entry("Attribute.getName", "String"),
            Map.entry("AttributeMap.get", "T"), Map.entry("AttributeMap.instance", "AttributeMapInstance<T>"),
            Map.entry("AttributeMap.getMapSize", "int"), Map.entry("AttributeMap.getAllInstanceKeys", "List<String>"),
            Map.entry("AttributeMap.getName", "String"), Map.entry("AttributeMapInstance.getInstance", "String"),
            Map.entry("Channel.getConditionResults", "List<T>"), Map.entry("Channel.pendingMessages", "List<ChannelMessage<T>>"),
            Map.entry("Channel.findPendingMessage", "ChannelMessage<T>"), Map.entry("Channel.size", "int"),
            Map.entry("Channel.getName", "String"), Map.entry("Channel.forOne", "Condition"), Map.entry("Channel.forN", "Condition"),
            Map.entry("Channel.atLeast", "Condition"), Map.entry("Channel.atMost", "Condition"), Map.entry("Channel.range", "Condition"),
            Map.entry("ChannelMap.getConditionResults", "List<T>"),
            Map.entry("ChannelMap.pendingMessages", "List<ChannelMessage<T>>"),
            Map.entry("ChannelMap.findPendingMessage", "ChannelMessage<T>"), Map.entry("ChannelMap.size", "int"),
            Map.entry("ChannelMap.getMapSize", "int"), Map.entry("ChannelMap.getAllInstanceKeys", "List<String>"),
            Map.entry("ChannelMap.getName", "String"), Map.entry("ChannelMap.forOne", "Condition"),
            Map.entry("ChannelMap.forN", "Condition"), Map.entry("ChannelMap.atLeast", "Condition"),
            Map.entry("ChannelMap.atMost", "Condition"), Map.entry("ChannelMap.range", "Condition"),
            Map.entry("ChannelMessage.getValue", "T"), Map.entry("ChannelMessage.getMessageId", "String"),
            Map.entry("Stream.getName", "String"), Map.entry("Stream.getStreamName", "String"),
            Map.entry("Join.getBranchResults", "List<T>"), Map.entry("Join.getRemainingBranchResults", "List<T>"),
            Map.entry("Join.endBranch", "StepDecision"), Map.entry("Join.fork", "List<StepMovement>"),
            Map.entry("Join.forkWithRoundKey", "List<StepMovement>"), Map.entry("Join.getChannelMap", "ChannelMap<T>"),
            Map.entry("Join.forAllBranches", "Condition"), Map.entry("Join.forBranches", "Condition"),
            Map.entry("Join.forFirstBranch", "Condition"), Map.entry("Join.forRemainingBranches", "Condition"),
            Map.entry("Join.getName", "String"), Map.entry("JoinRound.getRoundKey", "String"),
            Map.entry("JoinRound.getBranchCount", "int"), Map.entry("BufferedTextStream.create", "BufferedTextStream"),
            Map.entry("StepDecision.*", "StepDecision"), Map.entry("Wait.*", "Wait"), Map.entry("Timer.byDuration", "Condition"),
            Map.entry("GracefulCompletion.requested", "Condition"), Map.entry("SubFlow.run", "Condition"),
            Map.entry("StepMovement.of", "StepMovement"), Map.entry("AttributeLock.of", "AttributeLock"),
            Map.entry("RPCResult.of", "RPCResult"), Map.entry("RPCResult.withCancelingSteps", "RPCResult"),
            Map.entry("ConditionCombination.of", "ConditionCombination"), Map.entry("PersistenceSchema.of", "PersistenceSchema"),
            Map.entry("StepOptions.newBuilder", "StepOptions.Builder"), Map.entry("StepOptions.Builder.*", "StepOptions.Builder"),
            Map.entry("StepOptions.Builder.build", "StepOptions"),
            Map.entry("SubFlowOptions.newBuilder", "SubFlowOptions.Builder"),
            Map.entry("SubFlowOptions.Builder.*", "SubFlowOptions.Builder"), Map.entry("SubFlowOptions.Builder.build", "SubFlowOptions"),
            Map.entry("RetryPolicy.newBuilder", "RetryPolicy.Builder"), Map.entry("RetryPolicy.Builder.*", "RetryPolicy.Builder"),
            Map.entry("RetryPolicy.Builder.build", "RetryPolicy"),
            Map.entry("StartFlowOptions.newBuilder", "StartFlowOptions.Builder"),
            Map.entry("StartFlowOptions.Builder.*", "StartFlowOptions.Builder"),
            Map.entry("StartFlowOptions.Builder.build", "StartFlowOptions"));
    // Reflection reaches any code; wait blocks the thread.
    private static final Set<String> DENIED_METHOD_NAMES = Set.of("forName", "getMethod", "getMethods", "getDeclaredMethod",
            "getDeclaredMethods", "getConstructor", "getConstructors", "getDeclaredConstructor", "getDeclaredConstructors",
            "getField", "getDeclaredField", "invoke", "newInstance", "setAccessible", "wait");
    private static final Set<String> OBJECT_METHODS = Set.of("equals", "hashCode", "toString", "getClass");

    private enum ValueKind { SDK, SDK_CONNECTION, PURE, FUNCTIONAL, SAME_FILE, SAME_PACKAGE_DATA, PRIMITIVE, ARRAY, UNKNOWN }

    /** A record or enum declared in another file of the source's package; only its implicit members are checked. */
    private record SamePackageData(ClassTree tree, Map<String, Tree> components, boolean hasConstructor, Set<String> methods) {}

    /** The analyzer's view of an expression's static type; isTypeName marks a class used for static access. */
    private record ValueType(ValueKind kind, String name, Class<?> jdkClass, Declaration declaration,
                             List<ValueType> arguments, boolean isTypeName) {
        static ValueType of(ValueKind kind, String name) {
            return new ValueType(kind, name, null, null, List.of(), false);
        }

        ValueType withArguments(List<ValueType> values) {
            return new ValueType(kind, name, jdkClass, declaration, values, isTypeName);
        }

        ValueType asTypeName() {
            return new ValueType(kind, name, jdkClass, declaration, arguments, true);
        }

        ValueType argument(int index) {
            return index < arguments.size() ? arguments.get(index) : of(ValueKind.UNKNOWN, "?");
        }
    }

    private record ExternalCallResult(Map<String, Object> call, boolean isExact) {}

    /** Marks a possible external call that may be pure, such as another package's static method or console output. */
    private static final String UNCERTAIN = "uncertain";

    /** Third-party packages of HTTP, cloud, database, and RPC clients; their calls are definite external calls. */
    private static final List<String> KNOWN_INPUT_OUTPUT_PACKAGES = List.of(
            "okhttp3.OkHttpClient", "okhttp3.Call", "org.apache.http.impl.client.", "org.apache.hc.client5.http.impl.", "software.amazon.awssdk.", "com.amazonaws.",
            "com.google.cloud.", "io.grpc.", "redis.clients.", "io.lettuce.", "com.mongodb.", "org.springframework.web.client.",
            "org.springframework.web.reactive.function.client.", "org.apache.kafka.", "com.rabbitmq.", "com.twilio.", "com.stripe.");

    /** Fills methodExternalCalls for Step methods with an allowlist; see locked_method_external_calls.go. */
    private final class ExternalCallFinder {
        final List<Map<String, Object>> markers = new ArrayList<>();
        // exactResults caches helpers whose result did not depend on the depth bound or a cycle.
        private final Map<MethodTree, Optional<Map<String, Object>>> exactResults = new IdentityHashMap<>();
        private final Set<MethodTree> inProgress = Collections.newSetFromMap(new IdentityHashMap<>());
        private final Map<String, ValueType> resolvedTypeNames = new HashMap<>();
        private final Map<String, Optional<ClassTree>> samePackageFiles = new HashMap<>();
        final Map<String, SamePackageData> samePackageTypes = new HashMap<>();

        private record PendingStepMethod(String stepID, String phase, MethodScope scope, String reason) {}

        private final List<PendingStepMethod> pendingMethods = new ArrayList<>();

        void addStepMethod(String stepID, String phase, MethodScope scope) {
            var reason = scope.method.getBody() == null ? "a " + scope.method.getName() + " without a body" : null;
            pendingMethods.add(new PendingStepMethod(stepID, phase, scope, reason));
        }

        void addUncheckedMethod(String stepID, String phase, String reason) {
            pendingMethods.add(new PendingStepMethod(stepID, phase, null, reason));
        }

        /** Emits markers only for Step methods that hold a lock an RPC also takes, once every lock edge is known. */
        void scanLockedMethods() {
            for (var pending : pendingMethods) {
                if (!sharesRPCLock(pending.stepID(), pending.phase())) continue;
                if (pending.reason() != null) {
                    markers.add(object("nodeId", pending.stepID(), "phase", pending.phase(),
                            "calls", List.of(object("callee", pending.reason(), "certainty", UNCERTAIN))));
                    continue;
                }
                var scan = new ExternalCallScan(pending.scope(), 0, false);
                scan.scan(pending.scope().method.getBody(), null);
                markers.add(object("nodeId", pending.stepID(), "phase", pending.phase(), "calls", scan.calls));
            }
        }

        /** Mirrors firstLockSharedWithRPC: two known, different AttributeMap instances do not conflict. */
        private boolean sharesRPCLock(String stepID, String phase) {
            for (var held : edges) {
                if (!lockEdge(held, phase) || !stepID.equals(held.get("from"))) continue;
                for (var rpc : edges) {
                    if (!lockEdge(rpc, "rpc") || !held.get("to").equals(rpc.get("to"))) continue;
                    var heldInstance = lockInstance(held);
                    var rpcInstance = lockInstance(rpc);
                    if (heldInstance == null || rpcInstance == null || heldInstance.equals(rpcInstance)) return true;
                }
            }
            return false;
        }

        private boolean lockEdge(Map<String, Object> edge, String phase) {
            return "resource_lock".equals(edge.get("kind")) && edge.get("metadata") instanceof Map<?, ?> metadata &&
                    phase.equals(metadata.get("phase"));
        }

        private String lockInstance(Map<String, Object> edge) {
            return ((Map<?, ?>) edge.get("metadata")).get("instance") instanceof String instance && !instance.isEmpty() ? instance : null;
        }

        /** Returns the first possible external call of a same-file helper, and whether the result is exact. */
        ExternalCallResult helperExternalCall(Declaration owner, MethodTree method, int depth) {
            var cached = exactResults.get(method);
            if (cached != null) return new ExternalCallResult(cached.orElse(null), true);
            if (inProgress.contains(method)) return new ExternalCallResult(null, false);
            if (depth + 1 > MAXIMUM_HELPER_DEPTH) {
                return new ExternalCallResult(object("callee", "a helper chain deeper than dexcli follows", "certainty", UNCERTAIN), false);
            }
            inProgress.add(method);
            var scan = new ExternalCallScan(new MethodScope(owner, method), depth + 1, true);
            scan.scan(method.getBody(), null);
            inProgress.remove(method);
            // The first definite call wins; an uncertain one is kept only when the helper makes no definite call.
            var result = scan.calls.stream().filter(call -> !call.containsKey("certainty")).findFirst()
                    .orElse(scan.calls.isEmpty() ? null : scan.calls.get(0));
            if (scan.isExact) exactResults.put(method, Optional.ofNullable(result));
            return new ExternalCallResult(result, scan.isExact);
        }

        ValueType resolveType(Tree type, Declaration owner) {
            if (type == null) return ValueType.of(ValueKind.UNKNOWN, "?");
            if (type instanceof PrimitiveTypeTree) return ValueType.of(ValueKind.PRIMITIVE, type.toString());
            if (type instanceof ArrayTypeTree array) return arrayOf(resolveType(array.getType(), owner));
            if (type instanceof ParameterizedTypeTree parameterized) {
                var arguments = parameterized.getTypeArguments().stream().map(argument -> resolveType(argument, owner)).toList();
                return resolveType(parameterized.getType(), owner).withArguments(arguments);
            }
            if (type instanceof WildcardTree wildcard) return resolveType(wildcard.getBound(), owner);
            if (type instanceof AnnotatedTypeTree annotated) return resolveType(annotated.getUnderlyingType(), owner);
            return resolveTypeName(type.toString(), owner);
        }

        ValueType resolveTypeName(String name, Declaration owner) {
            var declaration = resolveClass(name, owner);
            if (declaration != null) return new ValueType(ValueKind.SAME_FILE, name, null, declaration, List.of(), false);
            return resolvedTypeNames.computeIfAbsent(name, this::resolveImportedTypeName);
        }

        private ValueType resolveImportedTypeName(String name) {
            var head = name.contains(".") ? name.substring(0, name.indexOf('.')) : name;
            var rest = name.substring(head.length());
            if (!head.isEmpty() && Character.isLowerCase(head.charAt(0))) return classifyQualifiedName(name);
            var packageName = unit.getPackageName() == null ? "" : unit.getPackageName().toString();
            for (var importTree : unit.getImports()) {
                var imported = importTree.getQualifiedIdentifier().toString();
                if (importTree.isStatic() || !imported.endsWith("." + head)) continue;
                var importedPackage = imported.substring(0, imported.length() - head.length() - 1);
                if (!packageName.isEmpty() && imported.startsWith(packageName + ".")) {
                    var data = samePackageData(imported.substring(packageName.length() + 1) + rest, importedPackage);
                    if (data != null) return data;
                }
                return classifyQualifiedName(imported + rest);
            }
            var samePackage = samePackageData(name, packageName);
            if (samePackage != null) return samePackage;
            if (loadJdkClass("java.lang." + name) != null) return classifyQualifiedName("java.lang." + name);
            for (var importTree : unit.getImports()) {
                var imported = importTree.getQualifiedIdentifier().toString();
                if (importTree.isStatic() || !imported.endsWith(".*")) continue;
                var wildcardPackage = imported.substring(0, imported.length() - 2);
                if (wildcardPackage.equals(SDK_PACKAGE) && SDK_WILDCARD_TYPES.contains(head) ||
                        loadJdkClass(wildcardPackage + "." + name) != null) {
                    return classifyQualifiedName(wildcardPackage + "." + name);
                }
            }
            return ValueType.of(ValueKind.UNKNOWN, name);
        }

        /** Returns a record or enum declared in a sibling file of the source's package, or null. */
        private ValueType samePackageData(String relativeName, String declaredPackage) {
            var current = unit.getPackageName() == null ? "" : unit.getPackageName().toString();
            if (!declaredPackage.equals(current) && !declaredPackage.startsWith(current + ".")) return null;
            var segments = relativeName.split("\\.");
            var tree = samePackageClass(segments[0]);
            for (int index = 1; tree != null && index < segments.length; index++) {
                var segment = segments[index];
                tree = tree.getMembers().stream().filter(member -> member instanceof ClassTree nested &&
                        nested.getSimpleName().contentEquals(segment)).map(member -> (ClassTree) member).findFirst().orElse(null);
            }
            if (tree == null || (tree.getKind() != Tree.Kind.RECORD && tree.getKind() != Tree.Kind.ENUM)) return null;
            Map<String, Tree> components = new LinkedHashMap<>();
            Set<String> methods = new HashSet<>();
            var hasConstructor = false;
            for (var member : tree.getMembers()) {
                if (member instanceof VariableTree field && !field.getModifiers().getFlags().contains(Modifier.STATIC)) {
                    components.put(field.getName().toString(), field.getType());
                } else if (member instanceof MethodTree method) {
                    if (method.getName().contentEquals("<init>")) hasConstructor = true;
                    else methods.add(method.getName().toString());
                }
            }
            var name = current + "." + relativeName;
            samePackageTypes.put(name, new SamePackageData(tree, components, hasConstructor, methods));
            return ValueType.of(ValueKind.SAME_PACKAGE_DATA, name);
        }

        private ClassTree samePackageClass(String topLevelName) {
            return samePackageFiles.computeIfAbsent(topLevelName, this::parseSamePackageClass).orElse(null);
        }

        private Optional<ClassTree> parseSamePackageClass(String topLevelName) {
            if (!topLevelName.matches("[A-Za-z_$][A-Za-z0-9_$]*") || path == null || path.isEmpty()) return Optional.empty();
            var directory = java.nio.file.Path.of(path).toAbsolutePath().getParent();
            var file = directory == null ? null : directory.resolve(topLevelName + ".java");
            if (file == null || !java.nio.file.Files.isRegularFile(file)) return Optional.empty();
            try {
                var siblingSource = java.nio.file.Files.readString(file);
                var input = new SimpleJavaFileObject(file.toUri(), JavaFileObject.Kind.SOURCE) {
                    @Override public CharSequence getCharContent(boolean ignoreEncodingErrors) { return siblingSource; }
                };
                var task = (JavacTask) ToolProvider.getSystemJavaCompiler().getTask(null, null, ignored -> {}, List.of("-proc:none"), null, List.of(input));
                for (var sibling : task.parse()) {
                    for (var declaration : sibling.getTypeDecls()) {
                        if (declaration instanceof ClassTree type && type.getSimpleName().contentEquals(topLevelName)) return Optional.of(type);
                    }
                }
            } catch (Exception ignored) {
                // An unreadable sibling stays unresolved, so calls on its type count as external.
            }
            return Optional.empty();
        }

        ValueType classifyQualifiedName(String name) {
            if (name.startsWith(SDK_PACKAGE + ".")) {
                var simple = name.substring(SDK_PACKAGE.length() + 1);
                var topLevel = simple.contains(".") ? simple.substring(0, simple.indexOf('.')) : simple;
                // Lowercase segments are subpackages; only the exceptions subpackage belongs to the handler API.
                if (Character.isLowerCase(topLevel.charAt(0)) && !topLevel.equals("exceptions")) return ValueType.of(ValueKind.UNKNOWN, name);
                return ValueType.of(SDK_CONNECTION_TYPES.contains(topLevel) ? ValueKind.SDK_CONNECTION : ValueKind.SDK, name);
            }
            if (name.startsWith("org.slf4j.")) return ValueType.of(PURE_JDK_CLASSES.contains(name) ? ValueKind.PURE : ValueKind.UNKNOWN, name);
            var loaded = loadJdkClass(name);
            return loaded == null ? ValueType.of(ValueKind.UNKNOWN, name) : classifyJdkClass(loaded);
        }

        private Class<?> loadJdkClass(String name) {
            if (!name.startsWith("java.") && !name.startsWith("javax.")) return null;
            var binaryName = name;
            while (true) {
                try {
                    return Class.forName(binaryName, false, ClassLoader.getSystemClassLoader());
                } catch (ClassNotFoundException | LinkageError ignored) {
                    var dot = binaryName.lastIndexOf('.');
                    if (dot < 0 || !Character.isUpperCase(binaryName.charAt(binaryName.lastIndexOf('.', dot - 1) + 1))) return null;
                    binaryName = binaryName.substring(0, dot) + "$" + binaryName.substring(dot + 1);
                }
            }
        }

        ValueType classifyJdkClass(Class<?> type) {
            if (type.isPrimitive()) return ValueType.of(ValueKind.PRIMITIVE, type.getName());
            if (type.isArray()) return arrayOf(classifyJdkClass(type.getComponentType()));
            var outer = type;
            while (outer.getEnclosingClass() != null) outer = outer.getEnclosingClass();
            var packageName = type.getPackageName();
            var isPure = PURE_JDK_CLASSES.contains(outer.getName()) || packageName.equals("java.util.function") ||
                    Throwable.class.isAssignableFrom(type);
            var name = type.getName().replace('$', '.');
            if (!isPure) return ValueType.of(ValueKind.UNKNOWN, name);
            var isFunctional = type.isInterface() && (packageName.equals("java.util.function") || type == Runnable.class ||
                    type.isAnnotationPresent(FunctionalInterface.class) && singleAbstractMethod(type) != null);
            return new ValueType(isFunctional ? ValueKind.FUNCTIONAL : ValueKind.PURE, name, type, null, List.of(), false);
        }

        private ValueType arrayOf(ValueType element) {
            return ValueType.of(ValueKind.ARRAY, element.name() + "[]").withArguments(List.of(element));
        }

        java.lang.reflect.Method singleAbstractMethod(Class<?> type) {
            java.lang.reflect.Method found = null;
            for (var method : type.getMethods()) {
                if (!java.lang.reflect.Modifier.isAbstract(method.getModifiers()) || isObjectMethod(method)) continue;
                if (found != null) return null;
                found = method;
            }
            return found;
        }

        private boolean isObjectMethod(java.lang.reflect.Method method) {
            try {
                Object.class.getMethod(method.getName(), method.getParameterTypes());
                return true;
            } catch (NoSuchMethodException ignored) {
                return false;
            }
        }

        List<java.lang.reflect.Method> jdkMethods(ValueType receiver, String name, int argumentCount) {
            List<java.lang.reflect.Method> methods = new ArrayList<>();
            if (receiver.jdkClass() == null) return methods;
            for (var method : receiver.jdkClass().getMethods()) {
                if (method.isBridge() || !method.getName().equals(name)) continue;
                if (receiver.isTypeName() && !java.lang.reflect.Modifier.isStatic(method.getModifiers())) continue;
                var count = method.getParameterCount();
                if (argumentCount < 0 || count == argumentCount || method.isVarArgs() && argumentCount >= count - 1) methods.add(method);
            }
            return methods;
        }

        /** Binds the receiver class's type variables, and those of its supertypes, to the receiver's type arguments. */
        Map<java.lang.reflect.TypeVariable<?>, ValueType> receiverBindings(ValueType receiver) {
            Map<java.lang.reflect.TypeVariable<?>, ValueType> bindings = new HashMap<>();
            var type = receiver.jdkClass();
            if (type == null) return bindings;
            var parameters = type.getTypeParameters();
            if (parameters.length == receiver.arguments().size()) {
                for (int index = 0; index < parameters.length; index++) bindings.put(parameters[index], receiver.argument(index));
            }
            bindSupertypes(type, bindings, new HashSet<>());
            return bindings;
        }

        private void bindSupertypes(Class<?> type, Map<java.lang.reflect.TypeVariable<?>, ValueType> bindings, Set<Class<?>> visited) {
            if (type == null || !visited.add(type)) return;
            List<java.lang.reflect.Type> supertypes = new ArrayList<>(List.of(type.getGenericInterfaces()));
            if (type.getGenericSuperclass() != null) supertypes.add(type.getGenericSuperclass());
            for (var supertype : supertypes) {
                if (supertype instanceof java.lang.reflect.ParameterizedType parameterized &&
                        parameterized.getRawType() instanceof Class<?> raw) {
                    var variables = raw.getTypeParameters();
                    var actual = parameterized.getActualTypeArguments();
                    for (int index = 0; index < variables.length && index < actual.length; index++) {
                        bindings.putIfAbsent(variables[index], convertType(actual[index], bindings));
                    }
                    bindSupertypes(raw, bindings, visited);
                } else if (supertype instanceof Class<?> raw) {
                    bindSupertypes(raw, bindings, visited);
                }
            }
        }

        ValueType convertType(java.lang.reflect.Type type, Map<java.lang.reflect.TypeVariable<?>, ValueType> bindings) {
            if (type instanceof Class<?> raw) return classifyJdkClass(raw);
            if (type instanceof java.lang.reflect.ParameterizedType parameterized && parameterized.getRawType() instanceof Class<?> raw) {
                var arguments = Arrays.stream(parameterized.getActualTypeArguments()).map(argument -> convertType(argument, bindings)).toList();
                return classifyJdkClass(raw).withArguments(arguments);
            }
            if (type instanceof java.lang.reflect.TypeVariable<?> variable) {
                return bindings.getOrDefault(variable, ValueType.of(ValueKind.UNKNOWN, variable.getName()));
            }
            if (type instanceof java.lang.reflect.WildcardType wildcard) {
                if (wildcard.getLowerBounds().length > 0) return convertType(wildcard.getLowerBounds()[0], bindings);
                var upper = wildcard.getUpperBounds()[0];
                return upper == Object.class ? ValueType.of(ValueKind.UNKNOWN, "?") : convertType(upper, bindings);
            }
            if (type instanceof java.lang.reflect.GenericArrayType array) return arrayOf(convertType(array.getGenericComponentType(), bindings));
            return ValueType.of(ValueKind.UNKNOWN, String.valueOf(type));
        }

        /** Binds a method's type variables from one argument's type. */
        void inferTypeArguments(java.lang.reflect.Type parameter, ValueType argument,
                                Map<java.lang.reflect.TypeVariable<?>, ValueType> bindings) {
            if (argument.kind() == ValueKind.UNKNOWN) return;
            if (parameter instanceof java.lang.reflect.TypeVariable<?> variable &&
                    variable.getGenericDeclaration() instanceof java.lang.reflect.Method) {
                bindings.putIfAbsent(variable, argument);
            } else if (parameter instanceof java.lang.reflect.ParameterizedType parameterized &&
                    parameterized.getActualTypeArguments().length == argument.arguments().size()) {
                var actual = parameterized.getActualTypeArguments();
                for (int index = 0; index < actual.length; index++) inferTypeArguments(actual[index], argument.argument(index), bindings);
            } else if (parameter instanceof java.lang.reflect.GenericArrayType array && argument.kind() == ValueKind.ARRAY) {
                inferTypeArguments(array.getGenericComponentType(), argument.argument(0), bindings);
            } else if (parameter instanceof java.lang.reflect.WildcardType wildcard) {
                var bounds = wildcard.getLowerBounds().length > 0 ? wildcard.getLowerBounds() : wildcard.getUpperBounds();
                inferTypeArguments(bounds[0], argument, bindings);
            }
        }

        java.lang.reflect.Type parameterType(java.lang.reflect.Method method, int index, boolean isSingleVararg) {
            var parameters = method.getGenericParameterTypes();
            if (!method.isVarArgs() || index < parameters.length - 1) return parameters[index];
            var last = parameters[parameters.length - 1];
            if (isSingleVararg) return last;
            return last instanceof java.lang.reflect.GenericArrayType array ? array.getGenericComponentType()
                    : last instanceof Class<?> raw && raw.isArray() ? raw.getComponentType() : last;
        }
    }

    /** Collects the possible external calls of one method body. */
    private final class ExternalCallScan extends TreeScanner<Void, Void> {
        final MethodScope scope;
        final int depth;
        final boolean firstOnly;
        final List<Map<String, Object>> calls = new ArrayList<>();
        boolean isExact = true;
        private final Map<VariableTree, ValueType> inferredTypes = new IdentityHashMap<>();
        private final Map<Tree, Optional<ExternalCallResult>> classified = new IdentityHashMap<>();
        private final ExternalCallFinder finder = externalCalls;

        ExternalCallScan(MethodScope scope, int depth, boolean firstOnly) {
            this.scope = scope;
            this.depth = depth;
            this.firstOnly = firstOnly;
        }

        @Override public Void scan(Tree tree, Void unused) {
            if (firstOnly && calls.stream().anyMatch(call -> !call.containsKey("certainty"))) return null;
            return super.scan(tree, unused);
        }

        @Override public Void visitClass(ClassTree tree, Void unused) { return null; }

        // Calls are recorded after their receiver and arguments, in evaluation order.
        @Override public Void visitMethodInvocation(MethodInvocationTree call, Void unused) {
            typeLambdaParameters(call);
            super.visitMethodInvocation(call, unused);
            record(call);
            return null;
        }

        @Override public Void visitNewClass(NewClassTree created, Void unused) {
            super.visitNewClass(created, unused);
            record(created);
            return null;
        }

        @Override public Void visitMemberReference(MemberReferenceTree reference, Void unused) {
            record(reference);
            return super.visitMemberReference(reference, unused);
        }

        @Override public Void visitTry(TryTree statement, Void unused) {
            super.visitTry(statement, unused);
            for (var resource : statement.getResources()) {
                if (!(resource instanceof VariableTree variable)) continue;
                var type = typeOfVariable(variable);
                // The implicit close of a stream resource runs its close handlers.
                if (type.jdkClass() != null && java.util.stream.BaseStream.class.isAssignableFrom(type.jdkClass())) {
                    calls.add(object("callee", variable.getName() + ".close", "span", span(variable)));
                }
            }
            return null;
        }

        @Override public Void visitEnhancedForLoop(EnhancedForLoopTree loop, Void unused) {
            var variable = loop.getVariable();
            if (isInferred(variable)) {
                var iterable = typeOf(loop.getExpression());
                inferredTypes.put(variable, iterable.arguments().size() == 1 ? iterable.argument(0) : ValueType.of(ValueKind.UNKNOWN, "?"));
            }
            return super.visitEnhancedForLoop(loop, unused);
        }

        private void record(Tree tree) {
            var result = classify(tree);
            if (result == null) return;
            isExact = isExact && result.isExact();
            if (result.call() != null) calls.add(result.call());
        }

        private ExternalCallResult classify(Tree tree) {
            var cached = classified.get(tree);
            if (cached != null) return cached.orElse(null);
            ExternalCallResult result;
            if (tree instanceof MethodInvocationTree call) result = classifyCall(call);
            else if (tree instanceof NewClassTree created) result = classifyConstruction(created);
            else result = classifyReference((MemberReferenceTree) tree);
            classified.put(tree, Optional.ofNullable(result));
            return result;
        }

        private ExternalCallResult external(String callee, Tree site) {
            return new ExternalCallResult(object("callee", callee, "span", span(site)), true);
        }

        private ExternalCallResult uncertain(String callee, Tree site) {
            return new ExternalCallResult(object("callee", callee, "span", span(site), "certainty", UNCERTAIN), true);
        }

        /** Marks a definite result uncertain; a result with no call or an existing certainty is unchanged. */
        private ExternalCallResult asUncertain(ExternalCallResult result) {
            if (result == null || result.call() == null || result.call().containsKey("certainty")) return result;
            var call = new LinkedHashMap<>(result.call());
            call.put("certainty", UNCERTAIN);
            return new ExternalCallResult(call, result.isExact());
        }

        private ExternalCallResult helperCall(String callee, Declaration owner, MethodTree method, Tree site) {
            // An interface or abstract method runs whatever implementation is injected.
            if (method.getBody() == null) return external(callee, site);
            var inner = finder.helperExternalCall(owner, method, depth);
            if (inner.call() == null) return new ExternalCallResult(null, inner.isExact());
            return new ExternalCallResult(object("callee", callee + " (calls " + inner.call().get("callee") + ")",
                    "span", span(site), "certainty", inner.call().get("certainty")), inner.isExact());
        }

        /** Reports whether a receiver comes from another package's type or static call, not from a field or parameter. */
        private boolean isUncertainReceiver(ExpressionTree receiver, int steps) {
            var current = unwrap(receiver);
            while (current instanceof MemberSelectTree || current instanceof MethodInvocationTree) {
                if (scope.owner.field(current) != null || steps++ > MAXIMUM_HELPER_DEPTH * 4) return false;
                current = unwrap(current instanceof MemberSelectTree member ? member.getExpression() : ((MethodInvocationTree) current).getMethodSelect());
            }
            if (current instanceof NewClassTree created) return !isDefiniteInputOutputTypeName(finder.resolveType(created.getIdentifier(), scope.owner).name());
            if (!(current instanceof IdentifierTree identifier)) return false;
            var name = identifier.getName().toString();
            if (name.equals("this") || name.equals("super") || scope.owner.field(identifier) != null) return false;
            var variable = scope.variable(identifier);
            if (variable != null) {
                if (variable.getType() != null && isDefiniteInputOutputTypeName(finder.resolveType(variable.getType(), scope.owner).name())) return false;
                return variable.getInitializer() != null && isUncertainReceiver(variable.getInitializer(), steps + 1);
            }
            var type = finder.resolveTypeName(name, scope.owner);
            return (type.kind() == ValueKind.UNKNOWN || type.kind() == ValueKind.SAME_PACKAGE_DATA) && !isDefiniteInputOutputTypeName(type.name());
        }

        private static boolean isJdkTypeName(String name) {
            return name.startsWith("java.") || name.startsWith("javax.");
        }

        /** Reports whether a type is a JDK type or a known third-party I/O client, whose calls stay definite. */
        private static boolean isDefiniteInputOutputTypeName(String name) {
            return isJdkTypeName(name) || KNOWN_INPUT_OUTPUT_PACKAGES.stream().anyMatch(name::startsWith);
        }

        /** Checks every candidate declaration of an overloaded call and returns the first possible external call. */
        private ExternalCallResult candidatesCall(String callee, List<SameFileMethod> candidates, Tree site) {
            var isAllExact = true;
            Map<String, Object> uncertainCall = null;
            for (var candidate : candidates) {
                var result = helperCall(callee, candidate.owner(), candidate.method(), site);
                isAllExact = isAllExact && result.isExact();
                if (result.call() != null && !result.call().containsKey("certainty")) return new ExternalCallResult(result.call(), isAllExact);
                if (uncertainCall == null) uncertainCall = result.call();
            }
            return new ExternalCallResult(uncertainCall, isAllExact);
        }

        private ExternalCallResult classifyCall(MethodInvocationTree call) {
            var name = callName(call);
            var callee = call.getMethodSelect().toString();
            if (DENIED_METHOD_NAMES.contains(name)) return external(callee, call);
            var receiver = receiver(call);
            var arguments = call.getArguments();
            if (receiver == null) {
                if (name.equals("super") || name.equals("this")) return classifyConstructorCall(call, name);
                var helpers = sameFileMethods(scope.owner, name, arguments, true);
                if (!helpers.isEmpty()) return candidatesCall(callee, helpers, call);
                var inherited = enclosingInheritedSupertype(name, arguments.size());
                if (inherited != null) return classifyMemberCall(inherited, name, arguments, callee, call);
                var imported = staticImportOwner(name);
                if (imported != null) {
                    var result = classifyMemberCall(imported.asTypeName(), name, arguments, callee, call);
                    return isDefiniteInputOutputTypeName(imported.name()) ? result : asUncertain(result);
                }
                return uncertain(callee, call);
            }
            var receiverType = typeOf(receiver);
            var result = classifyMemberCall(receiverType, name, arguments, callee, call);
            // Console output reaches no external system.
            var receiverText = unwrap(receiver).toString();
            if (receiverText.equals("System.out") || receiverText.equals("System.err")) return asUncertain(result);
            var isOtherPackageType = receiverType.kind() == ValueKind.UNKNOWN || receiverType.kind() == ValueKind.SAME_PACKAGE_DATA;
            if (isOtherPackageType && !isDefiniteInputOutputTypeName(receiverType.name()) && isUncertainReceiver(receiver, 0)) return asUncertain(result);
            return result;
        }

        private ValueType enclosingInheritedSupertype(String name, int argumentCount) {
            for (var declaration = scope.owner; declaration != null; declaration = declaration.enclosing) {
                var inherited = inheritedSupertype(declaration, name, argumentCount, new HashSet<>());
                if (inherited == null) continue;
                // SDK and unresolved supertypes cannot confirm the method, so a matching static import wins.
                var isConfirmed = inherited.kind() == ValueKind.PURE || inherited.kind() == ValueKind.FUNCTIONAL;
                return isConfirmed || staticImportOwner(name) == null ? inherited : null;
            }
            return null;
        }

        /** Returns the supertype outside the file that may supply name to a same-file class, or null. */
        private ValueType inheritedSupertype(Declaration declaration, String name, int argumentCount, Set<Declaration> visited) {
            if (declaration == null || !visited.add(declaration)) return null;
            List<Tree> supertypes = new ArrayList<>();
            if (declaration.tree.getExtendsClause() != null) supertypes.add(declaration.tree.getExtendsClause());
            supertypes.addAll(declaration.tree.getImplementsClause());
            for (var supertypeTree : supertypes) {
                var supertype = finder.resolveType(supertypeTree, declaration);
                switch (supertype.kind()) {
                    case SAME_FILE -> {
                        var inherited = inheritedSupertype(supertype.declaration(), name, argumentCount, visited);
                        if (inherited != null) return inherited;
                    }
                    case PURE, FUNCTIONAL -> {
                        if (supertype.jdkClass() != null && !finder.jdkMethods(supertype, name, argumentCount).isEmpty()) return supertype;
                    }
                    case SDK, SDK_CONNECTION, UNKNOWN, SAME_PACKAGE_DATA -> {
                        return supertype;
                    }
                    default -> { }
                }
            }
            return null;
        }

        /** Classifies a call on a receiver type; arguments is null for a method reference. */
        private ExternalCallResult classifyMemberCall(ValueType receiver, String name, List<? extends ExpressionTree> arguments,
                                                      String callee, Tree site) {
            var argumentCount = arguments == null ? -1 : arguments.size();
            switch (receiver.kind()) {
                case SDK, PRIMITIVE, ARRAY:
                    return null;
                case SAME_FILE: {
                    var methods = sameFileMethods(receiver.declaration(), name, arguments, false);
                    if (!methods.isEmpty()) return candidatesCall(callee, methods, site);
                    if (!isImplicitMethod(receiver.declaration(), name, argumentCount)) {
                        // A method the file does not declare follows the allowlist of the supertype that supplies it.
                        var inherited = inheritedSupertype(receiver.declaration(), name, argumentCount, new HashSet<>());
                        return inherited == null ? external(callee, site) : classifyMemberCall(inherited, name, arguments, callee, site);
                    }
                    // values and valueOf initialize the enum, which runs its constructors.
                    var isEnumInitialization = receiver.declaration().tree.getKind() == Tree.Kind.ENUM &&
                            (name.equals("values") || name.equals("valueOf"));
                    return isEnumInitialization ? candidatesCall(callee, sameFileConstructors(receiver.declaration(), null), site) : null;
                }
                case SAME_PACKAGE_DATA:
                    return isSamePackageImplicitMethod(receiver, name, argumentCount) ? null : external(callee, site);
                case PURE:
                    if (receiver.name().equals("java.lang.System") && !ALLOWED_SYSTEM_METHODS.contains(name)) return external(callee, site);
                    if (receiver.jdkClass() == Class.class && !ALLOWED_CLASS_METHODS.contains(name)) return external(callee, site);
                    // Closing a stream runs the close handlers registered on it.
                    if (receiver.jdkClass() != null && java.util.stream.BaseStream.class.isAssignableFrom(receiver.jdkClass()) &&
                            name.equals("close")) return external(callee, site);
                    var simple = receiver.jdkClass() == null ? "" : receiver.jdkClass().getSimpleName();
                    return DENIED_JDK_METHODS.getOrDefault(simple, Set.of()).contains(name) ? external(callee, site) : null;
                case FUNCTIONAL: {
                    if (receiver.isTypeName()) return null;
                    var abstractMethod = receiver.jdkClass() == null ? null : finder.singleAbstractMethod(receiver.jdkClass());
                    return abstractMethod == null || abstractMethod.getName().equals(name) ? external(callee, site) : null;
                }
                default:
                    return OBJECT_METHODS.contains(name) && !name.equals("getClass") ? null : external(callee, site);
            }
        }

        /** Reports whether a call is an undeclared record accessor, enum method, or Object method of a sibling type. */
        private boolean isSamePackageImplicitMethod(ValueType receiver, String name, int argumentCount) {
            var data = finder.samePackageTypes.get(receiver.name());
            if (data == null || data.methods().contains(name)) return false;
            if (OBJECT_METHODS.contains(name)) return true;
            if (data.tree().getKind() == Tree.Kind.ENUM) {
                // A sibling enum constructor is not analyzed, so initializing such an enum counts as external.
                if (data.hasConstructor() && (name.equals("values") || name.equals("valueOf"))) return false;
                return Set.of("values", "valueOf", "name", "ordinal", "compareTo", "getDeclaringClass").contains(name);
            }
            return argumentCount <= 0 && data.components().containsKey(name);
        }

        private boolean isSamePackageImplicitConstructor(ValueType type) {
            var data = finder.samePackageTypes.get(type.name());
            return data != null && data.tree().getKind() == Tree.Kind.RECORD && !data.hasConstructor();
        }

        private ExternalCallResult classifyConstructorCall(MethodInvocationTree call, String name) {
            if (name.equals("this")) return candidatesCall("this", sameFileConstructors(scope.owner, call.getArguments()), call);
            var superclass = scope.owner.tree.getExtendsClause();
            if (superclass == null) return null;
            var type = finder.resolveType(superclass, scope.owner);
            if (type.kind() == ValueKind.SAME_FILE) {
                return candidatesCall("super", sameFileConstructors(type.declaration(), call.getArguments()), call);
            }
            return type.kind() == ValueKind.PURE || type.kind() == ValueKind.SDK ? null : external("super " + type.name(), call);
        }

        private ExternalCallResult classifyConstruction(NewClassTree created) {
            var callee = "new " + created.getIdentifier();
            if (created.getClassBody() != null) return external(callee + " {...}", created);
            if (created.getEnclosingExpression() != null) return external(callee, created);
            var type = finder.resolveType(created.getIdentifier(), scope.owner);
            return switch (type.kind()) {
                case SAME_FILE -> candidatesCall(callee, sameFileConstructors(type.declaration(), created.getArguments()), created);
                case SDK, PURE -> null;
                case SAME_PACKAGE_DATA -> isSamePackageImplicitConstructor(type) ? null : uncertain(callee, created);
                case UNKNOWN -> isJdkTypeName(type.name()) ? external(callee, created) : uncertain(callee, created);
                default -> external(callee, created);
            };
        }

        private ExternalCallResult classifyReference(MemberReferenceTree reference) {
            var qualifier = unwrap(reference.getQualifierExpression());
            var callee = reference.toString();
            var name = reference.getName().toString();
            if (reference.getMode() == MemberReferenceTree.ReferenceMode.NEW) {
                var type = finder.resolveType(qualifier, scope.owner);
                if (type.kind() == ValueKind.SAME_FILE) {
                    return candidatesCall(callee, sameFileConstructors(type.declaration(), null), reference);
                }
                var isAllowed = Set.of(ValueKind.SDK, ValueKind.PURE, ValueKind.ARRAY).contains(type.kind()) ||
                        type.kind() == ValueKind.SAME_PACKAGE_DATA && isSamePackageImplicitConstructor(type);
                return isAllowed ? null : external(callee, reference);
            }
            if (DENIED_METHOD_NAMES.contains(name)) return external(callee, reference);
            if (qualifier instanceof IdentifierTree identifier && identifier.getName().contentEquals("super")) {
                return external(callee, reference);
            }
            return classifyMemberCall(typeOf(qualifier), name, null, callee, reference);
        }

        private record SameFileMethod(Declaration owner, MethodTree method) {}

        /** Returns the same-file declarations a call may run; arguments is null for a method reference. */
        private List<SameFileMethod> sameFileMethods(Declaration owner, String name, List<? extends ExpressionTree> arguments,
                                                     boolean searchEnclosing) {
            for (var declaration = owner; declaration != null; declaration = searchEnclosing ? declaration.enclosing : null) {
                List<SameFileMethod> found = new ArrayList<>();
                collectSameFileMethods(declaration, name, arguments, found, new HashSet<>());
                if (!found.isEmpty()) return applicableMethods(found, arguments);
            }
            return List.of();
        }

        private void collectSameFileMethods(Declaration declaration, String name, List<? extends ExpressionTree> arguments,
                                            List<SameFileMethod> found, Set<Declaration> visited) {
            if (declaration == null || !visited.add(declaration)) return;
            for (var member : declaration.tree.getMembers()) {
                if (member instanceof MethodTree method && method.getName().contentEquals(name) && acceptsArgumentCount(method, arguments)) {
                    found.add(new SameFileMethod(declaration, method));
                }
            }
            List<Tree> supertypes = new ArrayList<>(declaration.tree.getImplementsClause());
            if (declaration.tree.getExtendsClause() != null) supertypes.add(declaration.tree.getExtendsClause());
            for (var supertype : supertypes) {
                collectSameFileMethods(resolveClass(rawType(supertype), declaration), name, arguments, found, visited);
            }
        }

        /** Returns the explicit constructors a creation may run; an empty list means the implicit constructor. */
        private List<SameFileMethod> sameFileConstructors(Declaration declaration, List<? extends ExpressionTree> arguments) {
            List<SameFileMethod> found = new ArrayList<>();
            for (var member : declaration.tree.getMembers()) {
                if (member instanceof MethodTree method && method.getName().contentEquals("<init>") && acceptsArgumentCount(method, arguments)) {
                    found.add(new SameFileMethod(declaration, method));
                }
            }
            return applicableMethods(found, arguments);
        }

        private boolean acceptsArgumentCount(MethodTree method, List<? extends ExpressionTree> arguments) {
            if (arguments == null) return true;
            var parameters = method.getParameters();
            if (parameters.size() == arguments.size()) return true;
            return isVarargs(method) && arguments.size() >= parameters.size() - 1;
        }

        private boolean isVarargs(MethodTree method) {
            var parameters = method.getParameters();
            return !parameters.isEmpty() && parameters.get(parameters.size() - 1).toString().contains("...");
        }

        /** Drops only candidates whose parameters provably reject an argument; every remaining candidate is checked. */
        private List<SameFileMethod> applicableMethods(List<SameFileMethod> candidates, List<? extends ExpressionTree> arguments) {
            if (arguments == null || candidates.size() < 2) return candidates;
            var argumentTypes = arguments.stream().map(argument -> typeOf(unwrap(argument))).toList();
            var applicable = candidates.stream().filter(candidate -> {
                var parameters = candidate.method().getParameters();
                var checked = isVarargs(candidate.method()) ? parameters.size() - 1 : parameters.size();
                for (int index = 0; index < checked && index < argumentTypes.size(); index++) {
                    var parameter = finder.resolveType(parameters.get(index).getType(), candidate.owner());
                    if (isProvablyIncompatible(argumentTypes.get(index), parameter)) return false;
                }
                return true;
            }).toList();
            return applicable.isEmpty() ? candidates : applicable;
        }

        /** Reports whether a value of type argument can never be passed as parameter; anything unresolved stays compatible. */
        private boolean isProvablyIncompatible(ValueType argument, ValueType parameter) {
            if (argument.kind() == ValueKind.UNKNOWN || parameter.kind() == ValueKind.UNKNOWN) return false;
            if (parameter.jdkClass() == Object.class) return false;
            if (argument.kind() == ValueKind.PRIMITIVE || parameter.kind() == ValueKind.PRIMITIVE) {
                return !(isNumericOrBoxed(argument) && isNumericOrBoxed(parameter));
            }
            if (argument.kind() == ValueKind.SAME_FILE) return !mayBeSubtype(argument.declaration(), parameter, new HashSet<>());
            if (argument.jdkClass() != null && parameter.jdkClass() != null) return !parameter.jdkClass().isAssignableFrom(argument.jdkClass());
            if (argument.jdkClass() != null) return parameter.kind() == ValueKind.SAME_FILE || parameter.kind() == ValueKind.SDK;
            // A lambda fits only a functional interface; a same-file interface may be one.
            if (argument.kind() == ValueKind.FUNCTIONAL && argument.jdkClass() == null) return parameter.jdkClass() != null &&
                    parameter.kind() != ValueKind.FUNCTIONAL;
            return false;
        }

        /** Reports whether a same-file class may be a subtype of parameter, following its supertypes into the JDK and SDK. */
        private boolean mayBeSubtype(Declaration declaration, ValueType parameter, Set<Declaration> visited) {
            if (declaration == null || !visited.add(declaration)) return false;
            if (parameter.kind() == ValueKind.SAME_FILE && parameter.declaration() == declaration) return true;
            List<Tree> supertypes = new ArrayList<>(declaration.tree.getImplementsClause());
            if (declaration.tree.getExtendsClause() != null) supertypes.add(declaration.tree.getExtendsClause());
            List<ValueType> resolved = new ArrayList<>();
            for (var supertype : supertypes) resolved.add(finder.resolveType(supertype, declaration));
            var implicit = switch (declaration.tree.getKind()) {
                case ENUM -> Enum.class;
                case RECORD -> Record.class;
                default -> Object.class;
            };
            resolved.add(finder.classifyJdkClass(implicit));
            for (var supertype : resolved) {
                switch (supertype.kind()) {
                    case SAME_FILE:
                        if (mayBeSubtype(supertype.declaration(), parameter, visited)) return true;
                        break;
                    case SDK, SDK_CONNECTION:
                        // SDK type hierarchies are not modeled, so an SDK supertype may reach any non-local parameter.
                        if (parameter.kind() != ValueKind.SAME_FILE) return true;
                        break;
                    default:
                        if (supertype.jdkClass() == null) return true;
                        if (parameter.jdkClass() != null && parameter.jdkClass().isAssignableFrom(supertype.jdkClass())) return true;
                }
            }
            return false;
        }

        /** Reports whether a primitive or a boxed value may convert to type, for example int to Comparable. */
        private boolean isNumericOrBoxed(ValueType type) {
            if (type.kind() == ValueKind.PRIMITIVE) return true;
            if (type.jdkClass() == null) return type.kind() != ValueKind.SAME_FILE && type.kind() != ValueKind.ARRAY;
            for (var boxed : List.of(Integer.class, Long.class, Double.class, Float.class, Short.class, Byte.class,
                    Character.class, Boolean.class)) {
                if (type.jdkClass().isAssignableFrom(boxed) || boxed.isAssignableFrom(type.jdkClass())) return true;
            }
            return false;
        }

        /** Returns the return type shared by every candidate, or unknown when they differ. */
        private ValueType candidatesReturnType(List<SameFileMethod> candidates, String name) {
            ValueType result = null;
            for (var candidate : candidates) {
                var type = finder.resolveType(candidate.method().getReturnType(), candidate.owner());
                if (result == null) result = type;
                else if (result.kind() != type.kind() || !result.name().equals(type.name())) return ValueType.of(ValueKind.UNKNOWN, name);
            }
            return result == null ? ValueType.of(ValueKind.UNKNOWN, name) : result;
        }

        /** Reports whether a call is an undeclared Object method, enum method, or record accessor. */
        private boolean isImplicitMethod(Declaration declaration, String name, int argumentCount) {
            if (OBJECT_METHODS.contains(name) && !hasExternalSupertype(declaration)) return true;
            var kind = declaration.tree.getKind();
            if (kind == Tree.Kind.ENUM && Set.of("values", "valueOf", "name", "ordinal", "compareTo", "getDeclaringClass").contains(name)) {
                return true;
            }
            return kind == Tree.Kind.RECORD && argumentCount <= 0 && declaration.fields.containsKey(name);
        }

        private boolean hasExternalSupertype(Declaration declaration) {
            var superclass = declaration.tree.getExtendsClause();
            if (superclass == null || declaration.tree.getKind() == Tree.Kind.INTERFACE) return false;
            var type = finder.resolveType(superclass, declaration);
            if (type.kind() == ValueKind.SAME_FILE) return hasExternalSupertype(type.declaration());
            return type.kind() != ValueKind.PURE && type.kind() != ValueKind.SDK;
        }

        private ValueType staticImportOwner(String name) {
            for (var importTree : unit.getImports()) {
                var imported = importTree.getQualifiedIdentifier().toString();
                if (!importTree.isStatic()) continue;
                var dot = imported.lastIndexOf('.');
                var member = imported.substring(dot + 1);
                if (member.equals(name) || member.equals("*")) {
                    var owner = finder.classifyQualifiedName(imported.substring(0, dot));
                    if (member.equals(name) || owner.kind() != ValueKind.UNKNOWN &&
                            (owner.jdkClass() == null || !finder.jdkMethods(owner.asTypeName(), name, -1).isEmpty())) return owner;
                }
            }
            return null;
        }

        private boolean isInferred(VariableTree variable) {
            return variable.getType() == null || variable.getType().toString().equals("var");
        }

        ValueType typeOfVariable(VariableTree variable) {
            var inferred = inferredTypes.get(variable);
            if (inferred != null) return inferred;
            if (!isInferred(variable)) return finder.resolveType(variable.getType(), scope.owner);
            if (variable.getInitializer() == null) return ValueType.of(ValueKind.UNKNOWN, variable.getName().toString());
            inferredTypes.put(variable, ValueType.of(ValueKind.UNKNOWN, variable.getName().toString()));
            var type = typeOf(variable.getInitializer());
            inferredTypes.put(variable, type);
            return type;
        }

        ValueType typeOf(ExpressionTree expression) {
            if (expression instanceof TypeCastTree cast) return finder.resolveType(cast.getType(), scope.owner);
            if (expression instanceof ParenthesizedTree parentheses) return typeOf(parentheses.getExpression());
            if (expression instanceof LiteralTree literal) {
                if (literal.getValue() == null) return ValueType.of(ValueKind.UNKNOWN, "null");
                return literal.getValue() instanceof String ? finder.classifyJdkClass(String.class) : ValueType.of(ValueKind.PRIMITIVE, "literal");
            }
            if (expression instanceof IdentifierTree identifier) return typeOfIdentifier(identifier);
            if (expression instanceof MemberSelectTree member) return typeOfMember(member);
            if (expression instanceof MethodInvocationTree call) return callResultType(call);
            if (expression instanceof NewClassTree created) return finder.resolveType(created.getIdentifier(), scope.owner);
            if (expression instanceof NewArrayTree array) {
                return array.getType() == null ? ValueType.of(ValueKind.ARRAY, "array")
                        : finder.arrayOf(finder.resolveType(array.getType(), scope.owner));
            }
            if (expression instanceof ArrayAccessTree access) return typeOf(access.getExpression()).argument(0);
            if (expression instanceof ConditionalExpressionTree conditional) return typeOf(conditional.getTrueExpression());
            if (expression instanceof BinaryTree binary) {
                var isText = expression.getKind() == Tree.Kind.PLUS && (typeOf(binary.getLeftOperand()).name().equals("java.lang.String") ||
                        typeOf(binary.getRightOperand()).name().equals("java.lang.String"));
                return isText ? finder.classifyJdkClass(String.class) : ValueType.of(ValueKind.PRIMITIVE, "boolean");
            }
            if (expression instanceof UnaryTree || expression instanceof InstanceOfTree) return ValueType.of(ValueKind.PRIMITIVE, "value");
            if (expression instanceof LambdaExpressionTree || expression instanceof MemberReferenceTree) {
                return ValueType.of(ValueKind.FUNCTIONAL, "lambda");
            }
            return ValueType.of(ValueKind.UNKNOWN, String.valueOf(expression));
        }

        private ValueType typeOfIdentifier(IdentifierTree identifier) {
            var name = identifier.getName().toString();
            if (name.equals("this")) return new ValueType(ValueKind.SAME_FILE, scope.owner.qualifiedName(), null, scope.owner, List.of(), false);
            if (name.equals("super")) {
                var superclass = scope.owner.tree.getExtendsClause();
                return superclass == null ? finder.classifyJdkClass(Object.class) : finder.resolveType(superclass, scope.owner);
            }
            var variable = scope.variable(identifier);
            if (variable != null) return typeOfVariable(variable);
            var type = finder.resolveTypeName(name, scope.owner);
            return type.kind() == ValueKind.UNKNOWN ? type : type.asTypeName();
        }

        private ValueType typeOfMember(MemberSelectTree member) {
            var qualifier = unwrap(member.getExpression());
            var name = member.getIdentifier().toString();
            if (name.equals("this")) {
                var declaration = resolveClass(qualifier.toString(), scope.owner);
                return declaration == null ? ValueType.of(ValueKind.UNKNOWN, member.toString())
                        : new ValueType(ValueKind.SAME_FILE, declaration.qualifiedName(), null, declaration, List.of(), false);
            }
            if (name.equals("class")) return finder.classifyJdkClass(Class.class);
            var field = scope.owner.field(member);
            if (field != null) return typeOfVariable(field);
            var isVariable = qualifier instanceof IdentifierTree && scope.variable(qualifier) != null;
            if (!isVariable && !(qualifier instanceof MethodInvocationTree)) {
                var typeName = finder.resolveTypeName(member.toString(), scope.owner);
                if (typeName.kind() != ValueKind.UNKNOWN) return typeName.asTypeName();
            }
            var owner = typeOf(qualifier);
            return switch (owner.kind()) {
                case ARRAY -> ValueType.of(ValueKind.PRIMITIVE, "int");
                case SAME_FILE -> {
                    var declared = owner.declaration().fields.get(name);
                    yield declared == null ? ValueType.of(ValueKind.UNKNOWN, member.toString()) : typeOfVariable(declared);
                }
                case PURE, FUNCTIONAL -> {
                    if (owner.jdkClass() == null) yield ValueType.of(ValueKind.PURE, member.toString());
                    try {
                        var reflected = owner.jdkClass().getField(name);
                        yield finder.convertType(reflected.getGenericType(), finder.receiverBindings(owner));
                    } catch (NoSuchFieldException ignored) {
                        yield ValueType.of(ValueKind.UNKNOWN, member.toString());
                    }
                }
                default -> ValueType.of(ValueKind.UNKNOWN, member.toString());
            };
        }

        private ValueType callResultType(MethodInvocationTree call) {
            var name = callName(call);
            var receiver = receiver(call);
            if (receiver == null) {
                var helpers = sameFileMethods(scope.owner, name, call.getArguments(), true);
                if (!helpers.isEmpty()) return candidatesReturnType(helpers, name);
                var inherited = enclosingInheritedSupertype(name, call.getArguments().size());
                if (inherited != null) return memberResultType(inherited, call);
                var imported = staticImportOwner(name);
                return imported == null ? ValueType.of(ValueKind.UNKNOWN, name) : memberResultType(imported.asTypeName(), call);
            }
            return memberResultType(typeOf(receiver), call);
        }

        private ValueType jdkResultType(ValueType receiver, MethodInvocationTree call) {
            var name = callName(call);
            var arguments = call.getArguments();
            if (receiver.jdkClass() == null) return ValueType.of(ValueKind.PURE, receiver.name());
            typeLambdaParameters(call);
            ValueType result = null;
            for (var method : finder.jdkMethods(receiver, name, arguments.size())) {
                var bindings = finder.receiverBindings(receiver);
                var isSingleVararg = method.isVarArgs() && arguments.size() == method.getParameterCount();
                for (int index = 0; index < arguments.size(); index++) {
                    var parameter = finder.parameterType(method, index, isSingleVararg);
                    var argument = unwrap(arguments.get(index));
                    if (argument instanceof LambdaExpressionTree || argument instanceof MemberReferenceTree) {
                        inferFunctionalResult(parameter, argument, bindings);
                    } else {
                        finder.inferTypeArguments(parameter, typeOf(argument), bindings);
                    }
                }
                var candidate = finder.convertType(method.getGenericReturnType(), bindings);
                if (result == null) result = candidate;
                else if (!candidate.name().equals(result.name()) || candidate.kind() != result.kind()) {
                    return ValueType.of(ValueKind.UNKNOWN, receiver.name() + "." + name);
                }
            }
            return result == null ? ValueType.of(ValueKind.UNKNOWN, receiver.name() + "." + name) : result;
        }

        /** Binds a functional parameter's result type variable, such as R of map, from a lambda or reference. */
        private void inferFunctionalResult(java.lang.reflect.Type parameter, ExpressionTree argument,
                                           Map<java.lang.reflect.TypeVariable<?>, ValueType> bindings) {
            if (!(parameter instanceof java.lang.reflect.ParameterizedType parameterized) ||
                    !(parameterized.getRawType() instanceof Class<?> raw)) return;
            var abstractMethod = finder.singleAbstractMethod(raw);
            if (abstractMethod == null || !(abstractMethod.getGenericReturnType() instanceof java.lang.reflect.TypeVariable<?> variable)) return;
            var index = Arrays.asList(raw.getTypeParameters()).indexOf(variable);
            if (index < 0) return;
            finder.inferTypeArguments(parameterized.getActualTypeArguments()[index], functionalResultType(argument), bindings);
        }

        private ValueType functionalResultType(ExpressionTree argument) {
            if (argument instanceof LambdaExpressionTree lambda) {
                if (lambda.getBody() instanceof ExpressionTree body) return typeOf(body);
                var returned = new ReturnScanner();
                returned.scan(lambda.getBody(), "");
                return returned.outcomes.isEmpty() ? ValueType.of(ValueKind.UNKNOWN, "lambda") : typeOf(returned.outcomes.get(0).expression());
            }
            var reference = (MemberReferenceTree) argument;
            var qualifier = unwrap(reference.getQualifierExpression());
            if (reference.getMode() == MemberReferenceTree.ReferenceMode.NEW) return finder.resolveType(qualifier, scope.owner);
            var receiver = typeOf(qualifier);
            var name = reference.getName().toString();
            if (receiver.kind() == ValueKind.SAME_FILE) {
                var methods = sameFileMethods(receiver.declaration(), name, null, false);
                if (!methods.isEmpty()) return candidatesReturnType(methods, name);
                var field = receiver.declaration().fields.get(name);
                return field == null ? ValueType.of(ValueKind.UNKNOWN, name) : finder.resolveType(field.getType(), receiver.declaration());
            }
            if (receiver.kind() == ValueKind.SAME_PACKAGE_DATA) return samePackageResultType(receiver, name);
            if (receiver.jdkClass() == null) return ValueType.of(ValueKind.UNKNOWN, name);
            var instance = new ValueType(receiver.kind(), receiver.name(), receiver.jdkClass(), null, receiver.arguments(), false);
            var methods = finder.jdkMethods(instance, name, -1);
            return methods.isEmpty() ? ValueType.of(ValueKind.UNKNOWN, name)
                    : finder.convertType(methods.get(0).getGenericReturnType(), finder.receiverBindings(instance));
        }

        private ValueType memberResultType(ValueType receiver, MethodInvocationTree call) {
            var name = callName(call);
            var arguments = call.getArguments();
            if (name.equals("toString")) return finder.classifyJdkClass(String.class);
            if (name.equals("equals") || name.equals("hashCode")) return ValueType.of(ValueKind.PRIMITIVE, "value");
            switch (receiver.kind()) {
                case SAME_FILE: {
                    var declaration = receiver.declaration();
                    var methods = sameFileMethods(declaration, name, arguments, false);
                    if (!methods.isEmpty()) return candidatesReturnType(methods, name);
                    var inherited = inheritedSupertype(declaration, name, arguments.size(), new HashSet<>());
                    if (inherited != null && inherited.kind() != ValueKind.UNKNOWN) return memberResultType(inherited, call);
                    var field = declaration.fields.get(name);
                    if (declaration.tree.getKind() == Tree.Kind.RECORD && arguments.isEmpty() && field != null) {
                        return finder.resolveType(field.getType(), declaration);
                    }
                    if (name.equals("values")) return finder.arrayOf(receiver.withArguments(List.of()));
                    if (name.equals("valueOf")) return receiver;
                    if (name.equals("name")) return finder.classifyJdkClass(String.class);
                    return ValueType.of(ValueKind.PRIMITIVE, "value");
                }
                case SAME_PACKAGE_DATA:
                    return samePackageResultType(receiver, name);
                case SDK:
                    return sdkResultType(receiver, name, arguments);
                case PURE, FUNCTIONAL:
                    return jdkResultType(receiver, call);
                case ARRAY:
                    return name.equals("clone") ? receiver : ValueType.of(ValueKind.PRIMITIVE, "value");
                default:
                    return ValueType.of(ValueKind.UNKNOWN, call.getMethodSelect().toString());
            }
        }

        /** Types a sibling record accessor by its component; the component type is read with this file's imports. */
        private ValueType samePackageResultType(ValueType receiver, String name) {
            var data = finder.samePackageTypes.get(receiver.name());
            var component = data == null ? null : data.components().get(name);
            if (component != null) return finder.resolveType(component, scope.owner);
            if (name.equals("values")) return finder.arrayOf(receiver.withArguments(List.of()));
            if (name.equals("valueOf")) return receiver;
            if (name.equals("name")) return finder.classifyJdkClass(String.class);
            return ValueType.of(ValueKind.PRIMITIVE, "value");
        }

        /** Types an SDK call by SDK_RETURN_TYPES; an unlisted SDK method returns an unknown value. */
        private ValueType sdkResultType(ValueType receiver, String name, List<? extends ExpressionTree> arguments) {
            var typeName = receiver.name().startsWith(SDK_PACKAGE + ".") ? receiver.name().substring(SDK_PACKAGE.length() + 1) : receiver.name();
            var specification = SDK_RETURN_TYPES.get(typeName + "." + name);
            if (specification == null) specification = SDK_RETURN_TYPES.get(typeName + ".*");
            if (specification == null) return ValueType.of(ValueKind.UNKNOWN, receiver.name() + "." + name);
            Map<String, ValueType> variables = new HashMap<>();
            variables.put("T", receiver.argument(0));
            variables.put("A", arguments.isEmpty() ? ValueType.of(ValueKind.UNKNOWN, "?") : typeOf(arguments.get(0)).argument(0));
            var classType = arguments.isEmpty() ? null : classLiteral(arguments.get(arguments.size() - 1));
            variables.put("C", classType == null ? ValueType.of(ValueKind.UNKNOWN, "?") : finder.resolveTypeName(classType, scope.owner));
            return parseSDKReturnType(specification, variables);
        }

        private ValueType parseSDKReturnType(String specification, Map<String, ValueType> variables) {
            var opening = specification.indexOf('<');
            var base = opening < 0 ? specification : specification.substring(0, opening);
            var variable = variables.get(base);
            if (variable != null) return variable;
            ValueType type = switch (base) {
                case "int", "boolean", "long" -> ValueType.of(ValueKind.PRIMITIVE, base);
                case "String" -> finder.classifyJdkClass(String.class);
                case "Instant" -> finder.classifyJdkClass(java.time.Instant.class);
                case "List" -> finder.classifyJdkClass(List.class);
                default -> finder.classifyQualifiedName(SDK_PACKAGE + "." + base);
            };
            if (opening < 0) return type;
            var argument = parseSDKReturnType(specification.substring(opening + 1, specification.length() - 1), variables);
            return type.withArguments(List.of(argument));
        }


        /** Types implicit lambda parameters from the JDK method's functional parameter. */
        private void typeLambdaParameters(MethodInvocationTree call) {
            var arguments = call.getArguments();
            for (int index = 0; index < arguments.size(); index++) {
                if (!(arguments.get(index) instanceof LambdaExpressionTree lambda)) continue;
                var parameters = lambda.getParameters();
                var types = lambdaParameterTypes(call, index, parameters.size());
                for (int position = 0; position < parameters.size(); position++) {
                    var parameter = parameters.get(position);
                    if (isInferred(parameter)) {
                        inferredTypes.put(parameter, types == null ? ValueType.of(ValueKind.UNKNOWN, parameter.getName().toString())
                                : types.get(position));
                    }
                }
            }
        }

        private List<ValueType> lambdaParameterTypes(MethodInvocationTree call, int argumentIndex, int parameterCount) {
            var receiver = receiver(call);
            ValueType owner;
            if (receiver != null) owner = typeOf(receiver);
            else owner = staticImportOwner(callName(call)) == null ? null : staticImportOwner(callName(call)).asTypeName();
            if (owner == null || (owner.kind() != ValueKind.PURE && owner.kind() != ValueKind.FUNCTIONAL)) return null;
            var arguments = call.getArguments();
            for (var method : finder.jdkMethods(owner, callName(call), arguments.size())) {
                var bindings = finder.receiverBindings(owner);
                var isSingleVararg = method.isVarArgs() && arguments.size() == method.getParameterCount();
                for (int index = 0; index < arguments.size(); index++) {
                    if (index != argumentIndex) {
                        finder.inferTypeArguments(finder.parameterType(method, index, isSingleVararg), typeOf(arguments.get(index)), bindings);
                    }
                }
                var functional = finder.convertType(finder.parameterType(method, argumentIndex, isSingleVararg), bindings);
                if (functional.jdkClass() == null) continue;
                var abstractMethod = finder.singleAbstractMethod(functional.jdkClass());
                if (abstractMethod == null || abstractMethod.getParameterCount() != parameterCount) continue;
                var functionalBindings = finder.receiverBindings(functional);
                return Arrays.stream(abstractMethod.getGenericParameterTypes())
                        .map(parameter -> finder.convertType(parameter, functionalBindings)).toList();
            }
            return null;
        }
    }

    private static Map<String, Object> integerSchema(long minimum, long maximum) {
        return object("kind", "integer", "minimum", Long.toString(minimum), "maximum", Long.toString(maximum));
    }

    private final class Declaration {
        final ClassTree tree;
        final Declaration enclosing;
        final Map<String, VariableTree> fields = new LinkedHashMap<>();

        Declaration(ClassTree tree, Declaration enclosing) {
            this.tree = tree;
            this.enclosing = enclosing;
            for (var member : tree.getMembers()) {
                if (member instanceof VariableTree field) fields.put(field.getName().toString(), field);
            }
        }

        String qualifiedName() {
            return (enclosing == null ? "" : enclosing.qualifiedName() + ".") + tree.getSimpleName();
        }

        boolean implementsType(String name) {
            return tree.getImplementsClause().stream().anyMatch(type -> simpleName(rawType(type)).equals(name));
        }

        String interfaceArgument(String name) {
            for (var type : tree.getImplementsClause()) {
                if (type instanceof ParameterizedTypeTree parameterized && simpleName(rawType(type)).equals(name))
                    return parameterized.getTypeArguments().get(0).toString();
            }
            return null;
        }

        MethodTree method(String name) {
            return method(name, -1);
        }

        MethodTree method(String name, int argumentCount) {
            for (var member : tree.getMembers()) {
                if (member instanceof MethodTree method && method.getName().contentEquals(name) &&
                        (argumentCount < 0 || argumentCount == method.getParameters().size())) return method;
            }
            return null;
        }

        VariableTree field(ExpressionTree expression) {
            if (expression instanceof IdentifierTree identifier) {
                for (var owner = this; owner != null; owner = owner.enclosing) {
                    var field = owner.fields.get(identifier.getName().toString());
                    if (field != null) return field;
                }
            }
            if (expression instanceof MemberSelectTree member) {
                var receiver = member.getExpression().toString();
                var target = receiver.equals("this") ? this : resolveClass(receiver.replaceFirst("\\.this$", ""), this);
                if (target != null) return target.fields.get(member.getIdentifier().toString());
            }
            return null;
        }
    }

    private final class MethodScope {
        final Declaration owner;
        final MethodTree method;
        final List<LocalBinding> locals = new ArrayList<>();
        final Map<VariableTree, List<ExpressionTree>> mutations = new IdentityHashMap<>();

        MethodScope(Declaration owner, MethodTree method) {
            this.owner = owner;
            this.method = method;
            new LocalScanner(this).scan(method, method);
        }

        MethodScope helperScope(MethodInvocationTree call) {
            var receiver = unwrap(receiver(call));
            if (receiver == null) {
                for (var declaration = owner; declaration != null; declaration = declaration.enclosing) {
                    var candidate = declaration.method(callName(call), call.getArguments().size());
                    if (candidate != null) return new MethodScope(declaration, candidate);
                }
                return null;
            }
            var qualifier = receiver.toString();
            Declaration declaration;
            if (qualifier.equals("this")) {
                declaration = owner;
            } else {
                if (variable(receiver) != null) return null;
                declaration = resolveClass(qualifier.replaceFirst("\\.this$", ""), owner);
            }
            if (declaration == null) return null;
            var candidate = declaration.method(callName(call), call.getArguments().size());
            return candidate == null ? null : new MethodScope(declaration, candidate);
        }

        VariableTree variable(ExpressionTree expression) {
            if (expression instanceof IdentifierTree identifier) {
                long location = positions.getStartPosition(unit, identifier);
                for (int index = locals.size() - 1; index >= 0; index--) {
                    var binding = locals.get(index);
                    if (binding.variable.getName().equals(identifier.getName()) &&
                            binding.start <= location && location < binding.end) return binding.variable;
                }
            }
            return owner.field(expression);
        }

        List<ExpressionTree> values(ExpressionTree expression) {
            var variable = variable(expression);
            if (variable == null) return List.of();
            List<ExpressionTree> values = new ArrayList<>();
            if (variable.getInitializer() != null) values.add(variable.getInitializer());
            values.addAll(mutations.getOrDefault(variable, List.of()));
            return values;
        }

        ExpressionTree initializer(ExpressionTree expression) {
            Set<VariableTree> visited = new HashSet<>();
            expression = unwrap(expression);
            while (expression instanceof IdentifierTree || expression instanceof MemberSelectTree) {
                var variable = variable(expression);
                if (variable == null || !visited.add(variable) || variable.getInitializer() == null ||
                        mutations.containsKey(variable)) break;
                expression = unwrap(variable.getInitializer());
            }
            return expression;
        }

        List<Outcome> outcomes() {
            return outcomes("");
        }

        List<Outcome> outcomes(String condition) {
            var scanner = new ReturnScanner();
            scanner.scan(method.getBody(), condition);
            return scanner.outcomes;
        }
    }

    private record LocalBinding(VariableTree variable, long start, long end) {}
    private record Outcome(ExpressionTree expression, String condition) {}

    private final class LocalScanner extends TreeScanner<Void, Tree> {
        private final MethodScope scope;

        LocalScanner(MethodScope scope) { this.scope = scope; }

        @Override public Void visitClass(ClassTree tree, Tree within) { return null; }
        @Override public Void visitBlock(BlockTree tree, Tree within) { return super.visitBlock(tree, tree); }
        @Override public Void visitForLoop(ForLoopTree tree, Tree within) { return super.visitForLoop(tree, tree); }
        @Override public Void visitEnhancedForLoop(EnhancedForLoopTree tree, Tree within) { return super.visitEnhancedForLoop(tree, tree); }
        @Override public Void visitCatch(CatchTree tree, Tree within) { return super.visitCatch(tree, tree); }
        @Override public Void visitLambdaExpression(LambdaExpressionTree tree, Tree within) { return super.visitLambdaExpression(tree, tree); }

        @Override public Void visitVariable(VariableTree variable, Tree within) {
            var start = variable.getInitializer() == null ? positions.getStartPosition(unit, variable)
                    : positions.getEndPosition(unit, variable.getInitializer());
            scope.locals.add(new LocalBinding(variable, start, positions.getEndPosition(unit, within)));
            return super.visitVariable(variable, within);
        }

        @Override public Void visitAssignment(AssignmentTree assignment, Tree within) {
            var target = assignment.getVariable();
            if (target instanceof ArrayAccessTree array) target = array.getExpression();
            add(target, assignment.getExpression());
            return super.visitAssignment(assignment, within);
        }

        @Override public Void visitMethodInvocation(MethodInvocationTree call, Tree within) {
            if (Set.of("add", "addAll", "set").contains(callName(call))) {
                var arguments = call.getArguments();
                if (!arguments.isEmpty()) add(receiver(call), arguments.get(arguments.size() - 1));
            }
            return super.visitMethodInvocation(call, within);
        }

        private void add(ExpressionTree target, ExpressionTree value) {
            var variable = scope.variable(target);
            if (variable != null) scope.mutations.computeIfAbsent(variable, ignored -> new ArrayList<>()).add(value);
        }
    }

    private static final class ReturnScanner extends TreeScanner<Void, String> {
        final List<Outcome> outcomes = new ArrayList<>();

        @Override public Void visitClass(ClassTree tree, String condition) { return null; }
        @Override public Void visitLambdaExpression(LambdaExpressionTree tree, String condition) { return null; }

        @Override public Void visitBlock(BlockTree block, String condition) {
            scanStatements(block.getStatements(), condition);
            return null;
        }

        @Override public Void visitIf(IfTree branch, String condition) {
            var text = unwrap(branch.getCondition()).toString();
            scan(branch.getThenStatement(), combine(condition, text));
            scan(branch.getElseStatement(), combine(condition, negate(text)));
            return null;
        }

        @Override public Void visitSwitch(SwitchTree selection, String condition) {
            scanCases(selection.getExpression(), selection.getCases(), condition);
            return null;
        }

        @Override public Void visitSwitchExpression(SwitchExpressionTree selection, String condition) {
            return null;
        }

        @Override public Void visitCatch(CatchTree handler, String condition) {
            scan(handler.getBlock(), combine(condition, "exception"));
            return null;
        }

        @Override public Void visitReturn(ReturnTree returned, String condition) {
            expand(returned.getExpression(), condition);
            return null;
        }

        @Override public Void visitYield(YieldTree yielded, String condition) {
            expand(yielded.getValue(), condition);
            return null;
        }

        private String scanStatements(List<? extends StatementTree> statements, String condition) {
            for (var statement : statements) {
                scan(statement, condition);
                if (alwaysExits(statement)) return null;
                if (statement instanceof IfTree branch) {
                    var text = unwrap(branch.getCondition()).toString();
                    if (alwaysExits(branch.getThenStatement())) condition = combine(condition, negate(text));
                    else if (alwaysExits(branch.getElseStatement())) condition = combine(condition, text);
                }
            }
            return condition;
        }

        private void scanCases(ExpressionTree subject, List<? extends CaseTree> cases, String condition) {
            var selector = unwrap(subject).toString();
            String fallthrough = null;
            for (var arm : cases) {
                var labels = arm.getExpressions().stream().map(value -> selector + " == " + value).toList();
                var label = labels.isEmpty() ? "default" : labels.size() == 1 ? labels.get(0) :
                        "(" + String.join(" || ", labels) + ")";
                var armCondition = combine(condition, label);
                if (fallthrough != null) armCondition = "((" + fallthrough + ") || (" + armCondition + "))";
                if (arm.getCaseKind() == CaseTree.CaseKind.STATEMENT) {
                    fallthrough = scanStatements(arm.getStatements(), armCondition);
                } else if (arm.getBody() instanceof ExpressionTree expression) {
                    expand(expression, armCondition);
                } else {
                    scan(arm.getBody(), armCondition);
                }
            }
        }

        private boolean alwaysExits(StatementTree statement) {
            if (statement == null) return false;
            return switch (statement.getKind()) {
                case RETURN, THROW, BREAK, CONTINUE, YIELD -> true;
                case BLOCK -> ((BlockTree) statement).getStatements().stream().anyMatch(this::alwaysExits);
                case IF -> {
                    var branch = (IfTree) statement;
                    yield alwaysExits(branch.getThenStatement()) && alwaysExits(branch.getElseStatement());
                }
                default -> false;
            };
        }

        private void expand(ExpressionTree expression, String condition) {
            expression = unwrap(expression);
            if (expression instanceof ConditionalExpressionTree branch) {
                var text = unwrap(branch.getCondition()).toString();
                expand(branch.getTrueExpression(), combine(condition, text));
                expand(branch.getFalseExpression(), combine(condition, negate(text)));
            } else if (expression instanceof SwitchExpressionTree selection) {
                scanCases(selection.getExpression(), selection.getCases(), condition);
            } else if (expression != null) {
                outcomes.add(new Outcome(expression, condition));
            }
        }

        private static String negate(String condition) {
            return "!(" + condition + ")";
        }

        private static String combine(String previous, String next) {
            return previous.isEmpty() ? next : previous + " && " + next;
        }
    }

    private Declaration resolveClass(String name, Declaration scope) {
        if (name == null) return null;
        for (var declaration = scope; declaration != null; declaration = declaration.enclosing) {
            var nested = classes.get(declaration.qualifiedName() + "." + name);
            if (nested != null) return nested;
        }
        var direct = classes.get(name);
        if (direct != null) return direct;
        var packageName = unit.getPackageName();
        if (packageName != null && name.startsWith(packageName + ".")) {
            return classes.get(name.substring(packageName.toString().length() + 1));
        }
        return null;
    }

    private String typeName(Declaration declaration, String methodName) {
        return typeName(declaration, methodName, true);
    }

    private String typeName(Declaration declaration, String methodName, boolean reportUnresolved) {
        var method = declaration.method(methodName);
        if (method != null) {
            var scope = new MethodScope(declaration, method);
            var values = scope.outcomes().stream()
                    .map(outcome -> stringValue(scope.initializer(outcome.expression), declaration)).distinct().toList();
            if (values.size() == 1 && values.get(0) != null) return values.get(0);
            if (reportUnresolved && unresolvedTypeNames.add(method)) {
                diagnostic("warning", "dynamic_type_name", methodName +
                        " must return a static string to resolve the registered type; using " + declaration.tree.getSimpleName(),
                        span(method));
            }
        }
        return declaration.tree.getSimpleName().toString();
    }

    private String stringValue(ExpressionTree expression, Declaration owner) {
        Set<VariableTree> visited = new HashSet<>();
        expression = unwrap(expression);
        while (expression != null) {
            if (expression instanceof LiteralTree literal && literal.getValue() instanceof String value) return value;
            var field = owner.field(expression);
            if (field == null || !visited.add(field)) return null;
            expression = unwrap(field.getInitializer());
        }
        return null;
    }

    private boolean staticCall(MethodInvocationTree call, String owner, String name) {
        if (!callName(call).equals(name)) return false;
        if (receiver(call) != null) return simpleName(receiver(call).toString()).equals(owner);
        for (var importTree : unit.getImports()) {
            var imported = importTree.getQualifiedIdentifier().toString();
            if (importTree.isStatic() && (imported.endsWith("." + owner + "." + name) ||
                    imported.endsWith("." + owner + ".*"))) return true;
        }
        return false;
    }

    private String dexOwner(MethodInvocationTree call) {
        if (receiver(call) != null) return simpleName(receiver(call).toString());
        var owners = RESOURCE_KINDS.keySet().stream().filter(owner -> staticCall(call, owner, callName(call))).toList();
        return owners.size() == 1 ? owners.get(0) : "";
    }

    private static ExpressionTree annotationArgument(AnnotationTree annotation, String key) {
        for (var argument : annotation.getArguments()) {
            if (argument instanceof AssignmentTree assignment && assignment.getVariable().toString().equals(key)) {
                return assignment.getExpression();
            }
            if (!(argument instanceof AssignmentTree) && key.equals("value")) return argument;
        }
        return null;
    }

    private static String classLiteral(ExpressionTree expression) {
        return unwrap(expression) instanceof MemberSelectTree select && select.getIdentifier().contentEquals("class")
                ? select.getExpression().toString() : null;
    }

    private static ExpressionTree unwrap(ExpressionTree expression) {
        while (true) {
            if (expression instanceof ParenthesizedTree parentheses) expression = parentheses.getExpression();
            else if (expression instanceof TypeCastTree cast) expression = cast.getExpression();
            else return expression;
        }
    }

    private static String callName(MethodInvocationTree call) {
        var select = call.getMethodSelect();
        return select instanceof MemberSelectTree member ? member.getIdentifier().toString() : select.toString();
    }

    private static ExpressionTree receiver(MethodInvocationTree call) {
        return call.getMethodSelect() instanceof MemberSelectTree member ? unwrap(member.getExpression()) : null;
    }

    private static ExpressionTree argument(MethodInvocationTree call, int index) {
        return call.getArguments().size() <= index ? null : call.getArguments().get(index);
    }

    private static String rawType(Tree type) {
        if (type instanceof ParameterizedTypeTree parameterized) return parameterized.getType().toString();
        return type == null ? "" : type.toString();
    }

    private static String simpleName(String type) {
        return type.substring(type.lastIndexOf('.') + 1);
    }

    private static String attributeValueType(String type) {
        return switch (simpleName(type)) {
            case "String", "char", "Character" -> "string";
            case "Boolean", "boolean" -> "bool";
            case "Byte", "Short", "Integer", "Long", "byte", "short", "int", "long" -> "int64";
            case "Double", "Float", "double", "float", "BigDecimal" -> "double";
            case "Instant", "OffsetDateTime", "ZonedDateTime", "LocalDateTime", "Date" -> "datetime";
            case "String[]" -> "string-array";
            case "List", "Collection" -> "array";
            case "Map" -> "object";
            default -> type.endsWith("[]") ? "array" : "object";
        };
    }

    private static String indexType(String type) {
        return switch (type) {
            case "FULL_TEXT" -> "fulltext";
            case "KEYWORD_ARRAY" -> "keyword-array";
            case "KEYWORD", "INT", "DOUBLE", "BOOL", "DATETIME" -> type.toLowerCase(Locale.ROOT);
            default -> "unknown";
        };
    }

    private static void walk(Tree tree, Consumer<Tree> visit) {
        new TreeScanner<Void, Void>() {
            @Override public Void scan(Tree child, Void unused) {
                if (child != null) visit.accept(child);
                return super.scan(child, unused);
            }
            @Override public Void visitClass(ClassTree declaration, Void unused) { return null; }
        }.scan(tree, null);
    }

    private List<Map<String, Object>> directives(Tree tree) {
        var position = positions.getStartPosition(unit, tree);
        if (position < 0) return List.of();
        int lineEnd = (int) position;
        List<Map<String, Object>> result = new ArrayList<>();
        while (lineEnd > 0) {
            int previousStart = source.lastIndexOf('\n', lineEnd - 1) + 1;
            var line = source.substring(previousStart, lineEnd);
            var trimmed = line.strip();
            if (trimmed.endsWith("*/") && !trimmed.startsWith("//")) {
                int end = previousStart + line.lastIndexOf("*/");
                int start = source.lastIndexOf("/**", end);
                if (start < 0 || source.indexOf("*/", start + 2) != end) break;
                lineEnd = start;
                continue;
            }
            if (!trimmed.isEmpty()) {
                if (!trimmed.startsWith("//")) break;
                var text = trimmed.substring(2).stripLeading();
                if (text.startsWith("dex:")) {
                    int comment = previousStart + line.indexOf("//");
                    result.add(object("text", text, "span", span(comment, lineEnd)));
                }
            }
            lineEnd = previousStart - 1;
        }
        Collections.reverse(result);
        return result;
    }

    private String locationID(String prefix, Tree tree) {
        var span = span(tree);
        return span == null ? prefix : prefix + ":" + span.get("startLine") + ":" + span.get("startColumn");
    }

    private Map<String, Object> span(Tree tree) {
        return tree == null ? null : span(positions.getStartPosition(unit, tree), positions.getEndPosition(unit, tree));
    }

    private Map<String, Object> span(long start, long end) {
        if (start < 0 || unit.getLineMap() == null) return null;
        var lines = unit.getLineMap();
        end = Math.max(start, end);
        var startLine = lines.getLineNumber(start);
        var endLine = lines.getLineNumber(end);
        return object("startLine", startLine, "startColumn", byteColumn(lines.getStartPosition(startLine), start),
                "endLine", endLine, "endColumn", byteColumn(lines.getStartPosition(endLine), end));
    }

    private int byteColumn(long lineStart, long offset) {
        return 1 + source.substring((int) lineStart, (int) offset).getBytes(StandardCharsets.UTF_8).length;
    }

    private void node(String id, String kind, String name, Tree anchor, Object... properties) {
        var node = object("id", id, "kind", kind, "name", name, "span", span(anchor));
        node.putAll(object(properties));
        nodes.putIfAbsent(id, node);
    }

    private void edge(String kind, String from, String to, String label, Tree anchor, Object... properties) {
        var edge = object("kind", kind, "from", from, "to", to, "label", label, "span", span(anchor));
        edge.putAll(object(properties));
        edges.add(edge);
    }

    private void diagnostic(String severity, String code, String message, Map<String, Object> span) {
        diagnostics.add(object("severity", severity, "code", code, "message", message, "span", span));
    }

    private static Map<String, Object> object(Object... pairs) {
        Map<String, Object> object = new LinkedHashMap<>();
        for (int index = 0; index < pairs.length; index += 2) {
            if (pairs[index + 1] != null) object.put((String) pairs[index], pairs[index + 1]);
        }
        return object;
    }

    private static String json(Object value) {
        if (value instanceof Map<?, ?> map) {
            var entries = new StringJoiner(",", "{", "}");
            map.forEach((key, item) -> entries.add(json(key) + ":" + json(item)));
            return entries.toString();
        }
        if (value instanceof Iterable<?> iterable) {
            var entries = new StringJoiner(",", "[", "]");
            iterable.forEach(item -> entries.add(json(item)));
            return entries.toString();
        }
        if (!(value instanceof String text)) return String.valueOf(value);
        var escaped = new StringBuilder("\"");
        for (int index = 0; index < text.length(); index++) {
            char character = text.charAt(index);
            if (character == '"' || character == '\\') escaped.append('\\').append(character);
            else if (character < 32) escaped.append(String.format(Locale.ROOT, "\\u%04x", (int) character));
            else escaped.append(character);
        }
        return escaped.append('"').toString();
    }

    private static final class JsonStrings {
        private final String input;
        private int offset;

        JsonStrings(String input) { this.input = input; }

        Map<String, String> readObject() {
            Map<String, String> values = new HashMap<>();
            expect('{');
            if (!consume('}')) {
                do {
                    var key = readString();
                    expect(':');
                    values.put(key, readString());
                } while (consume(','));
                expect('}');
            }
            return values;
        }

        private String readString() {
            expect('"');
            var value = new StringBuilder();
            while (offset < input.length()) {
                var character = input.charAt(offset++);
                if (character == '"') return value.toString();
                if (character == '\\') {
                    character = switch (input.charAt(offset++)) {
                        case '"', '\\', '/' -> input.charAt(offset - 1);
                        case 'b' -> '\b';
                        case 'f' -> '\f';
                        case 'n' -> '\n';
                        case 'r' -> '\r';
                        case 't' -> '\t';
                        case 'u' -> readUnicode();
                        default -> throw new IllegalArgumentException("Invalid JSON escape");
                    };
                }
                value.append(character);
            }
            throw new IllegalArgumentException("Unterminated JSON string");
        }

        private char readUnicode() {
            var character = (char) Integer.parseInt(input.substring(offset, offset + 4), 16);
            offset += 4;
            return character;
        }

        private void expect(char character) {
            if (!consume(character)) throw new IllegalArgumentException("Invalid Java analyzer request");
        }

        private boolean consume(char character) {
            while (offset < input.length() && Character.isWhitespace(input.charAt(offset))) offset++;
            if (offset >= input.length() || input.charAt(offset) != character) return false;
            offset++;
            return true;
        }
    }

    private record Resource(String id, String key, String kind, boolean isMap, String valueType,
                            VariableTree declaration, Map<String, Object> attribute) {}
}
