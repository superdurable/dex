# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import ast
import builtins
import datetime
import decimal

# Standard-library modules whose functions and objects do no I/O; PYTHON_DENIED_STANDARD_LIBRARY_CALLS lists exceptions.
PYTHON_PURE_STANDARD_LIBRARY_MODULES = {
    "abc", "base64", "binascii", "bisect", "calendar", "cmath", "collections", "contextlib", "copy",
    "dataclasses", "datetime", "decimal", "difflib", "enum", "fractions", "functools", "hashlib", "heapq",
    "hmac", "html", "ipaddress", "itertools", "json", "keyword", "logging", "math", "numbers", "operator",
    "random", "re", "statistics", "string", "struct", "textwrap", "time", "types", "typing",
    "typing_extensions", "unicodedata", "urllib.parse", "uuid", "zlib", "zoneinfo",
}

# These read or write files, block, or read host state.
PYTHON_DENIED_STANDARD_LIBRARY_CALLS = {
    "json.load", "json.dump", "time.sleep", "uuid.uuid1", "uuid.getnode",
}

# Single I/O-free functions of modules that otherwise do I/O.
PYTHON_PURE_STANDARD_LIBRARY_CALLS = {
    "os.getenv", "os.environ.get", "os.path.basename", "os.path.dirname", "os.path.join",
    "os.path.normpath", "os.path.splitext", "io.BytesIO", "io.StringIO", "asyncio.gather",
}

# Dex SDK names that reach the Dex server, files, or other processes.
PYTHON_DEX_EXTERNAL_NAMES = {
    "Client", "AsyncClient", "Worker", "AsyncWorker", "BlobCache", "open_blob_cache",
    "FileCredential", "ApiKeyCredential", "FilePayloadEncryptionKeyProvider",
}

PYTHON_EXTERNAL_BUILTINS = {
    "open", "input", "print", "breakpoint", "exit", "quit", "help",
    "getattr", "setattr", "delattr", "exec", "eval", "compile", "__import__", "globals", "locals", "vars",
}

# Methods of built-in values, dates, and decimals, which a Step input or a literal may hold.
PYTHON_VALUE_METHODS = {
    name
    for value_type in (
        str, bytes, bytearray, list, dict, set, frozenset, tuple, int, float, complex, bool,
        datetime.datetime, datetime.date, datetime.time, datetime.timedelta, decimal.Decimal,
    )
    for name in dir(value_type)
    if not name.startswith("_")
}

# Positional arguments that these callables call.
PYTHON_CALLABLE_ARGUMENTS = {
    "map": 0, "filter": 0, "functools.partial": 0, "functools.reduce": 0, "itertools.starmap": 0,
    "itertools.filterfalse": 0, "itertools.takewhile": 0, "itertools.dropwhile": 0,
}

# Callables that call their key= argument; a list's sort method does too.
PYTHON_KEY_CALLABLES = {
    "sorted", "min", "max", "heapq.nlargest", "heapq.nsmallest", "itertools.groupby",
    "bisect.bisect", "bisect.bisect_left", "bisect.bisect_right", "bisect.insort", "bisect.insort_left", "bisect.insort_right",
}

# Resource methods that return application values; other resource methods return Dex SDK objects.
PYTHON_RESOURCE_VALUE_METHODS = {"get", "results"}

PYTHON_MAXIMUM_HELPER_DEPTH = 6

DATA = "data"
# SDK values are Dex SDK or pure standard-library objects; every method of one is allowed.
SDK = "sdk"
CONTEXT = "context"
OPAQUE = "opaque"
LOCAL_FUNCTION = "local_function"
# A value whose kind depends on itself, such as value = value.strip(); other bindings decide it.
CYCLE = "cycle"


class ExternalCallScope:
    # One function being checked: the class that self names, and the kind of each parameter.
    def __init__(self, function, self_kind, parameter_kinds, depth):
        self.function = function
        self.self_kind = self_kind
        self.parameter_kinds = parameter_kinds
        self.depth = depth


class ExternalCallAnalyzer:
    def initialize_external_calls(self):
        self.method_external_calls = []
        # Helper results keyed by function and parameter kinds; only exact results are cached.
        self.helper_external_call_results = {}
        self.helpers_in_progress = set()
        self.kinds_in_progress = set()
        self.pending_method_external_calls = []

    def defer_method_external_calls(self, owner_id, step_class, phase):
        self.pending_method_external_calls.append((owner_id, step_class, phase, self.current_frame))

    # Checks only Step methods that share a lock with an RPC; other methods get no marker, meaning unchecked.
    def record_deferred_method_external_calls(self):
        method_locks = {}
        rpc_locks = set()
        for edge in self.graph["edges"]:
            if edge["kind"] != "resource_lock":
                continue
            phase = edge.get("metadata", {}).get("phase")
            if phase == "rpc":
                rpc_locks.add(edge["to"])
            elif phase in ("execute", "wait_for"):
                method_locks.setdefault((edge["from"], phase), set()).add(edge["to"])
        step_frame = self.current_frame
        for owner_id, step_class, phase, frame in self.pending_method_external_calls:
            if not method_locks.get((owner_id, phase), set()) & rpc_locks:
                continue
            self.current_frame = frame
            try:
                self.record_method_external_calls(owner_id, step_class, phase)
            except RecursionError:
                self.kinds_in_progress.clear()
                self.helpers_in_progress.clear()
                self.add_method_external_calls(owner_id, phase, [{"callee": f"code dexcli cannot read ({phase} nests too deeply)", "span": self.span(step_class)}])
        self.current_frame = step_frame

    def record_method_external_calls(self, owner_id, step_class, phase):
        method = self.method(step_class, phase)
        if method is None:
            if phase in self.scope_bindings.get(step_class, {}):
                self.add_method_external_calls(owner_id, phase, [{"callee": f"code dexcli cannot read ({phase} is assigned, not defined)", "span": self.span(step_class)}])
            return
        for decorator in method.decorator_list:
            if not self.is_followed_step_method_decorator(decorator):
                self.add_method_external_calls(owner_id, phase, [{"callee": f"code dexcli cannot read ({phase} has decorator {self.unparse(decorator)})", "span": self.span(decorator)}])
                return
        positional = [*method.args.posonlyargs, *method.args.args]
        is_static = any(isinstance(decorator, ast.Name) and decorator.id == "staticmethod" for decorator in method.decorator_list)
        parameter_kinds = {}
        if not is_static and positional:
            positional = positional[1:]
        for index, parameter in enumerate(positional):
            parameter_kinds[parameter.arg] = CONTEXT if index == 0 else DATA
        scope = ExternalCallScope(method, ("class", step_class.name), parameter_kinds, 0)
        self.external_call_owner_id = owner_id
        calls, _ = self.function_external_calls(scope, first_only=False)
        self.add_method_external_calls(owner_id, phase, calls)

    def add_method_external_calls(self, owner_id, phase, calls):
        self.method_external_calls.append({"nodeId": owner_id, "phase": phase, "calls": calls})

    def is_followed_step_method_decorator(self, decorator):
        if isinstance(decorator, ast.Name) and decorator.id == "staticmethod":
            return True
        return not isinstance(decorator, ast.Call) and self.imported_name(decorator) in {"typing.override", "typing_extensions.override", "typing.final", "typing_extensions.final"}

    # Returns the possible external calls of scope.function in source order, and whether the result is exact.
    def function_external_calls(self, scope, first_only):
        calls = sorted(
            (node for statement in scope.function.body for node in ast.walk(statement) if isinstance(node, ast.Call)),
            key=lambda call: (call.lineno, call.col_offset),
        )
        found = []
        is_exact = True
        for call in calls:
            callee, _, call_is_exact = self.classify_call(call.func, call.args, call.keywords, scope)
            is_exact = is_exact and call_is_exact
            if callee is not None:
                found.append({"callee": callee, "span": self.span(call)})
            for argument in self.called_arguments(call, scope):
                callee, _, argument_is_exact = self.classify_call(argument, [], [], scope)
                is_exact = is_exact and argument_is_exact
                if callee is not None:
                    found.append({"callee": callee, "span": self.span(argument)})
            if first_only and found:
                break
        return found, is_exact

    def called_arguments(self, call, scope):
        name = self.callable_qualified_name(call.func)
        arguments = []
        if name in PYTHON_KEY_CALLABLES or (isinstance(call.func, ast.Attribute) and call.func.attr == "sort"):
            arguments = [keyword.value for keyword in call.keywords if keyword.arg == "key"]
        position = PYTHON_CALLABLE_ARGUMENTS.get(name)
        if position is not None and len(call.args) > position:
            arguments.append(call.args[position])
        return [argument for argument in arguments if not isinstance(argument, ast.Lambda)]

    def callable_qualified_name(self, expression):
        if isinstance(expression, ast.Name) and self.is_builtin_name(expression):
            return expression.id
        return self.qualified_import_name(expression)

    # Returns (external callee or None, kind of the call result, whether the answer is exact).
    def classify_call(self, function, arguments, keywords, scope):
        callee = self.unparse(function)
        if isinstance(function, ast.Lambda):
            return None, DATA, True
        qualified = self.qualified_import_name(function)
        if qualified is not None:
            if qualified == "dex" or qualified.startswith("dex."):
                if qualified.rsplit(".", 1)[-1] in PYTHON_DEX_EXTERNAL_NAMES:
                    return callee, OPAQUE, True
                return None, SDK, True
            if self.is_pure_standard_library_call(qualified):
                return None, SDK, True
            return callee, OPAQUE, True
        if isinstance(function, ast.Name):
            return self.classify_name_call(function, arguments, keywords, scope)
        if isinstance(function, ast.Attribute):
            return self.classify_method_call(function, arguments, keywords, scope)
        return callee, OPAQUE, True

    def classify_name_call(self, function, arguments, keywords, scope):
        callee = function.id
        if self.is_builtin_name(function):
            if function.id in PYTHON_EXTERNAL_BUILTINS:
                return callee, OPAQUE, True
            return None, DATA, True
        binding_scope = self.binding_scope(function)
        if binding_scope is self.tree:
            binding = self.static_binding(self.tree, function.id)
            if isinstance(binding, (ast.FunctionDef, ast.AsyncFunctionDef)):
                return self.helper_call(callee, binding, None, arguments, keywords, scope, DATA)
            if isinstance(binding, ast.ClassDef):
                return self.construction_call(callee, binding, arguments, keywords, scope)
            return callee, OPAQUE, True
        if self.value_kind(function, scope) == LOCAL_FUNCTION:
            for argument in [*arguments, *(keyword.value for keyword in keywords)]:
                if self.value_kind(argument, scope) == OPAQUE:
                    return f"{callee} (passes {self.unparse(argument)})", OPAQUE, True
            return None, DATA, True
        return callee, OPAQUE, True

    def classify_method_call(self, function, arguments, keywords, scope):
        callee = self.unparse(function)
        receiver = function.value
        if self.is_resource_receiver(receiver, scope):
            return None, DATA if function.attr in PYTHON_RESOURCE_VALUE_METHODS else SDK, True
        receiver_kind = self.value_kind(receiver, scope)
        if receiver_kind == CYCLE:
            # The outer evaluation of this receiver decides the call.
            return None, CYCLE, False
        if receiver_kind in (SDK, CONTEXT):
            return None, SDK if receiver_kind == SDK else DATA, True
        if receiver_kind == DATA:
            if function.attr in PYTHON_VALUE_METHODS:
                return None, DATA, True
            return self.module_method_call(callee, function.attr, arguments, keywords, scope)
        if isinstance(receiver_kind, tuple):
            declaration = self.classes.get(receiver_kind[1])
            method = self.method(declaration, function.attr) if declaration is not None else None
            if method is not None:
                is_static = any(isinstance(decorator, ast.Name) and decorator.id == "staticmethod" for decorator in method.decorator_list)
                return self.helper_call(callee, method, None if is_static else receiver_kind, arguments, keywords, scope, DATA)
        return callee, OPAQUE, True

    # A method of a data value is pure when every same-module class that defines it is pure.
    def module_method_call(self, callee, name, arguments, keywords, scope):
        methods = [(declaration, self.method(declaration, name)) for declaration in self.classes.values()]
        methods = [(declaration, method) for declaration, method in methods if method is not None]
        if not methods:
            return callee, OPAQUE, True
        is_exact = True
        for declaration, method in methods:
            external, _, method_is_exact = self.helper_call(callee, method, ("class", declaration.name), arguments, keywords, scope, DATA)
            is_exact = is_exact and method_is_exact
            if external is not None:
                return external, OPAQUE, is_exact
        return None, DATA, is_exact

    # Construction runs the metaclass __call__, then __new__, __init__, and __post_init__ found along the bases.
    def construction_call(self, callee, declaration, arguments, keywords, scope):
        classes = self.same_file_class_chain(declaration)
        if classes is None:
            return f"{callee} (a constructor dexcli cannot read)", OPAQUE, True
        constructors = []
        for metaclass in (keyword.value for chained in classes for keyword in chained.keywords if keyword.arg == "metaclass"):
            metaclass_declaration = self.static_binding(self.tree, metaclass.id) if isinstance(metaclass, ast.Name) else None
            if not isinstance(metaclass_declaration, ast.ClassDef):
                qualified = self.qualified_import_name(metaclass)
                if qualified is None or not self.is_pure_standard_library_call(qualified):
                    return f"{callee} (a constructor dexcli cannot read)", OPAQUE, True
                continue
            metaclass_chain = self.same_file_class_chain(metaclass_declaration)
            if metaclass_chain is None:
                return f"{callee} (a constructor dexcli cannot read)", OPAQUE, True
            constructors.extend(self.first_class_method(metaclass_chain, "__call__"))
        for name in ("__new__", "__init__", "__post_init__"):
            constructors.extend(self.first_class_method(classes, name))
        is_exact = True
        for owner, method in constructors:
            passes_arguments = method.name != "__post_init__"
            external, _, method_is_exact = self.helper_call(
                callee, method, ("class", owner.name), arguments if passes_arguments else [], keywords if passes_arguments else [], scope, DATA,
            )
            is_exact = is_exact and method_is_exact
            if external is not None:
                return external, OPAQUE, is_exact
        return None, ("class", declaration.name), is_exact

    def first_class_method(self, classes, name):
        for declaration in classes:
            method = self.method(declaration, name)
            if method is not None:
                return [(declaration, method)]
        return []

    # Returns the class and its same-file bases in lookup order, or None when a base may define a constructor.
    def same_file_class_chain(self, declaration):
        chain = []
        pending = [declaration]
        while pending:
            current = pending.pop(0)
            if current in chain:
                continue
            chain.append(current)
            for base in current.bases:
                base = base.value if isinstance(base, ast.Subscript) else base
                binding = self.static_binding(self.tree, base.id) if isinstance(base, ast.Name) else None
                if isinstance(binding, ast.ClassDef):
                    pending.append(binding)
                elif not self.is_constructor_free_base(base):
                    return None
        return chain

    # Built-in, pure standard-library, and Dex SDK bases construct without I/O.
    def is_constructor_free_base(self, base):
        if self.is_builtin_name(base):
            return True
        qualified = self.qualified_import_name(base)
        return qualified is not None and self.is_allowed_qualified_value(qualified)

    def helper_call(self, callee, function, self_kind, arguments, keywords, scope, result_kind):
        parameter_kinds = self.helper_parameter_kinds(function, self_kind, arguments, keywords, scope)
        # Step fields and resources resolve per Step node, so the node is part of the key.
        key = (self.external_call_owner_id, function, self_kind, tuple(sorted(parameter_kinds.items())))
        cached = self.helper_external_call_results.get(key, False)
        if cached is not False:
            return (None if cached is None else f"{callee} (calls {cached})"), (OPAQUE if cached else result_kind), True
        if key in self.helpers_in_progress:
            return None, result_kind, False
        if scope.depth + 1 > PYTHON_MAXIMUM_HELPER_DEPTH:
            return f"{callee} (calls a helper chain deeper than dexcli follows)", OPAQUE, False
        self.helpers_in_progress.add(key)
        calls, is_exact = self.function_external_calls(ExternalCallScope(function, self_kind, parameter_kinds, scope.depth + 1), first_only=True)
        self.helpers_in_progress.discard(key)
        inner = calls[0]["callee"] if calls else None
        if is_exact:
            self.helper_external_call_results[key] = inner
        if inner is None:
            return None, result_kind, is_exact
        return f"{callee} (calls {inner})", OPAQUE, is_exact

    def helper_parameter_kinds(self, function, self_kind, arguments, keywords, scope):
        positional = [*function.args.posonlyargs, *function.args.args]
        if self_kind is not None and positional:
            positional = positional[1:]
        kinds = {}
        for parameter in [*positional, *function.args.kwonlyargs]:
            kinds[parameter.arg] = DATA
        extra_kinds = []
        for index, argument in enumerate(arguments):
            kind = self.argument_kind(argument.value if isinstance(argument, ast.Starred) else argument, scope)
            if index < len(positional) and not isinstance(argument, ast.Starred):
                kinds[positional[index].arg] = kind
            else:
                extra_kinds.append(kind)
        for keyword in keywords:
            kind = self.argument_kind(keyword.value, scope)
            if keyword.arg in kinds:
                kinds[keyword.arg] = kind
            else:
                extra_kinds.append(kind)
        # Unmatched arguments may land in any parameter.
        if any(kind != DATA for kind in extra_kinds):
            for name in kinds:
                kinds[name] = OPAQUE
        for parameter in (function.args.vararg, function.args.kwarg):
            if parameter is not None:
                kinds[parameter.arg] = OPAQUE if any(kind != DATA for kind in extra_kinds) else DATA
        return kinds

    # A helper argument still being resolved is checked as opaque, never as data.
    def argument_kind(self, argument, scope):
        kind = self.value_kind(argument, scope)
        return OPAQUE if kind == CYCLE else kind

    def is_resource_receiver(self, receiver, scope):
        if isinstance(receiver, ast.Name) and receiver.id == "self" and scope.self_kind is not None:
            return False
        if isinstance(receiver, ast.Attribute) and isinstance(receiver.value, ast.Name) and receiver.value.id == "self":
            declaration = self.classes.get(scope.self_kind[1]) if scope.self_kind is not None else None
            if declaration is None or (declaration not in self.step_classes.values() and declaration is not self.flow_class):
                return False
        if isinstance(receiver, ast.Name) and self.binding_scope(receiver) is not self.tree:
            return False
        return self.resource_for(receiver, self.external_call_owner_id) is not None

    # Kinds: DATA, SDK, CONTEXT, OPAQUE, LOCAL_FUNCTION, or ("class", name) for self or an instance of a module class.
    def value_kind(self, expression, scope):
        key = (expression, scope.function, scope.self_kind, tuple(sorted(scope.parameter_kinds.items())))
        if key in self.kinds_in_progress:
            return CYCLE
        self.kinds_in_progress.add(key)
        try:
            return self.expression_kind(expression, scope)
        finally:
            self.kinds_in_progress.discard(key)

    def expression_kind(self, expression, scope):
        if isinstance(expression, (ast.Constant, ast.JoinedStr, ast.List, ast.Tuple, ast.Set, ast.Dict, ast.ListComp, ast.SetComp, ast.DictComp, ast.GeneratorExp, ast.BinOp, ast.UnaryOp, ast.Compare)):
            return DATA
        if isinstance(expression, ast.BoolOp):
            return self.combined_kind([self.value_kind(value, scope) for value in expression.values])
        if isinstance(expression, ast.Lambda):
            return LOCAL_FUNCTION
        if isinstance(expression, (ast.Await, ast.NamedExpr, ast.Starred)):
            return self.value_kind(expression.value, scope)
        if isinstance(expression, ast.IfExp):
            return self.combined_kind([self.value_kind(expression.body, scope), self.value_kind(expression.orelse, scope)])
        if isinstance(expression, ast.Subscript):
            owner_kind = self.value_kind(expression.value, scope)
            if owner_kind == CYCLE:
                return CYCLE
            return DATA if owner_kind in (DATA, SDK, CONTEXT) else OPAQUE
        if isinstance(expression, ast.Call):
            callee, kind, _ = self.classify_call(expression.func, expression.args, expression.keywords, scope)
            return OPAQUE if callee is not None else kind
        if isinstance(expression, ast.Name):
            return self.name_kind(expression, scope)
        if isinstance(expression, ast.Attribute):
            return self.attribute_kind(expression, scope)
        return OPAQUE

    def name_kind(self, name, scope):
        if name.id == "self" and scope.self_kind is not None and self.binding_scope(name) is scope.function:
            return scope.self_kind
        if self.is_builtin_name(name):
            return DATA
        qualified = self.qualified_import_name(name)
        if qualified is not None:
            return SDK if self.is_allowed_qualified_value(qualified) else OPAQUE
        binding_scope = self.binding_scope(name)
        if binding_scope is self.tree:
            binding = self.static_binding(self.tree, name.id)
            if isinstance(binding, ast.ClassDef):
                return ("class", binding.name)
            if isinstance(binding, (ast.FunctionDef, ast.AsyncFunctionDef)):
                return OPAQUE
            statement = self.assignment_statement(self.tree, binding) if binding is not None else None
            if statement is None:
                return OPAQUE
            return self.module_value_kind(statement.value)
        if isinstance(binding_scope, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            bindings = self.scope_bindings.get(binding_scope, {}).get(name.id, [])
            return self.combined_kind([self.binding_kind(binding, binding_scope, scope) for binding in bindings])
        return OPAQUE

    # A module-level value is SDK or data only when every call that builds it is allowed.
    def module_value_kind(self, expression):
        module_scope = ExternalCallScope(self.tree, None, {}, PYTHON_MAXIMUM_HELPER_DEPTH)
        for node in ast.walk(expression):
            if isinstance(node, ast.Call) and self.classify_call(node.func, node.args, node.keywords, module_scope)[0] is not None:
                return OPAQUE
        return self.value_kind(expression, module_scope)

    def binding_kind(self, binding, binding_scope, scope):
        if isinstance(binding, ast.arg):
            if binding_scope is scope.function:
                return scope.parameter_kinds.get(binding.arg, OPAQUE)
            return DATA
        if isinstance(binding, (ast.FunctionDef, ast.AsyncFunctionDef)):
            return LOCAL_FUNCTION
        if isinstance(binding, ast.ExceptHandler):
            return DATA
        if not isinstance(binding, ast.Name):
            return OPAQUE
        target = binding
        parent = self.parents.get(target)
        while isinstance(parent, (ast.Tuple, ast.List, ast.Starred)):
            target, parent = parent, self.parents.get(parent)
        is_unpacked = target is not binding
        if isinstance(parent, (ast.Assign, ast.AnnAssign, ast.AugAssign, ast.NamedExpr)) and parent.value is not None:
            kind = self.value_kind(parent.value, scope)
        elif isinstance(parent, (ast.For, ast.AsyncFor, ast.comprehension)) and parent.target is target:
            kind = self.value_kind(parent.iter, scope)
            is_unpacked = True
        elif isinstance(parent, ast.withitem) and parent.optional_vars is target:
            kind = self.value_kind(parent.context_expr, scope)
        else:
            return OPAQUE
        if kind == CYCLE:
            return CYCLE
        if isinstance(parent, ast.AugAssign) and kind != OPAQUE:
            return DATA
        if is_unpacked:
            return DATA if kind in (DATA, SDK, CONTEXT) else OPAQUE
        return kind

    def attribute_kind(self, attribute, scope):
        qualified = self.qualified_import_name(attribute)
        if qualified is not None:
            return SDK if self.is_allowed_qualified_value(qualified) else OPAQUE
        owner_kind = self.value_kind(attribute.value, scope)
        if owner_kind == CYCLE:
            return CYCLE
        if owner_kind in (DATA, CONTEXT):
            return DATA
        if owner_kind == SDK:
            return SDK
        if not isinstance(owner_kind, tuple):
            return OPAQUE
        if self.is_resource_receiver(attribute, scope):
            return SDK
        declaration = self.classes.get(owner_kind[1])
        if declaration is None:
            return OPAQUE
        if declaration in self.step_classes.values() or declaration is self.flow_class:
            return self.dex_field_kind(attribute)
        if isinstance(attribute.value, ast.Name) and self.static_binding(self.tree, attribute.value.id) is declaration:
            # A class attribute, such as an Enum member or a constant.
            return DATA
        return self.instance_field_kind(declaration, attribute.attr)

    # A Step or Flow field is data only when its stored value resolves; a Flow reference keeps its methods.
    def dex_field_kind(self, attribute):
        value, _ = self.resolved_value(attribute, self.current_frame)
        if self.flow_class is not None and self.self_reference_class(value) is self.flow_class:
            return ("class", self.flow_class.name)
        if value is attribute or isinstance(value, (ast.Name, ast.Attribute, ast.alias, ast.ClassDef)):
            return OPAQUE
        return self.module_value_kind(value)

    # An instance field is data when the class body assigns it or annotates it with value types.
    def instance_field_kind(self, declaration, name):
        for statement in declaration.body:
            if isinstance(statement, ast.AnnAssign) and isinstance(statement.target, ast.Name) and statement.target.id == name:
                if statement.value is not None:
                    return self.module_value_kind(statement.value)
                annotation = statement.annotation
                if isinstance(annotation, ast.Name) and isinstance(self.static_binding(self.tree, annotation.id), ast.ClassDef):
                    return ("class", annotation.id)
                return DATA if self.is_value_annotation(annotation) else OPAQUE
            if isinstance(statement, ast.Assign) and any(isinstance(target, ast.Name) and target.id == name for target in statement.targets):
                return self.module_value_kind(statement.value)
        return OPAQUE

    def is_value_annotation(self, annotation):
        for node in ast.walk(annotation):
            if isinstance(node, ast.Constant) and node.value is not None and node.value is not Ellipsis:
                return False
            if isinstance(node, ast.Name) and not self.is_builtin_name(node):
                qualified = self.qualified_import_name(node)
                if qualified is None or not self.is_pure_standard_library_call(qualified):
                    return False
        return True

    def combined_kind(self, kinds):
        resolved_kinds = [kind for kind in kinds if kind != CYCLE]
        if kinds and not resolved_kinds:
            return CYCLE
        kinds = resolved_kinds
        if not kinds or OPAQUE in kinds:
            return OPAQUE
        if all(kind == kinds[0] for kind in kinds):
            return kinds[0]
        if all(kind in (DATA, SDK) for kind in kinds):
            return DATA
        # A value that may be an instance of one module class is checked as that class, which is stricter.
        class_kinds = {kind for kind in kinds if isinstance(kind, tuple)}
        if len(class_kinds) == 1 and all(kind in (DATA, SDK) or isinstance(kind, tuple) for kind in kinds):
            return class_kinds.pop()
        return OPAQUE

    def is_builtin_name(self, name):
        if not isinstance(name, ast.Name) or not hasattr(builtins, name.id):
            return False
        return self.binding_scope(name) is self.tree and name.id not in self.scope_bindings.get(self.tree, {})

    def is_allowed_qualified_value(self, qualified):
        if qualified == "dex" or qualified.startswith("dex."):
            return qualified.rsplit(".", 1)[-1] not in PYTHON_DEX_EXTERNAL_NAMES
        return self.is_pure_standard_library_call(qualified)

    def is_pure_standard_library_call(self, qualified):
        if qualified in PYTHON_PURE_STANDARD_LIBRARY_CALLS:
            return True
        if qualified in PYTHON_DENIED_STANDARD_LIBRARY_CALLS:
            return False
        parts = qualified.split(".")
        return any(".".join(parts[:length]) in PYTHON_PURE_STANDARD_LIBRARY_MODULES for length in range(1, len(parts) + 1))

    # Returns the dotted import path that expression names, including dotted imports such as urllib.parse.
    def qualified_import_name(self, expression):
        if isinstance(expression, ast.Attribute):
            owner = self.qualified_import_name(expression.value)
            return f"{owner}.{expression.attr}" if owner else None
        if not isinstance(expression, ast.Name):
            return None
        scope = self.binding_scope(expression)
        binding = self.static_binding(scope, expression.id)
        if binding is None and expression.id not in self.declared_global_names:
            # import urllib.parse and import urllib.request both bind urllib.
            bindings = self.scope_bindings.get(scope, {}).get(expression.id, [])
            if bindings and all(isinstance(item, ast.alias) and isinstance(self.parents.get(item), ast.Import) and not item.asname for item in bindings):
                return expression.id
        statement = self.parents.get(binding)
        if not isinstance(binding, ast.alias):
            return None
        if isinstance(statement, ast.ImportFrom):
            module = "." * statement.level + (statement.module or "")
            return f"{module}.{binding.name}" if statement.level == 0 else f"{module}:{binding.name}"
        if binding.asname:
            return binding.name
        return binding.name.split(".")[0]
