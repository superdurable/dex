# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import ast
import json
import sys


class Analyzer:
    def __init__(self, path, source):
        self.path = path
        self.source = source
        self.graph = {
            "schemaVersion": "1.0",
            "valid": True,
            "source": {"language": "python", "path": path},
            "flow": {"name": ""},
            "nodes": [],
            "edges": [],
            "diagnostics": [],
        }
        self.imports = {}
        self.module_aliases = set()
        self.classes = {}
        self.functions = {}
        self.step_classes = {}
        self.flow_classes = {}
        self.flow_fields = {}
        self.registered = {}
        self.resources = {}
        self.step_resources = {}
        self.step_options = {}
        self.failure_transitions = set()
        self.node_ids = set()

    def analyze(self):
        if sys.version_info < (3, 11):
            self.error("python_version", "Python analysis requires Python 3.11 or newer")
            return self.graph
        try:
            self.tree = ast.parse(self.source, filename=self.path, type_comments=True)
        except SyntaxError as error:
            self.error("python_parse_failed", error.msg, self.syntax_span(error))
            return self.graph
        self.index_imports()
        self.index_classes()
        self.validate_dynamic_constructs()
        self.index_resources()
        if len(self.flow_classes) != 1:
            code = "flow_not_found" if not self.flow_classes else "multiple_flows"
            message = "source must define exactly one Flow"
            self.error(code, message)
            for name, node in self.flow_classes.items():
                self.add_node({"id": f"unknown:flow:{name}", "kind": "unknown", "name": name, "span": self.span(node)})
            return self.graph
        flow_name, flow_class = next(iter(self.flow_classes.items()))
        self.graph["flow"] = {"name": self.custom_type_name(flow_class, "get_flow_type", flow_name), "span": self.span(flow_class)}
        self.index_flow_fields(flow_class)
        self.analyze_registration(flow_class)
        for step_name, node_id in list(self.registered.items()):
            self.analyze_step(step_name, node_id)
        self.analyze_flow_handlers(flow_class)
        return self.graph

    def index_imports(self):
        for node in self.tree.body:
            if isinstance(node, ast.ImportFrom) and node.module == "dex":
                for imported in node.names:
                    if imported.name == "*":
                        self.error("wildcard_import", "from dex import * is not supported", self.span(node))
                        continue
                    self.imports[imported.asname or imported.name] = imported.name
            elif isinstance(node, ast.Import):
                for imported in node.names:
                    if imported.name == "dex":
                        self.module_aliases.add(imported.asname or imported.name)

    def index_classes(self):
        for node in self.tree.body:
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                self.functions[node.name] = node
                continue
            if not isinstance(node, ast.ClassDef):
                continue
            self.classes[node.name] = node
            if any(self.is_dex_reference(base, "Step") for base in node.bases):
                self.step_classes[node.name] = node
            if any(self.is_dex_reference(base, "Flow") for base in node.bases):
                self.flow_classes[node.name] = node

    def validate_dynamic_constructs(self):
        imported_names = set(self.imports)
        for node in ast.walk(self.tree):
            if isinstance(node, (ast.Assign, ast.AnnAssign, ast.AugAssign)):
                targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                for target in targets:
                    if isinstance(target, ast.Name) and target.id in imported_names:
                        self.error("dex_alias_rebinding", f"Dex import alias {target.id} must not be rebound", self.span(target))
            if isinstance(node, ast.Call) and self.symbol(node.func) in {"getattr", "setattr", "__import__", "import_module"}:
                self.error("dynamic_python", f"{self.symbol(node.func)} is not supported in Flow source", self.span(node))
                self.add_node({"id": f"unknown:python:{node.lineno}", "kind": "unknown", "name": "Dynamic Python", "span": self.span(node)})
            if isinstance(node, ast.ClassDef) and any(keyword.arg == "metaclass" for keyword in node.keywords):
                self.error("dynamic_class", f"class {node.name} uses a metaclass", self.span(node))

    def index_resources(self):
        self.index_resource_statements(self.tree.body, "")
        for flow_name, flow_class in self.flow_classes.items():
            self.index_resource_statements(flow_class.body, flow_name)

    def index_resource_statements(self, statements, owner):
        for statement in statements:
            target = None
            value = None
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1:
                target, value = statement.targets[0], statement.value
            elif isinstance(statement, ast.AnnAssign):
                target, value = statement.target, statement.value
            if not isinstance(target, ast.Name) or not isinstance(value, ast.Call):
                continue
            kind = self.resource_kind(self.symbol(value.func))
            if not kind or not self.is_dex_reference(value.func, self.symbol(value.func).rsplit(".", 1)[-1]):
                continue
            resource_name = target.id
            if value.args:
                if isinstance(value.args[0], ast.Constant) and isinstance(value.args[0].value, str):
                    resource_name = value.args[0].value
                elif isinstance(value.args[0], ast.Name):
                    constant = self.module_constant(value.args[0].id)
                    if constant is None and owner:
                        constant = self.class_constant(owner, value.args[0].id)
                    if constant is not None:
                        resource_name = constant
                    else:
                        self.error("dynamic_resource_name", f"resource {target.id} must use a static name", self.span(value.args[0]))
                else:
                    self.error("dynamic_resource_name", f"resource {target.id} must use a static name", self.span(value.args[0]))
            key = f"{owner}.{target.id}" if owner else target.id
            node_id = f"resource:{kind}:{key}"
            self.resources[key] = node_id
            self.resources[target.id] = node_id
            resource = self.resource_details(value)
            if resource["valueType"] == "unknown":
                self.warning("unknown_resource_type", f"{kind} {resource_name} has no statically readable value type", self.span(value))
            self.add_node({"id": node_id, "kind": kind, "name": resource_name, "span": self.span(statement), "resource": resource})

    def index_flow_fields(self, flow_class):
        local_resources = {}
        for statement in flow_class.body:
            target = None
            value = None
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1:
                target, value = statement.targets[0], statement.value
            elif isinstance(statement, ast.AnnAssign):
                target, value = statement.target, statement.value
            if isinstance(target, ast.Name):
                resource_id = self.flow_resource_for(value, flow_class.name, local_resources)
                if resource_id:
                    self.resources[f"{flow_class.name}.{target.id}"] = resource_id
                    self.resources[target.id] = resource_id
                    local_resources[target.id] = resource_id
                    continue
            if not isinstance(target, ast.Name) or not isinstance(value, ast.Call):
                continue
            class_name = self.symbol(value.func)
            if class_name in self.step_classes:
                self.flow_fields[target.id] = class_name
                self.index_step_resource_wiring(class_name, value, flow_class.name, local_resources)

        initializer = self.method(flow_class, "__init__")
        if initializer is None:
            return
        local_values = {}
        for statement in initializer.body:
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1 and isinstance(statement.targets[0], ast.Name):
                local_values[statement.targets[0].id] = statement.value
            elif isinstance(statement, ast.AnnAssign) and isinstance(statement.target, ast.Name):
                local_values[statement.target.id] = statement.value
        for statement in initializer.body:
            target = None
            value = None
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1:
                target, value = statement.targets[0], statement.value
            elif isinstance(statement, ast.AnnAssign):
                target, value = statement.target, statement.value
            if not isinstance(value, ast.Call):
                continue
            kind = self.resource_kind(self.symbol(value.func))
            if not kind or not self.is_dex_reference(value.func, self.symbol(value.func).rsplit(".", 1)[-1]):
                continue
            target_name = target.id if isinstance(target, ast.Name) else self.target_attribute(target)
            if not target_name:
                continue
            node_id = self.add_resource(kind, target_name, value, flow_class.name, statement)
            local_resources[target_name] = node_id
        for node in ast.walk(initializer):
            if not isinstance(node, (ast.Assign, ast.AnnAssign)):
                continue
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            value = node.value
            if not isinstance(value, ast.Call):
                continue
            target_name = self.target_attribute(targets[0]) if targets else None
            class_name = self.symbol(value.func)
            if target_name and class_name in self.step_classes:
                self.flow_fields[target_name] = class_name
                self.index_step_resource_wiring(class_name, value, flow_class.name, local_resources)
                self.index_step_option_wiring(class_name, value, local_values)

    def index_step_resource_wiring(self, step_name, constructor, flow_name, local_resources):
        initializer = self.method(self.step_classes[step_name], "__init__")
        if initializer is None:
            return
        parameters = [argument.arg for argument in initializer.args.args if argument.arg != "self"]
        arguments = {}
        for index, argument in enumerate(constructor.args):
            if index < len(parameters):
                arguments[parameters[index]] = argument
        for keyword in constructor.keywords:
            if keyword.arg:
                arguments[keyword.arg] = keyword.value
        parameter_resources = {}
        for parameter, argument in arguments.items():
            resource_id = self.flow_resource_for(argument, flow_name, local_resources)
            if resource_id:
                parameter_resources[parameter] = resource_id
        for node in ast.walk(initializer):
            if not isinstance(node, (ast.Assign, ast.AnnAssign)):
                continue
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            if not targets or not isinstance(node.value, ast.Name):
                continue
            field = self.target_attribute(targets[0])
            resource_id = parameter_resources.get(node.value.id)
            if field and resource_id:
                self.step_resources.setdefault(step_name, {})[field] = resource_id

    def index_step_option_wiring(self, step_name, constructor, local_values):
        step_class = self.step_classes[step_name]
        options_method = self.method(step_class, "get_step_options")
        initializer = self.method(step_class, "__init__")
        if options_method is None or initializer is None:
            return
        returned_fields = {
            statement.value.attr
            for statement in options_method.body
            if isinstance(statement, ast.Return)
            and isinstance(statement.value, ast.Attribute)
            and isinstance(statement.value.value, ast.Name)
            and statement.value.value.id == "self"
        }
        if not returned_fields:
            return
        field_parameters = {}
        for statement in ast.walk(initializer):
            if not isinstance(statement, (ast.Assign, ast.AnnAssign)):
                continue
            targets = statement.targets if isinstance(statement, ast.Assign) else [statement.target]
            if not targets or not isinstance(statement.value, ast.Name):
                continue
            field = self.target_attribute(targets[0])
            if field in returned_fields:
                field_parameters[field] = statement.value.id
        parameters = [argument.arg for argument in initializer.args.args if argument.arg != "self"]
        arguments = {}
        for index, argument in enumerate(constructor.args):
            if index < len(parameters):
                arguments[parameters[index]] = argument
        for keyword in constructor.keywords:
            if keyword.arg:
                arguments[keyword.arg] = keyword.value
        for parameter in field_parameters.values():
            expression = arguments.get(parameter)
            if expression is None:
                continue
            expression = self.resolve_local_value(expression, local_values)
            self.step_options.setdefault(step_name, []).append(expression)

    def resolve_local_value(self, expression, local_values):
        seen = set()
        while isinstance(expression, ast.Name) and expression.id in local_values and expression.id not in seen:
            seen.add(expression.id)
            expression = local_values[expression.id]
        return expression

    def flow_resource_for(self, expression, flow_name, local_resources):
        if isinstance(expression, ast.Name):
            return local_resources.get(expression.id) or self.resources.get(expression.id)
        if isinstance(expression, ast.Attribute):
            return self.resources.get(f"{flow_name}.{expression.attr}") or self.resources.get(expression.attr)
        return None

    def add_resource(self, kind, target_name, call, owner, source_node):
        resource_name = target_name
        if call.args:
            if isinstance(call.args[0], ast.Constant) and isinstance(call.args[0].value, str):
                resource_name = call.args[0].value
            elif isinstance(call.args[0], ast.Name):
                constant = self.module_constant(call.args[0].id)
                if constant is None and owner:
                    constant = self.class_constant(owner, call.args[0].id)
                if constant is not None:
                    resource_name = constant
                else:
                    self.error("dynamic_resource_name", f"resource {target_name} must use a static name", self.span(call.args[0]))
            else:
                self.error("dynamic_resource_name", f"resource {target_name} must use a static name", self.span(call.args[0]))
        key = f"{owner}.{target_name}" if owner else target_name
        node_id = f"resource:{kind}:{key}"
        self.resources[key] = node_id
        self.resources[target_name] = node_id
        resource = self.resource_details(call)
        if resource["valueType"] == "unknown":
            self.warning("unknown_resource_type", f"{kind} {resource_name} has no statically readable value type", self.span(call))
        self.add_node({"id": node_id, "kind": kind, "name": resource_name, "span": self.span(source_node), "resource": resource})
        return node_id

    def analyze_registration(self, flow_class):
        method = self.method(flow_class, "get_steps")
        if method is None:
            self.error("step_registration_missing", "Flow must define get_steps in the source file", self.span(flow_class))
            return
        registrations = []
        for node in ast.walk(method):
            if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Attribute):
                continue
            if node.func.attr not in {"start_step", "other_steps"}:
                continue
            for index, argument in enumerate(node.args):
                step_name = self.step_target(argument)
                is_start = node.func.attr == "start_step" and index == 0
                registrations.append((step_name, is_start, argument))
        starts = 0
        seen = set()
        for step_name, is_start, expression in registrations:
            if not step_name:
                self.dynamic_target("registered Step type must be static", expression)
                continue
            if step_name in seen:
                continue
            seen.add(step_name)
            if step_name not in self.step_classes:
                self.dynamic_target(f"Step {step_name} must be defined in the source file", expression)
                continue
            step_class = self.step_classes[step_name]
            node_id = f"step:{step_name}"
            self.registered[step_name] = node_id
            self.add_node({
                "id": node_id,
                "kind": "step",
                "name": self.custom_type_name(step_class, "get_step_type", step_name),
                "phase": "wait_for+execute" if self.method(step_class, "wait_for") else "execute",
                "start": is_start,
                "span": self.span(expression),
            })
            if is_start:
                starts += 1
                self.graph["flow"]["startStepId"] = node_id
        if starts > 1:
            self.error("multiple_start_steps", "Flow defines more than one start Step", self.span(method))
        if not registrations and not self.has_static_empty_registration(method):
            self.error("dynamic_step_registration", "get_steps must directly call StepList.start_step or other_steps", self.span(method))

    def has_static_empty_registration(self, method):
        if len(method.body) != 1 or not isinstance(method.body[0], ast.Return):
            return False
        expression = method.body[0].value
        return (
            isinstance(expression, ast.Call)
            and isinstance(expression.func, ast.Attribute)
            and expression.func.attr == "empty"
            and self.is_dex_reference(expression.func.value, "StepList")
            and not expression.args
            and not expression.keywords
        )

    def analyze_step(self, step_name, node_id):
        step_class = self.step_classes[step_name]
        execute = self.method(step_class, "execute")
        if execute is None:
            self.error("step_handler_not_in_file", f"Step {step_name} execute must be defined in the source file", self.span(step_class))
            return
        self.analyze_decisions(node_id, execute, rpc=False)
        self.analyze_resources(node_id, execute, "execute")
        wait_for = self.method(step_class, "wait_for")
        if wait_for is not None:
            self.analyze_wait(node_id, step_name, wait_for)
        options = self.method(step_class, "get_step_options")
        if options is not None:
            self.analyze_step_options(node_id, options)
        for expression in self.step_options.get(step_name, []):
            self.analyze_step_options(node_id, expression)

    def analyze_step_options(self, owner_id, expression):
        for source in self.option_sources(expression):
            self.analyze_failure(owner_id, source)
            self.analyze_locks(owner_id, ast.walk(source), {
                "wait_for_lock_attributes": "wait_for",
                "execute_lock_attributes": "execute",
            })

    def option_sources(self, expression):
        sources = []
        pending = [expression]
        seen_functions = set()
        while pending:
            source = pending.pop()
            sources.append(source)
            for call in [node for node in ast.walk(source) if isinstance(node, ast.Call)]:
                function_name = self.symbol(call.func)
                function = self.functions.get(function_name)
                if function is None or function_name in seen_functions:
                    continue
                seen_functions.add(function_name)
                pending.append(function)
        return sources

    def analyze_flow_handlers(self, flow_class):
        ignored = {"__init__", "get_steps", "get_persistence_schema", "get_flow_type", "get_flow_options", "get_flow_config"}
        for statement in flow_class.body:
            if not isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef)) or statement.name in ignored:
                continue
            if statement.name == "handle_timeout":
                node_id = f"timeout_handler:{flow_class.name}"
                self.add_node({"id": node_id, "kind": "timeout_handler", "name": "handleTimeout", "span": self.span(statement)})
                self.analyze_decisions(node_id, statement, rpc=False)
                self.analyze_resources(node_id, statement, "timeout")
            elif any(self.is_dex_reference(decorator.func if isinstance(decorator, ast.Call) else decorator, "rpc") for decorator in statement.decorator_list):
                node_id = f"rpc:{statement.name}"
                self.add_node({"id": node_id, "kind": "rpc", "name": statement.name, "span": self.span(statement)})
                self.analyze_locks(node_id, statement.decorator_list, {"lock_attributes": "rpc"})
                self.analyze_decisions(node_id, statement, rpc=True)
                self.analyze_resources(node_id, statement, "rpc")
                self.analyze_rpc_state_loads(node_id, statement.decorator_list)

    def analyze_rpc_state_loads(self, owner_id, decorators):
        option_methods = {
            "load_attribute_map_instances": {"load"},
            "load_channel_map_instances": {"load_messages"},
        }
        direct_options = {"load_attribute_maps", "load_channels", "load_channel_maps"}
        for decorator in decorators:
            if not isinstance(decorator, ast.Call) or not self.is_dex_reference(decorator.func, "rpc"):
                continue
            for keyword in decorator.keywords:
                if keyword.arg in direct_options:
                    selections = keyword.value.elts if isinstance(keyword.value, (ast.List, ast.Set, ast.Tuple)) else [keyword.value]
                    for selection in selections:
                        self.add_rpc_state_read(owner_id, selection, keyword.arg)
                    continue
                methods = option_methods.get(keyword.arg)
                if methods is None:
                    continue
                selections = keyword.value.elts if isinstance(keyword.value, (ast.List, ast.Set, ast.Tuple)) else [keyword.value]
                for selection in selections:
                    if not isinstance(selection, ast.Call) or not isinstance(selection.func, ast.Attribute) or selection.func.attr not in methods:
                        continue
                    self.add_rpc_state_read(owner_id, selection.func.value, selection.func.attr)

    def add_rpc_state_read(self, owner_id, selection, label):
        resource_id = self.resource_for(selection, owner_id)
        if not resource_id:
            return
        if any(edge["kind"] == "resource_read" and edge["from"] == resource_id and edge["to"] == owner_id for edge in self.graph["edges"]):
            return
        self.add_edge(
            "resource_read",
            resource_id,
            owner_id,
            label=label,
            span=self.span(selection),
            metadata={"phase": "rpc"},
        )

    def analyze_locks(self, owner_id, expressions, phases):
        seen = set()
        for expression in expressions:
            if not isinstance(expression, ast.Call):
                continue
            for keyword in expression.keywords:
                phase = phases.get(keyword.arg)
                if not phase:
                    continue
                locks = keyword.value.elts if isinstance(keyword.value, (ast.List, ast.Set, ast.Tuple)) else [keyword.value]
                for lock in locks:
                    if not isinstance(lock, ast.Call) or not isinstance(lock.func, ast.Attribute) or lock.func.attr != "lock":
                        continue
                    resource_id = self.resource_for(lock.func.value, owner_id)
                    key = (resource_id, phase)
                    if not resource_id or key in seen:
                        continue
                    seen.add(key)
                    self.add_edge(
                        "resource_lock",
                        owner_id,
                        resource_id,
                        label="lock",
                        span=self.span(lock),
                        metadata={"phase": phase},
                    )

    def analyze_decisions(self, owner_id, method, rpc):
        locals_map = self.local_movements(method)
        outcomes = []

        def collect_decisions(expression, condition):
            found = self.decision_outcomes(expression, condition, rpc, locals_map)
            outcomes.extend(found)
            return bool(found)

        self.walk_statements(method.body, "", collect_decisions)
        if any(outcome.get("condition") for outcome in outcomes) and len(outcomes) > 1:
            for outcome in outcomes:
                if not outcome.get("condition"):
                    outcome["condition"] = "otherwise"
        if not outcomes and self.has_hidden_decision(method):
            node_id = f"unknown:{owner_id}:{method.lineno}"
            self.add_node({"id": node_id, "kind": "unknown", "name": "Dynamic Dex decision", "span": self.span(method)})
            self.add_edge("transition", owner_id, node_id, label="dynamic", span=self.span(method))
            self.error("hidden_dex_decision", f"{method.name} hides its Dex decision in a helper", self.span(method))
            return
        if len(outcomes) > 1:
            self.add_node({
                "id": f"decision-dispatch:{owner_id}", "kind": "decision_dispatch", "name": "Decision",
                "parentId": owner_id, "phase": "rpc" if rpc else "execute", "span": self.span(method),
            })
        for outcome in outcomes:
            source_span = outcome["span"]
            decision_id = f"decision:{owner_id}:{source_span['startLine']}:{source_span['startColumn']}"
            details = {
                "type": outcome["decisionType"],
                "checkedChannels": outcome.get("checkedChannels", []),
                "cancellations": [],
            }
            for transition in outcome["transitions"]:
                if transition["kind"] != "cancel":
                    continue
                target_id = self.resolve_target(transition["target"], transition["span"])
                scope = "siblings" if transition.get("label") == "with_canceling_sibling_steps" else "all"
                details["cancellations"].append({"stepId": target_id, "scope": scope})
            self.add_node({
                "id": decision_id, "kind": "decision", "name": outcome["decisionType"],
                "parentId": owner_id, "condition": outcome.get("condition", ""),
                "phase": "rpc" if rpc else "execute", "span": source_span, "decision": details,
            })
            for transition in outcome["transitions"]:
                if transition["kind"] == "terminal":
                    continue
                target_id = self.resolve_target(transition["target"], transition["span"])
                self.add_edge(
                    transition["kind"], decision_id, target_id,
                    label=transition.get("label", ""), multiplicity=transition.get("multiplicity", ""),
                    span=transition.get("span"), metadata=transition.get("metadata"),
                )

    def analyze_wait(self, owner_id, step_name, method):
        outcomes = []

        def collect_waits(expression, condition):
            found = self.wait_outcomes(expression, condition)
            outcomes.extend(found)
            return bool(found)

        self.walk_statements(method.body, "", collect_waits)
        if any(outcome.get("condition") for outcome in outcomes) and len(outcomes) > 1:
            for outcome in outcomes:
                if not outcome.get("condition"):
                    outcome["condition"] = "otherwise"
        if not outcomes:
            if self.raises_wait_failure(method):
                source_span = self.span(method)
                wait_id = f"wait:{step_name}:{source_span['startLine']}:{source_span['startColumn']}:0"
                self.add_node({
                    "id": wait_id,
                    "kind": "wait",
                    "name": "failure",
                    "parentId": owner_id,
                    "phase": "wait_for",
                    "span": source_span,
                    "wait": {"type": "failure", "conditions": []},
                })
                self.analyze_resources(owner_id, method, "wait_for")
                return
            node_id = f"unknown:wait:{step_name}:{method.lineno}"
            self.add_node({"id": node_id, "kind": "unknown", "name": "Dynamic WaitFor", "parentId": owner_id, "span": self.span(method)})
            self.error("hidden_dex_wait", f"{method.name} hides its Dex Wait in a helper", self.span(method))
            return
        if len(outcomes) > 1:
            self.add_node({
                "id": f"wait-dispatch:{step_name}", "kind": "wait_dispatch", "name": "WaitFor",
                "parentId": owner_id, "phase": "wait_for", "span": self.span(method),
            })
        for index, outcome in enumerate(outcomes):
            call = outcome["call"]
            source_span = self.span(call)
            wait_id = f"wait:{step_name}:{source_span['startLine']}:{source_span['startColumn']}:{index}"
            wait_type = self.wait_type(call)
            conditions = self.wait_conditions(owner_id, wait_id, call, outcome.get("resourceOverride"))
            self.add_node({
                "id": wait_id, "kind": "wait", "name": wait_type, "parentId": owner_id,
                "condition": outcome.get("condition", ""), "phase": "wait_for", "span": source_span,
                "wait": {"type": wait_type, "conditions": conditions},
            })
        self.analyze_resources(owner_id, method, "wait_for")

    def raises_wait_failure(self, method):
        has_raise = False
        for node in ast.walk(method):
            if isinstance(node, ast.Return):
                return False
            if isinstance(node, ast.Raise):
                has_raise = True
        return has_raise

    def decision_outcomes(self, expression, condition, rpc, locals_map):
        if isinstance(expression, ast.IfExp):
            branch = self.unparse(expression.test)
            return (
                self.decision_outcomes(expression.body, self.combine(condition, branch), rpc, locals_map)
                + self.decision_outcomes(expression.orelse, self.combine(condition, self.negate(branch)), rpc, locals_map)
            )
        decision_type = self.decision_type(expression, rpc)
        if not decision_type:
            return []
        return [{
            "decisionType": decision_type,
            "condition": condition,
            "transitions": self.parse_decision(expression, condition, rpc, locals_map),
            "checkedChannels": self.checked_channels(expression),
            "span": self.span(expression),
        }]

    def decision_type(self, expression, rpc):
        if not isinstance(expression, ast.Call):
            return None
        if isinstance(expression.func, ast.Attribute) and expression.func.attr in {"with_canceling_steps", "with_canceling_sibling_steps"}:
            return self.decision_type(expression.func.value, rpc)
        short = self.symbol(expression.func).rsplit(".", 1)[-1]
        names = {
            "go_to": "goTo",
            "go_to_many": "goToMany",
            "graceful_complete": "gracefulComplete",
            "force_complete": "forceComplete",
            "force_fail": "forceFail",
            "dead_end": "deadEnd",
            "force_complete_if_channels_empty": "forceCompleteIfChannelsEmpty",
        }
        if short in names and self.is_dex_reference(expression.func, short):
            return names[short]
        if rpc and short == "RPCResult" and self.is_dex_reference(expression.func, "RPCResult"):
            return "rpcResult"
        return None

    def checked_channels(self, expression):
        if not isinstance(expression, ast.Call):
            return []
        if isinstance(expression.func, ast.Attribute) and expression.func.attr in {"with_canceling_steps", "with_canceling_sibling_steps"}:
            return self.checked_channels(expression.func.value)
        if self.symbol(expression.func).rsplit(".", 1)[-1] != "force_complete_if_channels_empty":
            return []
        result = []
        for argument in expression.args[2:]:
            resource_id = self.resource_for(argument, "")
            if resource_id and resource_id.startswith("resource:channel:") and resource_id not in result:
                result.append(resource_id)
        return result

    def wait_outcomes(self, expression, condition):
        if isinstance(expression, ast.IfExp):
            branch = self.unparse(expression.test)
            return (
                self.wait_outcomes(expression.body, self.combine(condition, branch))
                + self.wait_outcomes(expression.orelse, self.combine(condition, self.negate(branch)))
            )
        if not isinstance(expression, ast.Call) or not self.wait_type(expression):
            return []
        conditional_resource = self.conditional_wait_resource(expression)
        if conditional_resource is None:
            return [{"call": expression, "condition": condition}]
        return [
            {
                "call": expression,
                "condition": self.combine(condition, self.unparse(conditional_resource.test)),
                "resourceOverride": conditional_resource.body,
            },
            {
                "call": expression,
                "condition": self.combine(condition, self.negate(self.unparse(conditional_resource.test))),
                "resourceOverride": conditional_resource.orelse,
            },
        ]

    def wait_type(self, call):
        if not isinstance(call, ast.Call) or not isinstance(call.func, ast.Attribute) or not self.is_dex_reference(call.func.value, "Wait"):
            return None
        return {
            "skip_immediately": "skipWaitImmediately",
            "until": "until",
            "all_of": "allOf",
            "any_of": "anyOf",
            "any_combination_of": "anyComboOf",
        }.get(call.func.attr)

    def conditional_wait_resource(self, wait_call):
        for node in ast.walk(wait_call):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.IfExp):
                if node.func.attr in {"for_one", "for_n", "at_least", "at_most", "for_range"}:
                    return node.func.value
        return None

    def wait_conditions(self, owner_id, wait_id, wait_call, resource_override):
        conditions = []
        for call in [node for node in ast.walk(wait_call) if isinstance(node, ast.Call) and node is not wait_call]:
            if isinstance(call.func, ast.Attribute) and call.func.attr == "by_duration" and self.is_dex_reference(call.func.value, "Timer"):
                expression = self.unparse(call.args[0]) if call.args else "duration"
                conditions.append({
                    "kind": "timer", "label": f"{self.humanize_duration(call.args[0] if call.args else None)} timer",
                    "expression": expression, "span": self.span(call),
                })
                continue
            if isinstance(call.func, ast.Attribute) and call.func.attr == "run" and self.is_dex_reference(call.func.value, "SubFlow"):
                name = self.unparse(call.args[0]) if call.args else "SubFlow"
                subflow_id = f"subflow:{name}:{call.lineno}"
                self.add_node({"id": subflow_id, "kind": "subflow", "name": name, "external": True, "span": self.span(call)})
                self.add_edge("subflow", wait_id, subflow_id, label="start", span=self.span(call))
                conditions.append({"kind": "subflow", "label": name, "subFlowId": subflow_id, "span": self.span(call)})
                continue
            if not isinstance(call.func, ast.Attribute) or call.func.attr not in {"for_one", "for_n", "at_least", "at_most", "for_range"}:
                continue
            resource_expression = resource_override if isinstance(call.func.value, ast.IfExp) and resource_override is not None else call.func.value
            resource_id = self.resource_for(resource_expression, owner_id)
            if not resource_id or not resource_id.startswith("resource:channel:"):
                continue
            label, expression = self.channel_condition_label(resource_id, call)
            conditions.append({
                "kind": "channel", "label": label, "resourceId": resource_id,
                "expression": expression, "span": self.span(call),
            })
            self.add_edge("wait_condition", resource_id, wait_id, label=label, span=self.span(call))
        return conditions

    def channel_condition_label(self, resource_id, call):
        resource = next((node for node in self.graph["nodes"] if node["id"] == resource_id), {})
        name = resource.get("name", "Channel")
        is_map = resource.get("resource", {}).get("map", False)
        arguments = [self.unparse(argument) for argument in call.args]
        instance = ""
        counts = arguments
        if is_map and arguments:
            instance = f"[{arguments[0]}]"
            counts = arguments[1:]
        keywords = {keyword.arg: self.unparse(keyword.value) for keyword in call.keywords if keyword.arg}
        if call.func.attr == "for_one":
            suffix = "for 1"
        elif call.func.attr == "for_n":
            suffix = f"for {counts[0] if counts else 'N'}"
        elif call.func.attr == "at_least":
            suffix = f"at least {counts[0] if counts else 'N'}"
        elif call.func.attr == "at_most":
            suffix = f"at most {counts[0] if counts else 'N'}"
        else:
            lower = keywords.get("at_least", "0")
            upper = keywords.get("at_most", "∞")
            suffix = f"for {lower}…{upper}"
        expression = ", ".join(arguments + [f"{key}={value}" for key, value in keywords.items()])
        return f"{name}{instance}.{suffix}", expression

    def humanize_duration(self, expression):
        if isinstance(expression, ast.Call) and self.symbol(expression.func).rsplit(".", 1)[-1] == "timedelta":
            units = [("days", "day"), ("hours", "hour"), ("minutes", "minute"), ("seconds", "second"), ("milliseconds", "millisecond")]
            values = {keyword.arg: keyword.value for keyword in expression.keywords if keyword.arg}
            for keyword, label in units:
                value = values.get(keyword)
                if isinstance(value, ast.Constant) and isinstance(value.value, (int, float)):
                    suffix = label if value.value == 1 else label + "s"
                    return f"{value.value:g} {suffix}"
        return self.unparse(expression) or "duration"

    def analyze_failure(self, owner_id, method):
        for call in [node for node in ast.walk(method) if isinstance(node, ast.Call)]:
            if not isinstance(call.func, ast.Attribute) or call.func.attr != "on_execute_failure_proceed_to":
                continue
            if not call.args:
                self.dynamic_target("execute failure recovery target must be static", call)
                continue
            target = self.step_target(call.args[0])
            target_id = self.resolve_target(target, self.span(call.args[0]))
            transition = (owner_id, target_id)
            if transition in self.failure_transitions:
                continue
            self.failure_transitions.add(transition)
            metadata = {}
            if target in self.step_classes:
                metadata["skipWaitFor"] = self.method(self.step_classes[target], "wait_for") is None
            self.add_edge("failure_transition", owner_id, target_id, label="Execute failure", span=self.span(call), metadata=metadata)

    def analyze_resources(self, owner_id, method, phase):
        seen = set()
        local_resources = self.local_resource_aliases(owner_id, method)
        for call in [node for node in ast.walk(method) if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)]:
            resource_id = None
            if isinstance(call.func.value, ast.Name):
                resource_id = local_resources.get(call.func.value.id)
            resource_id = resource_id or self.resource_for(call.func.value, owner_id)
            if not resource_id:
                continue
            edge_kind = self.resource_edge_kind(call.func.attr, phase)
            if not edge_kind or edge_kind == "wait_condition":
                continue
            key = (edge_kind, resource_id, call.func.attr)
            if key in seen:
                continue
            seen.add(key)
            metadata = {"phase": phase}
            if resource_id.startswith("resource:stream:") and call.func.attr == "write":
                metadata.update({
                    "bestEffort": True,
                    "repeatable": True,
                    "role": "progress",
                })
                if phase in {"rpc", "timeout"}:
                    self.error(
                        "step_progress_outside_step",
                        "Stream.write is only available in wait_for and execute",
                        self.span(call),
                    )
            source = resource_id if edge_kind == "resource_read" else owner_id
            target = owner_id if edge_kind == "resource_read" else resource_id
            self.add_edge(edge_kind, source, target, label=call.func.attr, span=self.span(call), metadata=metadata)
        for reference in [node for node in ast.walk(method) if isinstance(node, ast.Attribute)]:
            if reference.attr != "write" or not isinstance(reference.value, ast.Name):
                continue
            resource_id = local_resources.get(reference.value.id)
            key = ("resource_write", resource_id, reference.attr)
            if not resource_id or not resource_id.startswith("resource:stream:") or key in seen:
                continue
            seen.add(key)
            metadata = {
                "phase": phase,
                "bestEffort": True,
                "repeatable": True,
                "role": "progress",
            }
            if phase in {"rpc", "timeout"}:
                self.error(
                    "step_progress_outside_step",
                    "Stream.write is only available in wait_for and execute",
                    self.span(reference),
                )
            self.add_edge(
                "resource_write",
                owner_id,
                resource_id,
                label=reference.attr,
                span=self.span(reference),
                metadata=metadata,
            )

    def local_resource_aliases(self, owner_id, method):
        aliases = {}
        for statement in ast.walk(method):
            target = None
            value = None
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1:
                target, value = statement.targets[0], statement.value
            elif isinstance(statement, ast.AnnAssign):
                target, value = statement.target, statement.value
            if not isinstance(target, ast.Name) or not isinstance(value, ast.Call):
                continue
            if not isinstance(value.func, ast.Attribute) or value.func.attr != "buffered_text":
                continue
            resource_id = self.resource_for(value.func.value, owner_id)
            if resource_id and resource_id.startswith("resource:stream:"):
                aliases[target.id] = resource_id
        return aliases

    def parse_decision(self, expression, condition, rpc, locals_map):
        if isinstance(expression, ast.IfExp):
            branch = self.unparse(expression.test)
            return self.parse_decision(expression.body, self.combine(condition, branch), rpc, locals_map) + self.parse_decision(expression.orelse, self.combine(condition, self.negate(branch)), rpc, locals_map)
        if not isinstance(expression, ast.Call):
            return []
        if isinstance(expression.func, ast.Attribute) and expression.func.attr in {"with_canceling_steps", "with_canceling_sibling_steps"}:
            transitions = self.parse_decision(expression.func.value, condition, rpc, locals_map)
            for argument in expression.args:
                transitions.append({"kind": "cancel", "target": self.step_target(argument), "label": expression.func.attr, "condition": condition, "span": self.span(argument)})
            return transitions
        symbol = self.symbol(expression.func)
        short = symbol.rsplit(".", 1)[-1]
        if short == "go_to" and self.is_dex_reference(expression.func, "go_to"):
            target = self.step_target(expression.args[0]) if expression.args else None
            return [{"kind": "transition", "target": target, "label": "go_to", "condition": condition, "span": self.span(expression)}]
        if short == "go_to_many" and self.is_dex_reference(expression.func, "go_to_many"):
            result = []
            for argument in expression.args:
                if isinstance(argument, ast.Starred) and isinstance(argument.value, ast.Name):
                    for movement in locals_map.get(argument.value.id, []):
                        result.append({**movement, "condition": condition, "multiplicity": movement.get("multiplicity") or "×N"})
                elif isinstance(argument, ast.Starred) and isinstance(argument.value, (ast.GeneratorExp, ast.ListComp)):
                    movement = self.parse_movement(argument.value.elt, condition)
                    if movement:
                        movement["multiplicity"] = "×N"
                        result.append(movement)
                elif isinstance(argument, ast.Name):
                    for movement in locals_map.get(argument.id, []):
                        result.append({**movement, "condition": condition})
                else:
                    movement = self.parse_movement(argument, condition)
                    if movement:
                        result.append(movement)
                    else:
                        self.dynamic_target("fan-out movements must be built directly in the handler", argument)
            if not expression.args:
                self.dynamic_target("go_to_many must contain a statically analyzable movement", expression)
            return result
        if short == "force_complete_if_channels_empty" and self.is_dex_reference(expression.func, short):
            movement = self.parse_movement(expression.args[1], condition) if len(expression.args) > 1 else None
            if movement:
                return [movement]
            return [{"kind": "terminal", "target": short, "label": "Complete if channels empty", "condition": condition, "span": self.span(expression)}]
        if short in {"graceful_complete", "force_complete", "force_fail", "dead_end", "force_complete_if_channels_empty"} and self.is_dex_reference(expression.func, short):
            labels = {
                "graceful_complete": "Graceful complete", "force_complete": "Force complete",
                "force_fail": "Force fail", "dead_end": "Dead end",
                "force_complete_if_channels_empty": "Complete if channels empty",
            }
            return [{"kind": "terminal", "target": short, "label": labels[short], "condition": condition, "span": self.span(expression)}]
        if rpc and short == "RPCResult" and self.is_dex_reference(expression.func, "RPCResult"):
            result = []
            for node in ast.walk(expression):
                movement = self.parse_movement(node, condition)
                if movement:
                    result.append(movement)
            return result
        if rpc:
            movement = self.parse_movement(expression, condition)
            return [movement] if movement else []
        return []

    def parse_movement(self, expression, condition):
        if not isinstance(expression, ast.Call) or not isinstance(expression.func, ast.Attribute) or expression.func.attr != "of" or not self.is_dex_reference(expression.func.value, "StepMovement"):
            return None
        if not expression.args:
            return None
        return {"kind": "transition", "target": self.step_target(expression.args[0]), "label": "fan-out", "condition": condition, "span": self.span(expression)}

    def local_movements(self, method):
        result = {}
        for node in ast.walk(method):
            if isinstance(node, (ast.Assign, ast.AnnAssign)):
                targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                value = node.value
                if not targets or not isinstance(targets[0], ast.Name):
                    continue
                name = targets[0].id
                if isinstance(value, (ast.List, ast.Tuple)):
                    result[name] = [movement for element in value.elts if (movement := self.parse_movement(element, ""))]
                elif isinstance(value, ast.ListComp):
                    movement = self.parse_movement(value.elt, "")
                    if movement:
                        movement["multiplicity"] = "×N"
                        result[name] = [movement]
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name) and node.args:
                if node.func.attr == "append":
                    movement = self.parse_movement(node.args[0], "")
                    if movement:
                        movement["multiplicity"] = "×N"
                        result.setdefault(node.func.value.id, []).append(movement)
                elif node.func.attr == "extend":
                    extension = node.args[0]
                    elements = extension.elts if isinstance(extension, (ast.List, ast.Tuple)) else [getattr(extension, "elt", None)]
                    for element in elements:
                        movement = self.parse_movement(element, "")
                        if movement:
                            movement["multiplicity"] = "×N"
                            result.setdefault(node.func.value.id, []).append(movement)
        return result

    def walk_statements(self, statements, condition, visit):
        has_outcome = False
        for statement in statements:
            if isinstance(statement, ast.Return) and statement.value is not None:
                has_outcome = visit(statement.value, condition) or has_outcome
            elif isinstance(statement, ast.If):
                branch = self.unparse(statement.test)
                body_has_outcome = self.walk_statements(statement.body, self.combine(condition, branch), visit)
                has_outcome = body_has_outcome or has_outcome
                if statement.orelse:
                    has_outcome = self.walk_statements(statement.orelse, self.combine(condition, self.negate(branch)), visit) or has_outcome
                elif body_has_outcome and self.statements_always_return(statement.body):
                    condition = self.combine(condition, self.negate(branch))
            elif isinstance(statement, ast.Match):
                for case in statement.cases:
                    has_outcome = self.walk_statements(case.body, self.combine(condition, self.unparse(case.pattern)), visit) or has_outcome
            elif isinstance(statement, (ast.For, ast.AsyncFor, ast.While, ast.With, ast.AsyncWith)):
                has_outcome = self.walk_statements(statement.body, condition, visit) or has_outcome
                has_outcome = self.walk_statements(getattr(statement, "orelse", []), condition, visit) or has_outcome
            elif isinstance(statement, ast.Try):
                has_outcome = self.walk_statements(statement.body, condition, visit) or has_outcome
                for handler in statement.handlers:
                    has_outcome = self.walk_statements(handler.body, self.combine(condition, "exception"), visit) or has_outcome
                has_outcome = self.walk_statements(statement.orelse, condition, visit) or has_outcome
                has_outcome = self.walk_statements(statement.finalbody, condition, visit) or has_outcome
        return has_outcome

    def statements_always_return(self, statements):
        if not statements:
            return False
        statement = statements[-1]
        if isinstance(statement, (ast.Return, ast.Raise)):
            return True
        if isinstance(statement, ast.If) and statement.orelse:
            return self.statements_always_return(statement.body) and self.statements_always_return(statement.orelse)
        return False

    def negate(self, condition):
        return f"not ({condition})"

    def resolve_target(self, target, span):
        if target in self.registered:
            return self.registered[target]
        target_name = target or "Dynamic Step"
        node_id = f"unknown:step:{target_name}"
        self.add_node({"id": node_id, "kind": "unknown", "name": target_name, "span": span})
        message = f"Step {target} is not registered in this Flow" if target else "Dex transition target must be a registered static Step"
        self.error("unknown_step_target", message, span)
        return node_id

    def dynamic_target(self, message, node):
        span = self.span(node)
        self.add_node({"id": f"unknown:step:{span['startLine']}", "kind": "unknown", "name": "Dynamic Step", "span": span})
        self.error("dynamic_step_target", message, span)

    def resource_for(self, expression, owner_id):
        if isinstance(expression, ast.Name):
            return self.resources.get(expression.id)
        if isinstance(expression, ast.Attribute):
            if isinstance(expression.value, ast.Name) and expression.value.id == "self":
                owner_step = owner_id.split(":", 1)[1] if ":" in owner_id else ""
                if owner_step.startswith("wait:"):
                    owner_step = owner_step.split(":", 1)[1]
                wired = self.step_resources.get(owner_step, {}).get(expression.attr)
                if wired:
                    return wired
                return self.resources.get(expression.attr) or self.resources.get(f"{next(iter(self.flow_classes), '')}.{expression.attr}")
            return self.resources.get(expression.attr)
        return None

    def step_target(self, expression):
        if isinstance(expression, ast.Name):
            return expression.id if expression.id in self.step_classes else None
        if isinstance(expression, ast.Attribute):
            if isinstance(expression.value, ast.Name) and expression.value.id == "self":
                return self.flow_fields.get(expression.attr)
            if expression.attr in self.flow_fields:
                return self.flow_fields[expression.attr]
            return expression.attr if expression.attr in self.step_classes else None
        if isinstance(expression, ast.Call):
            name = self.symbol(expression.func)
            return name if name in self.step_classes else None
        return None

    def custom_type_name(self, class_node, method_name, fallback):
        method = self.method(class_node, method_name)
        if method:
            for statement in method.body:
                if isinstance(statement, ast.Return) and isinstance(statement.value, ast.Constant) and isinstance(statement.value.value, str):
                    return statement.value.value or fallback
            self.error("dynamic_type_name", f"{method_name} must return a static string", self.span(method))
        return fallback

    def has_hidden_decision(self, method):
        annotation = self.unparse(method.returns) if method.returns else ""
        if "StepDecision" not in annotation:
            return False
        known = {"go_to", "go_to_many", "graceful_complete", "force_complete", "force_fail", "dead_end", "force_complete_if_channels_empty", "RPCResult"}
        for node in ast.walk(method):
            if isinstance(node, ast.Return) and isinstance(node.value, ast.Call):
                short = self.symbol(node.value.func).rsplit(".", 1)[-1]
                if short not in known or not self.is_dex_reference(node.value.func, short):
                    return True
        return False

    def target_attribute(self, expression):
        if isinstance(expression, ast.Attribute) and isinstance(expression.value, ast.Name) and expression.value.id == "self":
            return expression.attr
        return None

    def method(self, class_node, name):
        for statement in class_node.body:
            if isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef)) and statement.name == name:
                return statement
        return None

    def module_constant(self, name):
        for statement in self.tree.body:
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1 and isinstance(statement.targets[0], ast.Name) and statement.targets[0].id == name:
                if isinstance(statement.value, ast.Constant) and isinstance(statement.value.value, str):
                    return statement.value.value
        return None

    def class_constant(self, class_name, name):
        class_node = self.classes.get(class_name)
        if class_node is None:
            return None
        for statement in class_node.body:
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1 and isinstance(statement.targets[0], ast.Name) and statement.targets[0].id == name:
                if isinstance(statement.value, ast.Constant) and isinstance(statement.value.value, str):
                    return statement.value.value
        return None

    def symbol(self, expression):
        if isinstance(expression, ast.Name):
            return self.imports.get(expression.id, expression.id)
        if isinstance(expression, ast.Attribute):
            prefix = self.symbol(expression.value)
            if prefix in self.module_aliases:
                return expression.attr
            return f"{prefix}.{expression.attr}" if prefix else expression.attr
        if isinstance(expression, ast.Subscript):
            return self.symbol(expression.value)
        return ""

    def is_dex_reference(self, expression, expected):
        if isinstance(expression, ast.Subscript):
            return self.is_dex_reference(expression.value, expected)
        if isinstance(expression, ast.Name):
            return self.imports.get(expression.id) == expected
        if isinstance(expression, ast.Attribute) and isinstance(expression.value, ast.Name):
            return expression.value.id in self.module_aliases and expression.attr == expected
        return False

    def resource_kind(self, name):
        short = name.rsplit(".", 1)[-1]
        if short in {"Attribute", "AttributeMap"}:
            return "attribute"
        if short in {"Channel", "ChannelMap"}:
            return "channel"
        if short == "Stream":
            return "stream"
        return None

    def resource_details(self, call):
        short = self.symbol(call.func).rsplit(".", 1)[-1]
        value_type = "unknown"
        if len(call.args) > 1:
            value_type = self.unparse(call.args[1])
            if value_type == "type(None)":
                value_type = "None"
        elif isinstance(call.func, ast.Subscript):
            value_type = self.unparse(call.func.slice)
        return {"valueType": value_type or "unknown", "map": short in {"AttributeMap", "ChannelMap"}}

    def resource_edge_kind(self, method, phase):
        if phase == "wait_for" and method in {"for_one", "for_n", "at_least", "at_most", "for_range"}:
            return "wait_condition"
        if method in {"get", "size", "map_size", "all_instance_keys", "get_map_size", "get_all_instance_keys", "results", "pending_messages", "find_pending_message"}:
            return "resource_read"
        if method in {"set", "delete", "write"}:
            return "resource_write"
        if method == "publish":
            return "resource_publish"
        return None

    def add_node(self, node):
        if node["id"] in self.node_ids:
            return
        self.node_ids.add(node["id"])
        self.graph["nodes"].append(node)

    def add_edge(self, kind, source, target, label="", condition="", multiplicity="", span=None, metadata=None):
        edge = {"kind": kind, "from": source, "to": target}
        if label:
            edge["label"] = label
        if condition:
            edge["condition"] = condition
        if multiplicity:
            edge["multiplicity"] = multiplicity
        if span:
            edge["span"] = span
        if metadata:
            edge["metadata"] = metadata
        self.graph["edges"].append(edge)

    def error(self, code, message, span=None):
        diagnostic = {"severity": "error", "code": code, "message": message}
        if span:
            diagnostic["span"] = span
        self.graph["diagnostics"].append(diagnostic)
        self.graph["valid"] = False

    def warning(self, code, message, span=None):
        diagnostic = {"severity": "warning", "code": code, "message": message}
        if span:
            diagnostic["span"] = span
        self.graph["diagnostics"].append(diagnostic)

    def span(self, node):
        return {
            "startLine": getattr(node, "lineno", 1),
            "startColumn": getattr(node, "col_offset", 0) + 1,
            "endLine": getattr(node, "end_lineno", getattr(node, "lineno", 1)),
            "endColumn": getattr(node, "end_col_offset", getattr(node, "col_offset", 0)) + 1,
        }

    def syntax_span(self, error):
        return {
            "startLine": error.lineno or 1,
            "startColumn": error.offset or 1,
            "endLine": error.end_lineno or error.lineno or 1,
            "endColumn": error.end_offset or error.offset or 1,
        }

    def unparse(self, node):
        if node is None:
            return ""
        try:
            return ast.unparse(node)
        except Exception:
            return "condition"

    def combine(self, parent, child):
        return child if not parent else f"{parent} and {child}"


def main():
    request = json.load(sys.stdin)
    graph = Analyzer(request["path"], request["source"]).analyze()
    json.dump(graph, sys.stdout, ensure_ascii=False, separators=(",", ":"))


main()
