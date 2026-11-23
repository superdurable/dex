# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import ast


class V2MetadataAnalyzer:
    def preceding_directive_lines(self, line_index):
        directives = []
        lines = self.source.splitlines()
        while line_index > 0 and lines[line_index - 1].lstrip().startswith("#"):
            line = lines[line_index - 1]
            text = line.lstrip()[1:].strip()
            if text.startswith("dex:"):
                directives.append({"text": text, "span": {
                    "startLine": line_index, "startColumn": len(line) - len(line.lstrip()) + 1,
                    "endLine": line_index, "endColumn": len(line) + 1,
                }})
            line_index -= 1
        return list(reversed(directives))

    def view_rpc_fact(self, rpc_name, method):
        first_line = min([method.lineno, *(decorator.lineno for decorator in method.decorator_list)])
        fact = {
            "protocolName": rpc_name, "span": self.span(method),
            "directives": [
                line for line in self.preceding_directive_lines(first_line - 1)
                if line["text"].split(maxsplit=1)[0] == "dex:field"
            ],
        }
        keys = self.view_return_keys(method, frozenset(), wrapped=True)
        if keys is not None:
            fact["outputKeys"] = sorted(keys)
        return fact

    def view_return_keys(self, function, visited, wrapped):
        if not function or function in visited or len(visited) >= 32:
            return None
        visited = visited | {function}
        outputs = [self.view_expression_keys(expression, visited, wrapped)
                   for expression in self.view_return_expressions(function)]
        if outputs and outputs[0] is not None and all(keys == outputs[0] for keys in outputs):
            return outputs[0]
        return None

    def view_return_expressions(self, function):
        if isinstance(function, ast.Lambda):
            yield function.body
            return
        pending = list(function.body)
        while pending:
            node = pending.pop()
            if isinstance(node, ast.Return):
                yield node.value
            elif not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                pending.extend(ast.iter_child_nodes(node))

    def view_expression_keys(self, expression, visited, wrapped):
        expression, _ = self.resolved_value(expression, EvaluationFrame())
        if isinstance(expression, ast.Await):
            return self.view_expression_keys(expression.value, visited, wrapped)
        if isinstance(expression, ast.IfExp):
            left = self.view_expression_keys(expression.body, visited, wrapped)
            right = self.view_expression_keys(expression.orelse, visited, wrapped)
            return left if left == right else None
        if isinstance(expression, ast.Call):
            if wrapped and self.is_dex_reference(expression.func, "RPCResult"):
                return self.view_expression_keys(self.argument_value(expression, "output", 0), visited, wrapped=False)
            return self.view_return_keys(self.view_output_helper(expression.func), visited, wrapped)
        if isinstance(expression, ast.Dict):
            keys = []
            for key in expression.keys:
                value, _ = self.resolved_value(key, EvaluationFrame())
                if not isinstance(value, ast.Constant) or not isinstance(value.value, str):
                    return None
                keys.append(value.value)
            return set(keys)
        return None

    def view_output_helper(self, function):
        if isinstance(function, ast.Attribute) and isinstance(function.value, ast.Name) and function.value.id == "self":
            return self.method(self.flow_class, function.attr)
        resolved, _ = self.resolved_value(function, EvaluationFrame())
        if isinstance(resolved, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            return resolved
        if isinstance(resolved, ast.Name):
            binding = self.static_binding(self.binding_scope(resolved), resolved.id)
            if isinstance(binding, (ast.FunctionDef, ast.AsyncFunctionDef)):
                return binding
        return None

    def start_input_fact(self, step_name, step_class, registration):
        method = self.method(step_class, "execute") if step_class else None
        parameters = [*method.args.posonlyargs, *method.args.args] if method else []
        annotation = parameters[2].annotation if len(parameters) == 3 else None
        fact = {"stepType": step_name, "span": self.span(annotation or method or registration)}
        try:
            fact["input"] = self.start_input_schema(annotation)
        except ValueError as error:
            fact["error"] = str(error)
        return fact

    def start_input_schema(self, annotation, ancestors=frozenset(), depth=0):
        if depth > 32:
            raise ValueError("nesting exceeds 32 levels")
        if annotation is None:
            raise ValueError("execute must declare an input annotation in this file")
        if isinstance(annotation, ast.Constant):
            if annotation.value is None:
                return {"kind": "null"}
            if isinstance(annotation.value, str):
                try:
                    parsed = ast.parse(annotation.value, mode="eval").body
                except SyntaxError:
                    raise ValueError(f"type {annotation.value} cannot be resolved in this file") from None
                return self.start_input_schema(parsed, ancestors, depth + 1)
        if isinstance(annotation, ast.BinOp) and isinstance(annotation.op, ast.BitOr):
            return self.start_input_union([annotation.left, annotation.right], ancestors, depth)
        if isinstance(annotation, ast.Subscript):
            return self.start_input_subscript(annotation, ancestors, depth)
        imported = self.imported_name(annotation)
        if imported == "datetime.datetime":
            return {"kind": "string", "format": "date-time"}
        if isinstance(annotation, ast.Name):
            binding = self.static_binding(self.binding_scope(annotation), annotation.id)
            if isinstance(binding, ast.ClassDef):
                return self.start_input_class(binding, ancestors, depth)
            assignment = self.assignment_statement(self.binding_scope(annotation), binding) if binding else None
            if assignment:
                if binding in ancestors:
                    raise ValueError(f"type {annotation.id} is recursive")
                return self.start_input_schema(assignment.value, ancestors | {binding}, depth + 1)
            if binding is None and annotation.id not in self.scope_bindings.get(self.binding_scope(annotation), {}):
                kind = {"str": "string", "bool": "boolean", "float": "number", "int": "integer"}.get(annotation.id)
                if kind == "integer":
                    return self.start_integer_schema()
                if kind:
                    return {"kind": kind}
        raise ValueError(f"type {self.unparse(annotation)} cannot be resolved in this file")

    def start_input_subscript(self, annotation, ancestors, depth):
        name = self.imported_name(annotation.value) or self.unparse(annotation.value)
        arguments = annotation.slice.elts if isinstance(annotation.slice, ast.Tuple) else [annotation.slice]
        if name == "typing.Optional" and len(arguments) == 1:
            return self.start_input_union([arguments[0], ast.Constant(None)], ancestors, depth)
        if name == "typing.Union":
            return self.start_input_union(arguments, ancestors, depth)
        if name in {"list", "typing.List"} and len(arguments) == 1:
            return {"kind": "array", "items": self.start_input_schema(arguments[0], ancestors, depth + 1)}
        if name in {"dict", "typing.Dict"} and len(arguments) == 2:
            if self.start_input_schema(arguments[0], ancestors, depth + 1) != {"kind": "string"}:
                raise ValueError("map keys must be strings")
            return {"kind": "map", "values": self.start_input_schema(arguments[1], ancestors, depth + 1)}
        if name in {"typing.Literal", "typing_extensions.Literal"}:
            values = [self.static_python_scalar(argument) for argument in arguments]
            if all(known for known, _ in values):
                return self.start_enum_schema([(str(value) or '""', value) for _, value in values])
        raise ValueError(f"type {self.unparse(annotation)} is unsupported")

    def start_input_union(self, arguments, ancestors, depth):
        schemas = [self.start_input_schema(argument, ancestors, depth + 1) for argument in arguments]
        nonnull = [schema for schema in schemas if schema["kind"] != "null"]
        if len(nonnull) == 1 and len(nonnull) < len(schemas):
            return {**nonnull[0], "nullable": True}
        raise ValueError("only unions with None are supported")

    def start_input_class(self, declaration, ancestors, depth):
        if declaration in ancestors:
            raise ValueError(f"type {declaration.name} is recursive")
        bases = {self.imported_name(base) for base in declaration.bases}
        if bases & {"enum.Enum", "enum.IntEnum", "enum.StrEnum"}:
            values = []
            for member in declaration.body:
                if isinstance(member, ast.Assign) and len(member.targets) == 1 and isinstance(member.targets[0], ast.Name):
                    name = member.targets[0].id
                    if not name.startswith("_"):
                        known, value = self.static_python_scalar(member.value)
                        if not known:
                            raise ValueError(f"enum member {name} must have a literal value")
                        values.append((name, value))
            return self.start_enum_schema(values)
        typed_dict = bool(bases & {"typing.TypedDict", "typing_extensions.TypedDict"})
        dataclass = any(
            self.imported_name(decorator.func if isinstance(decorator, ast.Call) else decorator) == "dataclasses.dataclass"
            for decorator in declaration.decorator_list
        )
        if not (typed_dict or dataclass) or len(declaration.bases) > int(typed_dict):
            raise ValueError(f"type {declaration.name} must be a dataclass or TypedDict with fields declared in this file")
        fields = []
        for field in declaration.body:
            if isinstance(field, ast.AnnAssign) and isinstance(field.target, ast.Name):
                field_fact = self.start_input_field(declaration, field, typed_dict, ancestors | {declaration}, depth + 1)
                if field_fact is not None:
                    fields.append(field_fact)
        return {"kind": "object", "fields": fields}

    def start_input_field(self, declaration, field, typed_dict, ancestors, depth):
        annotation = field.annotation
        if typed_dict:
            required = not any(keyword.arg == "total" and isinstance(keyword.value, ast.Constant) and keyword.value.value is False
                               for keyword in declaration.keywords)
        else:
            value = field.value
            required = value is None or (
                isinstance(value, ast.Call) and self.imported_name(value.func) == "dataclasses.field"
                and not any(keyword.arg in {"default", "default_factory"} for keyword in value.keywords)
            )
        if isinstance(annotation, ast.Subscript):
            wrapper = self.imported_name(annotation.value)
            if wrapper == "typing.ClassVar":
                return None
            if typed_dict and wrapper in {"typing.Required", "typing_extensions.Required", "typing.NotRequired", "typing_extensions.NotRequired"}:
                required = wrapper.rsplit(".", 1)[-1] == "Required"
                annotation = annotation.slice
        name = field.target.id
        try:
            return {"name": name, "required": required, "schema": self.start_input_schema(annotation, ancestors, depth)}
        except ValueError as error:
            raise ValueError(f"field {name}: {error}") from None

    def start_enum_schema(self, members):
        types = {type(value) for _, value in members}
        if types == {int}:
            schema = self.start_integer_schema()
        elif types == {str}:
            schema = {"kind": "string"}
        else:
            raise ValueError("enum values must be all strings or all integers")
        values = {}
        for name, value in members:
            values.setdefault(str(value), {"name": name or value, "value": str(value)})
        return {**schema, "enumValues": list(values.values())}

    def start_integer_schema(self):
        return {"kind": "integer", "minimum": "-9223372036854775808", "maximum": "9223372036854775807"}
