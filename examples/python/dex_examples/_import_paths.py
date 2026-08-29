# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Maps dex_examples.* imports onto kebab-case example directories."""

from __future__ import annotations

import importlib.abc
import importlib.util
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
CATEGORIES = frozenset({"products", "patterns", "primitives", "shared"})


def _segment_to_dir(name: str) -> str:
    return name.replace("_", "-")


def _example_dir(category: str, segments: list[str]) -> Path:
    directory = ROOT / category
    for segment in segments:
        directory /= _segment_to_dir(segment)
    return directory


def _resolve_module(fullname: str) -> tuple[Path, bool] | None:
    parts = fullname.split(".")
    if len(parts) < 3 or parts[0] != "dex_examples":
        return None
    category = parts[1]
    if category not in CATEGORIES:
        return None

    if len(parts) == 3:
        package_dir = _example_dir(category, [parts[2]])
        if package_dir.is_dir():
            return package_dir, True
        return None

    module_file = _example_dir(category, parts[2:-1]) / f"{parts[-1]}.py"
    if module_file.is_file():
        return module_file, False

    package_dir = _example_dir(category, parts[2:])
    if package_dir.is_dir():
        return package_dir, True
    return None


class ExampleModuleFinder(importlib.abc.MetaPathFinder):
    def find_spec(
        self,
        fullname: str,
        path: object | None = None,
        target: object | None = None,
    ) -> importlib.machinery.ModuleSpec | None:
        resolved = _resolve_module(fullname)
        if resolved is None:
            return None
        module_path, is_package = resolved
        if is_package:
            init_path = module_path / "__init__.py"
            if not init_path.is_file():
                init_path.write_text("")
            return importlib.util.spec_from_file_location(
                fullname,
                init_path,
                submodule_search_locations=[str(module_path)],
            )
        return importlib.util.spec_from_file_location(fullname, module_path)


def install() -> None:
    if not any(isinstance(finder, ExampleModuleFinder) for finder in sys.meta_path):
        sys.meta_path.insert(0, ExampleModuleFinder())
