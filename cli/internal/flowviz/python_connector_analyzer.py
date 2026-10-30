# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import ast
import importlib.machinery
import json
import os
import pathlib
import site
import sys


def installed_package_directories():
    # The analyzer runs with -S, so site never ran; locate site-packages without executing .pth or sitecustomize code.
    executable_directory = os.path.dirname(os.path.abspath(sys.executable))
    configuration_paths = [os.path.join(directory, "pyvenv.cfg") for directory in (executable_directory, os.path.dirname(executable_directory))]
    configuration_path = next((path for path in configuration_paths if os.path.isfile(path)), None)
    if configuration_path is None:
        site_directories = site.getsitepackages()
    else:
        site_directories = site.getsitepackages([os.path.dirname(configuration_path)])
        if virtual_environment_includes_system_packages(configuration_path):
            site_directories.extend(site.getsitepackages([sys.base_prefix, sys.base_exec_prefix]))
    directories = []
    for site_directory in site_directories:
        if not os.path.isdir(site_directory) or site_directory in directories:
            continue
        directories.append(site_directory)
        for path_directory in path_configuration_directories(site_directory):
            if path_directory not in directories:
                directories.append(path_directory)
    return directories


def virtual_environment_includes_system_packages(configuration_path):
    try:
        lines = pathlib.Path(configuration_path).read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError):
        return False
    for line in lines:
        key, separator, value = line.partition("=")
        if separator and key.strip().lower() == "include-system-site-packages":
            return value.strip().lower() == "true"
    return False


def path_configuration_directories(site_directory):
    # Mirrors site.addpackage for plain directory lines and never executes "import" lines.
    try:
        names = sorted(name for name in os.listdir(site_directory) if name.endswith(".pth") and not name.startswith("."))
    except OSError:
        return []
    directories = []
    for name in names:
        try:
            lines = pathlib.Path(site_directory, name).read_text(encoding="utf-8-sig").splitlines()
        except (OSError, UnicodeDecodeError):
            continue
        for line in lines:
            if line.startswith("#") or not line.strip() or line.startswith(("import ", "import\t")):
                continue
            directory = os.path.join(site_directory, line.rstrip())
            if os.path.isdir(directory):
                directories.append(directory)
    return directories


def validate_connector_manifest_shape(manifest):
    if not isinstance(manifest, dict):
        raise ValueError("manifest must be a JSON object")
    factories = manifest.get("factories")
    if not isinstance(factories, list):
        raise ValueError("factories must be an array")
    for index, factory in enumerate(factories):
        if not isinstance(factory, dict):
            raise ValueError(f"factories[{index}] must be an object")
        if not isinstance(factory.get("name"), str) or not factory["name"]:
            raise ValueError(f"factories[{index}].name must be a non-empty string")
        if not isinstance(factory.get("kind"), str):
            raise ValueError(f"factories[{index}].kind must be a string")
        fields = factory.get("fields")
        if not isinstance(fields, dict) or not all(isinstance(value, str) for value in fields.values()):
            raise ValueError(f"factories[{index}].fields must be an object of strings")
        branches = factory.get("branches", [])
        if not isinstance(branches, list) or not all(isinstance(branch, dict) for branch in branches):
            raise ValueError(f"factories[{index}].branches must be an array of objects")


class ConnectorAnalyzer:
    def initialize_connectors(self):
        self.connector_imports = {}
        self.connector_missing_imports = {}
        self.connector_steps = {}
        self.connector_variables = {}
        self.connector_bindings = []
        self.connector_constants = {}
        self.connector_search_paths = [*sys.path, *installed_package_directories()]

    def index_connector_imports(self):
        for statement in self.tree.body:
            if isinstance(statement, ast.ImportFrom) and statement.module and statement.module != "dex":
                for imported in statement.names:
                    self.load_connector_import(statement.module, statement.level, imported.name, imported.asname or imported.name, statement)
            elif isinstance(statement, ast.Import):
                for imported in statement.names:
                    self.load_connector_import(imported.name, 0, "", imported.asname or imported.name, statement)

    def load_connector_import(self, module_name, level, export_name, alias, statement):
        package_directories = [pathlib.Path(self.path).parent, *pathlib.Path(self.path).parent.parents]
        if level > len(package_directories):
            return
        if level:
            search_paths = [str(package_directories[level - 1])]
        else:
            search_paths = [str(package_directories[0]), *self.connector_search_paths]
        imported_name = alias if export_name else f"{alias}."
        directories = []
        for component in module_name.split("."):
            specification = importlib.machinery.PathFinder.find_spec(component, search_paths)
            if specification is None:
                self.connector_missing_imports[imported_name] = f"module {module_name} cannot be found by the selected Python interpreter {sys.executable}"
                return
            if specification.submodule_search_locations is None:
                break
            search_paths = list(specification.submodule_search_locations)
            directories.extend(pathlib.Path(directory) for directory in search_paths)
        for directory in reversed(directories):
            manifest_path = directory / "dex-connector.json"
            if not manifest_path.is_file():
                continue
            try:
                manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
                validate_connector_manifest_shape(manifest)
                for factory in manifest["factories"]:
                    name = factory["name"]
                    if export_name and name == export_name:
                        self.connector_imports[alias] = (str(manifest_path), factory)
                    elif not export_name:
                        self.connector_imports[f"{alias}.{name}"] = (str(manifest_path), factory)
            except (OSError, ValueError) as error:
                self.error("connector_manifest", f"cannot read Connector manifest {manifest_path}: {error}", self.span(statement))
            return
        expected = directories[-1] / "dex-connector.json" if directories else None
        if expected is None:
            self.connector_missing_imports[imported_name] = f"module {module_name} is not a package, so it cannot ship dex-connector.json"
        else:
            self.connector_missing_imports[imported_name] = f"package {module_name} has no Connector manifest at {expected}"

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
            step_type = self.connector_static_string(use[2].get(use[1]["fields"].get("stepType")))
            if not step_type:
                self.error("connector_factory_step_type", "Connector factory requires a non-empty static Step type", self.span(expression))
                continue
            self.record_connector_step(step_type, use)
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
                        self.report_missing_connector_manifest(expression)
                        self.error("connector_trigger_binding_invalid", "Connector Trigger binding must call a manifest factory", self.span(expression))

    def record_connector_step(self, step_type, use):
        previous = self.connector_steps.get(step_type)
        if previous is not None and previous[3] is not use[3]:
            self.error("duplicate_step_type", f"Connector factories define Step type {step_type} more than once", self.span(use[3]))
            return
        self.connector_steps[step_type] = use

    def report_missing_connector_manifest(self, expression):
        expression = self.connector_constants.get(self.unparse(expression), expression)
        if not isinstance(expression, ast.Call):
            return
        function_name = self.unparse(expression.func)
        message = self.connector_missing_imports.get(function_name) or self.connector_missing_imports.get(function_name.rsplit(".", 1)[0] + ".")
        if message:
            self.error("connector_manifest_missing", f"{function_name} is not a manifest Connector factory: {message}", self.span(expression))

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
                step_type = self.connector_static_string(use[2].get(use[1]["fields"].get("stepType")))
                if step_type:
                    self.record_connector_step(step_type, use)
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
            if name in {"go_to", "GoTo"} and expression.args:
                return {"call": name, "arguments": [self.connector_value(expression.args[0])]}
        return {}

    def connector_analysis_result(self, graph):
        annotations = []
        factories = []
        for step_name, node_id in self.registered.items():
            if step_name in self.connector_steps:
                factories.append(self.connector_use_result(node_id, self.connector_steps[step_name]))
                continue
            step_class = self.step_instances[step_name].declaration
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
