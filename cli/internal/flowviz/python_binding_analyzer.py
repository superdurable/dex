# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import ast

# Each of these lets a class answer attribute reads, writes, or construction differently from its body.
ATTRIBUTE_INTERCEPTION_METHODS = {
    "__getattr__",
    "__getattribute__",
    "__setattr__",
    "__delattr__",
    "__get__",
    "__set__",
    "__delete__",
    "__set_name__",
    "__new__",
    "__init_subclass__",
    "__class_getitem__",
}

# The SDK and the analyzer call these Step and Flow methods, so a property must not replace one.
DEX_ENTRY_METHOD_NAMES = {
    "__init__",
    "execute",
    "wait_for",
    "get_step_type",
    "get_input_type",
    "get_step_options",
    "get_flow_type",
    "get_steps",
    "get_persistence_schema",
    "handle_timeout",
    "get_connector_trigger_bindings",
}


class EvaluationFrame:
    # The Step object that self names, and the constructor arguments bound while its __init__ runs.
    def __init__(self, instance=None, initializer=None, arguments=None):
        self.instance = instance
        self.initializer = initializer
        self.arguments = arguments or {}

    @property
    def identity(self):
        return self.instance.identity if self.instance else None


class StepInstance:
    # One Step object: a constructor call evaluated once in its frame.
    def __init__(self, declaration, call, frame):
        self.declaration = declaration
        self.call = call
        self.frame = frame
        self.identity = (call, frame.identity)


class StepBindingAnalyzer:
    def index_bindings(self):
        self.scope_bindings = {}
        self.declared_global_names = set()
        self.collect_scope_bindings(self.tree, self.tree.body, [])
        self.self_attribute_stores = {}
        self.foreign_attribute_stores = {}
        self.foreign_attribute_results = {}
        self.escaped_receiver_results = {}
        self.has_dynamic_attributes = False
        for node in ast.walk(self.tree):
            if isinstance(node, ast.Attribute):
                if node.attr in {"__dict__", "__setattr__", "__delattr__"}:
                    self.has_dynamic_attributes = True
                if not isinstance(node.ctx, (ast.Store, ast.Del)) or self.is_annotation_only_target(node):
                    continue
                if node.attr == "__class__":
                    self.has_dynamic_attributes = True
                # A store through a rebound or nested-function self lands on another object.
                owner = self.self_reference_class(node.value)
                if owner is None:
                    self.foreign_attribute_stores.setdefault(node.attr, []).append(node.value)
                else:
                    self.self_attribute_stores.setdefault(owner, {}).setdefault(node.attr, []).append(node)
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "vars":
                self.has_dynamic_attributes = True

    def is_foreign_attribute(self, name):
        # A store through another object poisons the attribute unless that object is provably a fresh literal.
        if name not in self.foreign_attribute_results:
            self.foreign_attribute_results[name] = True
            self.foreign_attribute_results[name] = any(
                not isinstance(self.resolved_value(owner, EvaluationFrame())[0], (ast.Dict, ast.List, ast.Set, ast.Tuple))
                for owner in self.foreign_attribute_stores.get(name, [])
            )
        return self.foreign_attribute_results[name]

    def collect_scope_bindings(self, scope, nodes, parameters):
        bindings = self.scope_bindings.setdefault(scope, {})
        for parameter in parameters:
            bindings.setdefault(parameter.arg, []).append(parameter)
        pending = list(nodes)
        while pending:
            node = pending.pop()
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                bindings.setdefault(node.name, []).append(node)
                pending.extend(node.decorator_list)
                if isinstance(node, ast.ClassDef):
                    pending.extend(node.bases)
                    pending.extend(keyword.value for keyword in node.keywords)
                    self.collect_scope_bindings(node, node.body, [])
                else:
                    pending.extend(self.function_header_expressions(node))
                    self.collect_scope_bindings(node, node.body, self.function_parameters(node.args))
                continue
            if isinstance(node, ast.Lambda):
                pending.extend(self.function_header_expressions(node))
                self.collect_scope_bindings(node, [node.body], self.function_parameters(node.args))
                continue
            if isinstance(node, ast.AnnAssign) and node.value is None:
                # A bare annotation makes a function name local without binding it; elsewhere it binds nothing.
                if isinstance(node.target, ast.Name) and isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    bindings.setdefault(node.target.id, []).append(node.target)
                pending.append(node.annotation)
                continue
            if isinstance(node, ast.Name) and isinstance(node.ctx, (ast.Store, ast.Del)):
                bindings.setdefault(node.id, []).append(node)
            elif isinstance(node, ast.alias) and node.name != "*":
                bindings.setdefault(node.asname or node.name.split(".")[0], []).append(node)
            elif isinstance(node, (ast.ExceptHandler, ast.MatchAs, ast.MatchStar)) and node.name:
                bindings.setdefault(node.name, []).append(node)
            elif isinstance(node, ast.MatchMapping) and node.rest:
                bindings.setdefault(node.rest, []).append(node)
            elif isinstance(node, (ast.Global, ast.Nonlocal)):
                self.declared_global_names.update(node.names)
            pending.extend(ast.iter_child_nodes(node))

    def function_header_expressions(self, function):
        arguments = function.args
        expressions = [*arguments.defaults, *(default for default in arguments.kw_defaults if default is not None)]
        if isinstance(function, ast.Lambda):
            return expressions
        expressions.extend(parameter.annotation for parameter in self.function_parameters(arguments) if parameter.annotation is not None)
        if function.returns is not None:
            expressions.append(function.returns)
        return expressions

    def function_parameters(self, arguments):
        parameters = [*arguments.posonlyargs, *arguments.args, *arguments.kwonlyargs]
        parameters.extend(parameter for parameter in (arguments.vararg, arguments.kwarg) if parameter is not None)
        return parameters

    def is_annotation_only_target(self, node):
        statement = self.parents.get(node)
        return isinstance(statement, ast.AnnAssign) and statement.target is node and statement.value is None

    def binding_scope(self, name_node):
        # Class bodies are visible only to code directly inside them, never to their methods.
        child = name_node
        parent = self.parents.get(child)
        is_class_visible = True
        while parent is not None:
            if isinstance(parent, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)) and self.is_function_body(parent, child):
                if name_node.id in self.scope_bindings.get(parent, {}):
                    return parent
                is_class_visible = False
            elif isinstance(parent, ast.ClassDef) and child in parent.body:
                if is_class_visible and name_node.id in self.scope_bindings.get(parent, {}):
                    return parent
                is_class_visible = False
            child, parent = parent, self.parents.get(parent)
        return self.tree

    def is_function_body(self, function, child):
        if isinstance(function, ast.Lambda):
            return child is function.body
        return child in function.body

    def static_binding(self, scope, name):
        if name in self.declared_global_names:
            return None
        bindings = self.scope_bindings.get(scope, {}).get(name, [])
        return bindings[0] if len(bindings) == 1 else None

    def assignment_statement(self, scope, binding):
        statement = self.parents.get(binding)
        if isinstance(statement, ast.Assign):
            is_single_target = len(statement.targets) == 1 and statement.targets[0] is binding
        elif isinstance(statement, ast.AnnAssign):
            is_single_target = statement.target is binding and statement.value is not None
        else:
            return None
        if not is_single_target or self.parents.get(statement) is not scope:
            return None
        return statement

    def resolved_value(self, expression, frame):
        """Follow provable bindings from expression.

        Returns the last (expression, frame) pair reached. A Name or Attribute result
        means a binding could not be proven; a ClassDef result names a local class.
        """
        result = (expression, frame)
        for result in self.binding_chain(expression, frame):
            pass
        return result

    def binding_chain(self, expression, frame):
        seen = set()
        while expression is not None:
            key = (expression, frame.identity, frame.initializer)
            if key in seen:
                return
            seen.add(key)
            yield expression, frame
            following = self.binding_step(expression, frame)
            if following is None:
                return
            expression, frame = following

    def binding_step(self, expression, frame):
        if isinstance(expression, ast.Name) and isinstance(expression.ctx, ast.Load):
            return self.name_value(expression, frame)
        if isinstance(expression, ast.Attribute) and isinstance(expression.ctx, ast.Load):
            return self.attribute_value(expression, frame)
        return None

    def name_value(self, name_node, frame):
        scope = self.binding_scope(name_node)
        binding = self.static_binding(scope, name_node.id)
        if binding is None:
            return None
        if isinstance(binding, ast.arg):
            if frame.initializer is scope:
                return frame.arguments.get(name_node.id)
            return None
        if isinstance(binding, (ast.ClassDef, ast.alias)):
            return binding, frame
        statement = self.assignment_statement(scope, binding)
        if statement is None or self.is_used_before_binding(name_node, statement, scope):
            return None
        if isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            return statement.value, frame
        return statement.value, EvaluationFrame()

    def is_used_before_binding(self, reference, statement, scope):
        node = reference
        while node is not None and node is not scope:
            parent = self.parents.get(node)
            if parent is not scope and isinstance(parent, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)) and self.is_function_body(parent, node):
                return False
            node = parent
        return (reference.lineno, reference.col_offset) < (statement.end_lineno, statement.end_col_offset)

    def attribute_value(self, attribute, frame):
        owner, owner_frame = self.resolved_value(attribute.value, frame)
        owner_class = self.self_reference_class(owner)
        if owner_class is not None:
            if owner_class is self.flow_class:
                return self.flow_field_value(attribute.attr, attribute, frame)
            if owner_frame.instance is not None and owner_class is owner_frame.instance.declaration:
                return self.step_field_value(owner_frame.instance, attribute.attr, attribute, frame)
            return None
        if isinstance(owner, ast.ClassDef):
            return self.class_attribute_value(owner, attribute.attr)
        return None

    def self_reference_class(self, expression):
        if not isinstance(expression, ast.Name) or expression.id != "self":
            return None
        method = self.binding_scope(expression)
        declaration = self.parents.get(method)
        if not isinstance(method, (ast.FunctionDef, ast.AsyncFunctionDef)) or not isinstance(declaration, ast.ClassDef):
            return None
        if any(self.symbol(decorator) in {"staticmethod", "classmethod"} for decorator in method.decorator_list):
            return None
        positional = [*method.args.posonlyargs, *method.args.args]
        if not positional or self.static_binding(method, "self") is not positional[0]:
            return None
        return declaration

    def flow_field_value(self, name, read, read_frame):
        declaration = self.flow_class
        if self.has_dynamic_attributes or self.is_foreign_attribute(name) or not self.has_modeled_construction(declaration, "Flow"):
            return None
        class_bindings = self.scope_bindings.get(declaration, {}).get(name, [])
        stores = self.self_attribute_stores.get(declaration, {}).get(name, [])
        if len(class_bindings) + len(stores) != 1:
            return None
        if class_bindings:
            return self.class_attribute_value(declaration, name)
        return self.initializer_store_value(declaration, stores[0], EvaluationFrame(), read, read_frame)

    def step_field_value(self, instance, name, read, read_frame):
        declaration = instance.declaration
        if self.has_dynamic_attributes or self.is_foreign_attribute(name) or not self.has_modeled_construction(declaration, "Step"):
            return None
        class_bindings = self.scope_bindings.get(declaration, {}).get(name, [])
        stores = self.self_attribute_stores.get(declaration, {}).get(name, [])
        if len(class_bindings) + len(stores) != 1:
            return None
        initializer = self.method(declaration, "__init__")
        if class_bindings:
            if initializer is None and (instance.call.args or instance.call.keywords):
                return None
            return self.class_attribute_value(declaration, name)
        arguments = self.constructor_arguments(initializer, instance) if initializer else None
        if arguments is None:
            return None
        return self.initializer_store_value(declaration, stores[0], EvaluationFrame(instance, initializer, arguments), read, read_frame)

    def class_attribute_value(self, declaration, name):
        if self.has_dynamic_attributes or self.is_foreign_attribute(name):
            return None
        binding = self.static_binding(declaration, name)
        statement = self.assignment_statement(declaration, binding) if binding is not None else None
        return (statement.value, EvaluationFrame()) if statement else None

    def initializer_store_value(self, declaration, store, frame, read, read_frame):
        initializer = self.method(declaration, "__init__")
        statement = self.parents.get(store)
        if initializer is None or self.parents.get(statement) is not initializer or self.function_returns(initializer):
            return None
        if self.is_read_before_store(read, read_frame, statement, initializer):
            return None
        if isinstance(statement, ast.Assign) and len(statement.targets) == 1 and statement.targets[0] is store:
            return statement.value, frame
        if isinstance(statement, ast.AnnAssign) and statement.target is store and statement.value is not None:
            return statement.value, frame
        return None

    def is_read_before_store(self, read, read_frame, statement, initializer):
        # Code that runs earlier in the same __init__ sees no field yet.
        while read_frame.initializer is not None and self.is_within(read, read_frame.initializer):
            read, read_frame = read_frame.instance.call, read_frame.instance.frame
        node = read
        while node is not None and node is not initializer:
            parent = self.parents.get(node)
            if parent is not initializer and isinstance(parent, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)) and self.is_function_body(parent, node):
                return False
            node = parent
        return node is not None and (read.lineno, read.col_offset) < (statement.end_lineno, statement.end_col_offset)

    def is_within(self, node, ancestor):
        while node is not None and node is not ancestor:
            node = self.parents.get(node)
        return node is ancestor

    def has_modeled_construction(self, declaration, dex_base):
        # Decorators, other bases, and a leaked self can all change fields after construction.
        if declaration.decorator_list or self.has_unknown_bases(declaration, dex_base) or self.has_escaped_receiver(declaration):
            return False
        initializer = self.method(declaration, "__init__")
        return initializer is None or not self.function_returns(initializer)

    def has_escaped_receiver(self, declaration):
        # Code outside the analysis may change a receiver that leaves an attribute base or a local constructor call.
        if declaration not in self.escaped_receiver_results:
            self.escaped_receiver_results[declaration] = any(
                isinstance(node, ast.Name) and node.id == receiver and not self.is_modeled_receiver_use(node)
                for receiver, method in self.receiver_methods(declaration)
                for node in ast.walk(method)
            )
        return self.escaped_receiver_results[declaration]

    def receiver_methods(self, declaration):
        for method in declaration.body:
            if not isinstance(method, (ast.FunctionDef, ast.AsyncFunctionDef)):
                continue
            positional = [*method.args.posonlyargs, *method.args.args]
            if positional and not any(self.symbol(decorator) == "staticmethod" for decorator in method.decorator_list):
                yield positional[0].arg, method

    def is_modeled_receiver_use(self, node):
        parent = self.parents.get(node)
        if isinstance(parent, ast.Attribute):
            return True
        if not isinstance(parent, ast.Call) or not (node in parent.args or any(keyword.value is node for keyword in parent.keywords)):
            return False
        function = parent.func.value if isinstance(parent.func, ast.Subscript) else parent.func
        return isinstance(function, ast.Name) and isinstance(self.static_binding(self.binding_scope(function), function.id), ast.ClassDef)

    def has_unknown_bases(self, declaration, dex_base):
        for base in declaration.bases:
            if self.is_dex_reference(base, dex_base):
                continue
            if self.symbol(base) in {"Generic", "typing.Generic"}:
                continue
            return True
        return bool(declaration.keywords)

    def constructor_arguments(self, initializer, instance):
        call = instance.call
        if any(isinstance(argument, ast.Starred) for argument in call.args) or any(keyword.arg is None for keyword in call.keywords):
            return None
        receiver, *positional = [*initializer.args.posonlyargs, *initializer.args.args]
        if len(call.args) > len(positional) and initializer.args.vararg is None:
            return None
        arguments = {parameter.arg: (argument, instance.frame) for parameter, argument in zip(positional, call.args)}
        keyword_names = {parameter.arg for parameter in [*initializer.args.args, *initializer.args.kwonlyargs] if parameter is not receiver}
        for keyword in call.keywords:
            if keyword.arg in arguments or (keyword.arg not in keyword_names and initializer.args.kwarg is None):
                return None
            if keyword.arg in keyword_names:
                arguments[keyword.arg] = (keyword.value, instance.frame)
        defaults = dict(zip([parameter.arg for parameter in positional[len(positional) - len(initializer.args.defaults):]], initializer.args.defaults))
        defaults.update((parameter.arg, default) for parameter, default in zip(initializer.args.kwonlyargs, initializer.args.kw_defaults) if default is not None)
        for parameter in [*positional, *initializer.args.kwonlyargs]:
            if parameter.arg in arguments:
                continue
            if parameter.arg not in defaults:
                return None
            arguments[parameter.arg] = (defaults[parameter.arg], EvaluationFrame())
        return arguments

    def function_nodes(self, function):
        pending = list(function.body)
        while pending:
            node = pending.pop()
            yield node
            if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda, ast.ClassDef)):
                pending.extend(ast.iter_child_nodes(node))

    def function_returns(self, function):
        return [node for node in self.function_nodes(function) if isinstance(node, ast.Return)]

    def constructed_class(self, call, frame):
        function = call.func.value if isinstance(call.func, ast.Subscript) else call.func
        declaration, _ = self.resolved_value(function, frame)
        return declaration if isinstance(declaration, ast.ClassDef) else None

    def step_instance_value(self, node, frame):
        if not isinstance(node, ast.Call):
            return None
        declaration = self.constructed_class(node, frame)
        if declaration is None or self.step_classes.get(declaration.name) is not declaration:
            return None
        return StepInstance(declaration, node, frame)

    def local_step_base(self, declaration):
        for base in declaration.bases:
            base_class, _ = self.resolved_value(base.value if isinstance(base, ast.Subscript) else base, EvaluationFrame())
            if not isinstance(base_class, ast.ClassDef) or base_class is declaration:
                continue
            if self.step_classes.get(base_class.name) is base_class or self.local_step_base(base_class):
                return base_class
        return None

    def instance_step_type(self, instance, report_errors):
        declaration = instance.declaration
        method = self.method(declaration, "get_step_type")
        if method is None:
            return declaration.name
        names = []
        for statement in self.function_returns(method):
            value, _ = self.resolved_value(statement.value, EvaluationFrame(instance)) if statement.value is not None else (None, None)
            names.append(value.value if isinstance(value, ast.Constant) and isinstance(value.value, str) else None)
        if not names or names[0] is None or any(name != names[0] for name in names):
            if report_errors:
                self.error("dynamic_type_name", "get_step_type must return one static string literal or a field bound to a literal at registration", self.span(method))
            return None
        return names[0] or declaration.name

    def step_reference_type(self, call):
        arguments = [*call.args, *(keyword.value for keyword in call.keywords if keyword.arg == "step_type")]
        if len(arguments) != 1 or len(call.args) + len(call.keywords) != 1:
            return None
        argument = arguments[0]
        return argument.value if isinstance(argument, ast.Constant) and isinstance(argument.value, str) else None
