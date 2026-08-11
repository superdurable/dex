# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import ast
import re
import sys
from dataclasses import dataclass
from pathlib import Path


PACKAGE = Path("dex")
PUBLIC_DUNDERS = {
    "__aenter__",
    "__aexit__",
    "__call__",
    "__enter__",
    "__exit__",
    "__init__",
    "__iter__",
    "__next__",
}


@dataclass(frozen=True)
class Issue:
    path: Path
    line: int
    message: str


@dataclass(frozen=True)
class Definition:
    path: Path
    source: str
    node: ast.AST


def main() -> int:
    issues = inspect_public_api()
    for issue in issues:
        print(f"{issue.path}:{issue.line}: {issue.message}", file=sys.stderr)
    if issues:
        print(
            f"public API documentation check failed with {len(issues)} issue(s)",
            file=sys.stderr,
        )
        return 1
    print("all hand-written Python SDK public APIs are documented")
    return 0


def inspect_public_api() -> list[Issue]:
    init_path = PACKAGE / "__init__.py"
    init_source = init_path.read_text(encoding="utf-8")
    init_tree = ast.parse(init_source, filename=str(init_path))
    exports = exported_names(init_tree)
    definitions = imported_definitions(init_tree)
    issues: list[Issue] = []
    for name in sorted(exports):
        definition = definitions.get(name)
        if definition is None:
            issues.append(Issue(init_path, 1, f"cannot resolve exported API {name}"))
            continue
        issues.extend(inspect_definition(name, definition))
    return issues


def exported_names(tree: ast.Module) -> set[str]:
    for node in tree.body:
        if not isinstance(node, ast.Assign):
            continue
        if not any(
            isinstance(target, ast.Name) and target.id == "__all__"
            for target in node.targets
        ):
            continue
        if not isinstance(node.value, (ast.List, ast.Tuple)):
            raise ValueError("dex.__all__ must be a literal list or tuple")
        return {
            element.value
            for element in node.value.elts
            if isinstance(element, ast.Constant) and isinstance(element.value, str)
        }
    raise ValueError("dex.__all__ is required")


def imported_definitions(tree: ast.Module) -> dict[str, Definition]:
    definitions: dict[str, Definition] = {}
    for node in tree.body:
        if not isinstance(node, ast.ImportFrom) or node.module is None:
            continue
        module_path = Path(*node.module.split("."))
        path = module_path.with_suffix(".py")
        if not path.exists():
            path = module_path.with_suffix(".pyi")
        source = path.read_text(encoding="utf-8")
        module_tree = ast.parse(source, filename=str(path))
        module_definitions = definitions_by_name(module_tree)
        for imported in node.names:
            exported_name = imported.asname or imported.name
            definition = module_definitions.get(imported.name)
            if definition is not None:
                definitions[exported_name] = Definition(path, source, definition)
    return definitions


def definitions_by_name(tree: ast.Module) -> dict[str, ast.AST]:
    definitions: dict[str, ast.AST] = {}
    for node in tree.body:
        if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            definitions[node.name] = node
        elif isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name):
                    definitions[target.id] = node
        elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
            definitions[node.target.id] = node
    return definitions


def inspect_definition(name: str, definition: Definition) -> list[Issue]:
    node = definition.node
    if isinstance(node, ast.ClassDef):
        return inspect_class(definition)
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        return inspect_function(definition.path, node)
    if isinstance(node, (ast.Assign, ast.AnnAssign)):
        if has_sphinx_attribute_doc(definition.source, node.lineno):
            return []
        return [
            Issue(
                definition.path,
                node.lineno,
                f"exported constant {name} needs a preceding '#:' documentation comment",
            )
        ]
    return [Issue(definition.path, node.lineno, f"unsupported exported API {name}")]


def inspect_class(definition: Definition) -> list[Issue]:
    node = definition.node
    assert isinstance(node, ast.ClassDef)
    issues = require_docstring(definition.path, node, f"class {node.name}")
    class_doc = ast.get_docstring(node, clean=True) or ""
    for member in node.body:
        if isinstance(member, (ast.FunctionDef, ast.AsyncFunctionDef)):
            if is_documented_public_method(member):
                issues.extend(inspect_function(definition.path, member))
        elif isinstance(member, (ast.Assign, ast.AnnAssign)):
            for field_name in assignment_names(member):
                if field_name.startswith("_"):
                    continue
                if not documents_attribute(class_doc, field_name):
                    issues.append(
                        Issue(
                            definition.path,
                            member.lineno,
                            f"class {node.name} docstring must document attribute {field_name}",
                        )
                    )
    for field_name, line in instance_attribute_names(node).items():
        if not documents_attribute(class_doc, field_name):
            issues.append(
                Issue(
                    definition.path,
                    line,
                    f"class {node.name} docstring must document attribute {field_name}",
                )
            )
    return issues


def inspect_function(
    path: Path,
    node: ast.FunctionDef | ast.AsyncFunctionDef,
) -> list[Issue]:
    if has_decorator(node, "overload"):
        return []
    label = f"method {node.name}" if node.col_offset else f"function {node.name}"
    issues = require_docstring(path, node, label)
    doc = ast.get_docstring(node, clean=True)
    if doc is None:
        return issues
    for argument in documented_arguments(node):
        if not documents_argument(doc, argument):
            issues.append(
                Issue(
                    path,
                    node.lineno,
                    f"{label} docstring must document argument {argument}",
                )
            )
    if returns_value(node) and not has_section(doc, "Returns"):
        issues.append(
            Issue(
                path, node.lineno, f"{label} docstring must include a Returns section"
            )
        )
    return issues


def require_docstring(
    path: Path,
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef,
    label: str,
) -> list[Issue]:
    doc = ast.get_docstring(node, clean=True)
    if doc is None:
        return [Issue(path, node.lineno, f"exported {label} has no docstring")]
    summary = next((line.strip() for line in doc.splitlines() if line.strip()), "")
    if not summary.endswith((".", "!", "?")):
        return [Issue(path, node.lineno, f"{label} summary must end with punctuation")]
    return []


def is_documented_public_method(node: ast.FunctionDef | ast.AsyncFunctionDef) -> bool:
    return not node.name.startswith("_") or node.name in PUBLIC_DUNDERS


def documented_arguments(node: ast.FunctionDef | ast.AsyncFunctionDef) -> list[str]:
    arguments = [*node.args.posonlyargs, *node.args.args, *node.args.kwonlyargs]
    names = [
        argument.arg for argument in arguments if argument.arg not in {"self", "cls"}
    ]
    if node.args.vararg is not None:
        names.append(node.args.vararg.arg)
    if node.args.kwarg is not None:
        names.append(node.args.kwarg.arg)
    return names


def documents_argument(doc: str, name: str) -> bool:
    return (
        re.search(
            rf"^\s{{4,}}\*{{0,2}}{re.escape(name)}(?:\s*\([^)]*\))?:\s+\S",
            doc,
            re.MULTILINE,
        )
        is not None
    )


def documents_attribute(doc: str, name: str) -> bool:
    return (
        re.search(
            rf"^\s{{4,}}{re.escape(name)}(?:\s*\([^)]*\))?:\s+\S", doc, re.MULTILINE
        )
        is not None
    )


def has_section(doc: str, name: str) -> bool:
    return re.search(rf"^{re.escape(name)}:\s*$", doc, re.MULTILINE) is not None


def returns_value(node: ast.FunctionDef | ast.AsyncFunctionDef) -> bool:
    if node.name in {"__init__", "__exit__", "__aexit__"}:
        return False
    annotation = node.returns
    if annotation is None:
        return False
    return ast.unparse(annotation) not in {"None", "NoReturn", "Never"}


def assignment_names(node: ast.Assign | ast.AnnAssign) -> list[str]:
    targets = node.targets if isinstance(node, ast.Assign) else [node.target]
    return [target.id for target in targets if isinstance(target, ast.Name)]


def instance_attribute_names(node: ast.ClassDef) -> dict[str, int]:
    attributes: dict[str, int] = {}
    for member in node.body:
        if not isinstance(member, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for descendant in ast.walk(member):
            targets: list[ast.expr] = []
            if isinstance(descendant, ast.Assign):
                targets.extend(descendant.targets)
            elif isinstance(descendant, ast.AnnAssign):
                targets.append(descendant.target)
            for target in targets:
                if (
                    isinstance(target, ast.Attribute)
                    and isinstance(target.value, ast.Name)
                    and target.value.id == "self"
                    and not target.attr.startswith("_")
                ):
                    attributes.setdefault(target.attr, descendant.lineno)
    return attributes


def has_sphinx_attribute_doc(source: str, line: int) -> bool:
    lines = source.splitlines()
    index = line - 2
    while index >= 0 and not lines[index].strip():
        index -= 1
    return index >= 0 and lines[index].lstrip().startswith("#:")


def has_decorator(node: ast.FunctionDef | ast.AsyncFunctionDef, name: str) -> bool:
    return any(
        (isinstance(decorator, ast.Name) and decorator.id == name)
        or (isinstance(decorator, ast.Attribute) and decorator.attr == name)
        for decorator in node.decorator_list
    )


if __name__ == "__main__":
    raise SystemExit(main())
