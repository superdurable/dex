# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import importlib.machinery
import pathlib


class ConnectorAnalyzer:
    def initialize_connectors(self):
        self.connector_imports = {}
        self.connector_steps = {}
        self.connector_variables = {}
        self.connector_bindings = []
        self.connector_constants = {}

    def index_connector_imports(self):
        for statement in self.tree.body:
            if isinstance(statement, ast.ImportFrom) and statement.module and statement.module != "dex":
                for imported in statement.names:
                    self.load_connector_import(statement.module, imported.name, imported.asname or imported.name, statement)
            elif isinstance(statement, ast.Import):
                for imported in statement.names:
                    self.load_connector_import(imported.name, "", imported.asname or imported.name, statement)

    def load_connector_import(self, module_name, export_name, alias, statement):
        search_paths = [str(pathlib.Path(self.path).parent), *sys.path]
        directories = []
        for component in module_name.split("."):
            specification = importlib.machinery.PathFinder.find_spec(component, search_paths)
            if specification is None:
                return
            if specification.submodule_search_locations is None:
                if specification.origin:
                    directories.append(pathlib.Path(specification.origin).parent)
                break
            search_paths = list(specification.submodule_search_locations)
            directories.extend(pathlib.Path(directory) for directory in search_paths)
        for directory in reversed(directories):
            manifest_path = directory / "dex-connector.json"
            if not manifest_path.is_file():
                continue
            try:
                manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
                for factory in manifest.get("factories", []):
                    if not isinstance(factory, dict) or not isinstance(factory.get("name"), str):
                        raise ValueError("factory requires a name")
                    name = factory["name"]
                    if export_name and name == export_name:
                        self.connector_imports[alias] = (str(manifest_path), factory)
                    elif not export_name:
                        self.connector_imports[f"{alias}.{name}"] = (str(manifest_path), factory)
            except (OSError, ValueError, TypeError, AttributeError) as error:
                self.error("connector_manifest", f"cannot read Connector manifest: {error}", self.span(statement))
            return

    def index_connector_fields(self, flow_class):
        statements = [*self.tree.body, *flow_class.body]
        initializer = self.method(flow_class, "__init__")
        if initializer:
            statements.extend(initializer.body)
        for statement in statements:
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1:
                target, value = statement.targets[0], statement.value
            elif isinstance(statement, ast.AnnAssign):
                target, value = statement.target, statement.value
            else:
                continue
            self.connector_constants[self.unparse(target)] = value
        for name, expression in self.connector_constants.items():
            use = self.connector_factory_use(expression)
            if use is None or use[1].get("kind") == "trigger":
                continue
            step_type = self.connector_static_string(use[2].get(use[1].get("fields", {}).get("stepType")))
            if not step_type:
                self.error("connector_factory_step_type", "Connector factory requires a non-empty static Step type", self.span(expression))
                continue
            self.connector_steps[step_type] = use
            self.connector_variables[name] = step_type
        binding_method = self.method(flow_class, "get_connector_trigger_bindings")
        if binding_method:
            if len(binding_method.body) != 1 or not isinstance(binding_method.body[0], ast.Return) or not isinstance(binding_method.body[0].value, (ast.List, ast.Tuple)):
                self.error("connector_trigger_bindings_dynamic", "Connector Trigger bindings must be returned as one static list", self.span(binding_method))
            else:
                for expression in binding_method.body[0].value.elts:
                    use = self.connector_factory_use(expression)
                    if use and use[1].get("kind") == "trigger":
                        self.connector_bindings.append(use)
                    else:
                        self.error("connector_trigger_binding_invalid", "Connector Trigger binding must call a manifest factory", self.span(expression))

    def connector_factory_use(self, expression):
        if not isinstance(expression, ast.Call):
            return None
        identity = self.connector_imports.get(self.unparse(expression.func))
        if identity is None:
            return None
        fields = {}
        if len(expression.args) == 1:
            argument = expression.args[0]
            if isinstance(argument, ast.Call):
                fields = {keyword.arg: keyword.value for keyword in argument.keywords if keyword.arg}
            elif isinstance(argument, ast.Dict) and all(isinstance(key, ast.Constant) and isinstance(key.value, str) for key in argument.keys):
                fields = {key.value: value for key, value in zip(argument.keys, argument.values)}
            else:
                self.error("connector_factory_config", "Connector config must be an inline object or named arguments", self.span(expression))
        elif expression.args:
            self.error("connector_factory_config", "Connector config must use named arguments", self.span(expression))
        for keyword in expression.keywords:
            if keyword.arg is None or keyword.arg in fields:
                self.error("connector_factory_config", "Connector config cannot use spreads or duplicate fields", self.span(expression))
            else:
                fields[keyword.arg] = keyword.value
        return (*identity, fields, expression)

    def connector_step_target(self, expression):
        variable = self.unparse(expression)
        if variable in self.connector_variables:
            return self.connector_variables[variable]
        if isinstance(expression, ast.Call):
            use = self.connector_factory_use(expression)
            if use and use[1].get("kind") != "trigger":
                step_type = self.connector_static_string(use[2].get(use[1].get("fields", {}).get("stepType")))
                if step_type:
                    self.connector_steps[step_type] = use
                    return step_type
        return None

    def connector_static_string(self, expression):
        if isinstance(expression, ast.Constant) and isinstance(expression.value, str):
            return expression.value
        if isinstance(expression, ast.Name):
            return self.module_constant(expression.id)
        return None

    def connector_value(self, expression):
        if expression is None:
            return {}
        text = self.connector_static_string(expression)
        if text is not None:
            return {"literal": text}
        try:
            return {"literal": ast.literal_eval(expression)}
        except (ValueError, TypeError):
            pass
        resource = self.resource_for(expression, "")
        if resource:
            return {"resource": resource}
        target = self.step_target(expression)
        if target:
            return {"target": self.registered.get(target, f"step:{target}")}
        if isinstance(expression, ast.Call):
            name = self.symbol(expression.func).rsplit(".", 1)[-1]
            if name in {"step_ref", "StepRef"} and len(expression.args) == 1:
                step_type = self.connector_static_string(expression.args[0])
                if step_type:
                    return {"target": next((node["id"] for node in self.graph["nodes"] if node.get("kind") == "step" and node["name"] == step_type), f"step:{step_type}")}
            if name in {"go_to", "GoTo"}:
                return {"call": name, "arguments": [self.connector_value(argument) for argument in expression.args]}
        return {}

    def connector_analysis_result(self, graph):
        annotations = []
        factories = []
        for step_name, node_id in self.registered.items():
            if step_name in self.connector_steps:
                factories.append(self.connector_use_result(node_id, self.connector_steps[step_name]))
                continue
            step_class = self.step_classes[step_name]
            first_line = min([step_class.lineno, *(decorator.lineno for decorator in step_class.decorator_list)]) - 1
            directives = []
            lines = self.source.splitlines()
            while first_line > 0 and lines[first_line - 1].lstrip().startswith("#"):
                text = lines[first_line - 1].lstrip()[1:].strip()
                if text.startswith("dex:"):
                    directives.insert(0, text)
                first_line -= 1
            annotations.append({"stepId": node_id, "directives": directives, "span": self.span(step_class)})
        factories.extend(self.connector_use_result("", use) for use in self.connector_bindings)
        return {"graph": graph, "annotations": annotations, "factories": factories}

    def connector_use_result(self, node_id, use):
        manifest_path, factory, fields, expression = use
        return {"stepId": node_id, "manifestPath": manifest_path, "factoryName": factory["name"], "fields": {name: self.connector_value(value) for name, value in fields.items()}, "span": self.span(expression)}
