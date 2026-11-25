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
                "startInput", startInput, "persistenceResourceIds", visibleResources.keySet());
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
            if (entry.getValue() != null) analyzeStep(entry.getKey(), entry.getValue());
        }
        analyzeTimeout(declaration);
        analyzeRPCs(declaration);
        collectStartInput(declaration);
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
        } else {
            diagnostic("error", "step_handler_not_in_file", "Step " + nodes.get(id).get("name") +
                    " execute must be defined in the source file", span(step.tree));
        }
        var waitFor = step.method("waitFor");
        if (waitFor != null) analyzeWait(id, new MethodScope(step, waitFor));
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
        var bound = scope.parameterResources.get(variable);
        if (bound != null) return bound;
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
                if (helper != null) {
                    bindParameterResources(helper, call, scope);
                    analyzeAccess(owner, phase, helper, visited, accesses);
                }
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

    // A resource passed to a helper makes the helper's accesses through that parameter count for the caller.
    private void bindParameterResources(MethodScope helper, MethodInvocationTree call, MethodScope caller) {
        var parameters = helper.method.getParameters();
        if (parameters.size() != call.getArguments().size()) return;
        for (int index = 0; index < parameters.size(); index++) {
            var resource = resource(call.getArguments().get(index), caller);
            if (resource != null) helper.parameterResources.put(parameters.get(index), resource);
        }
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
                var instance = argument(lock, 1);
                var literal = stringValue(scope.initializer(instance), scope.owner);
                metadata.put("instance", literal == null ? instance.toString() : literal);
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
        final Map<VariableTree, Resource> parameterResources = new IdentityHashMap<>();

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
