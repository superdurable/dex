# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""File tools confined to the workspace.

A path's symbolic links are first resolved one hop at a time with
``readlink`` relative to their directory's descriptor, and every hop must stay
inside the workspace. The resolved path is then opened from the workspace one
component at a time with ``O_NOFOLLOW`` opens relative to the parent
directory's descriptor, so a path swapped for a link while a tool runs cannot
lead outside. Each opened descriptor's real path is checked against the
workspace again, and a file with more than one hard link is refused, because
a hard link can name a file outside the workspace.
"""

from __future__ import annotations

import contextlib
import errno
import hashlib
import os
import re
import secrets
import stat
import sys
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

from dex_agents import ToolExecutionError

#: The most text ``read_file`` and ``search_files`` return.
MAX_FILE_TOOL_OUTPUT_CHARACTERS = 100_000
_MAX_SEARCH_MATCHES = 200
_MAX_LISTED_ENTRIES = 1000
_MAX_LINK_HOPS = 40
_MAX_READ_BYTES = 16 * 1024 * 1024
#: The largest file ``edit_file`` changes, as in the TypeScript CLI core.
MAX_EDIT_BYTES = 4 * 1024 * 1024
_DIRECTORY_FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
_READ_FLAGS = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | getattr(os, "O_NONBLOCK", 0)
_CREATE_FLAGS = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC
_LINK_ERRNOS = frozenset({errno.ELOOP, errno.ENOTDIR, errno.EMLINK})


class WorkspaceFiles:
    """Read and change files, refusing every path that leaves the workspace.

    ``..``, an absolute path outside the workspace, a symbolic link anywhere in
    a path, and a file with other hard links are all refused.
    """

    def __init__(self, workspace: Path) -> None:
        """Confine the tools to one directory.

        Args:
            workspace: The workspace directory, which must exist.
        """
        self._root = workspace.resolve(strict=True)
        root_fd = os.open(self._root, _DIRECTORY_FLAGS)
        try:
            # The descriptor's own path is the canonical spelling, also on case-insensitive disks.
            self._canonical_root = _descriptor_path(root_fd) or str(self._root)
        finally:
            os.close(root_fd)
        swapped = Path(str(self._root).swapcase())
        self._is_case_insensitive = swapped != self._root and swapped.exists()

    def read_file(self, path: str, offset: int = 0, limit: int | None = None) -> str:
        """Return a text file's lines.

        Args:
            path: Path relative to the workspace.
            offset: Zero-based first line.
            limit: Most lines to return, or ``None`` for all.

        Returns:
            The lines, cut to ``MAX_FILE_TOOL_OUTPUT_CHARACTERS``.
        """
        text = self._read_text(path)
        lines = text.splitlines(keepends=True)
        selected = lines[offset : None if limit is None else offset + limit]
        return _truncate("".join(selected))

    def write_file(self, path: str, content: str) -> str:
        """Replace a file's content, creating it and its directories when needed.

        The new content goes to a temporary file in the same directory, which
        then replaces the name, so a hard link at the name is replaced rather
        than written through. A symbolic link at the name is refused.

        Args:
            path: Path relative to the workspace.
            content: The complete new text.

        Returns:
            What was written.
        """
        parts = self._parts(path, is_file=True, is_final_link_followed=False)
        self._replace(path, parts, content)
        return f"wrote {len(content.encode('utf-8'))} bytes to {'/'.join(parts)}"

    def edit_file(self, path: str, old_text: str, new_text: str) -> str:
        """Replace the one occurrence of a text in a file.

        Right before the replacement, the name must still be the same file with
        the same content as when it was read; otherwise nothing is written.

        Args:
            path: Path relative to the workspace.
            old_text: Text that occurs exactly once.
            new_text: Its replacement.

        Returns:
            What was changed.
        """
        parts = self._parts(path, is_file=True, is_final_link_followed=False)
        if not old_text:
            raise ToolExecutionError("old_text must not be empty")
        text, read_identity = self._read_text_and_identity(path, MAX_EDIT_BYTES)
        count = text.count(old_text)
        if count == 0:
            raise ToolExecutionError(f"old_text does not occur in {path}")
        if count > 1:
            raise ToolExecutionError(f"old_text occurs {count} times in {path}; it must occur once")
        self._replace(path, parts, text.replace(old_text, new_text, 1), read_identity)
        return f"edited {'/'.join(parts)}: replaced 1 occurrence"

    def read_text_for_preview(self, path: str, limit: int) -> tuple[str, str | None] | None:
        """Read a file's text for an approval preview, with the checks of ``write_file``.

        The path must stay inside the workspace through every link and name a
        regular file with one link, or no file yet. The model call runs it, so
        it never raises.

        Args:
            path: Path relative to the workspace.
            limit: The largest file to read, in bytes.

        Returns:
            The path relative to the workspace and the file's text, the text
            ``None`` when no file has the path yet, or ``None`` when the path
            leaves the workspace, names something else, is larger than
            ``limit``, is not UTF-8, or cannot be read.
        """
        display_path: str | None = None
        try:
            parts = self._parts(path, is_file=True, is_final_link_followed=False)
            display_path = "/".join(parts)
            with self._open_directory(path, parts[:-1]) as directory_fd:
                try:
                    file_fd = os.open(parts[-1], _READ_FLAGS, dir_fd=directory_fd)
                except FileNotFoundError:
                    return display_path, None
            try:
                self._check_opened_file(path, file_fd)
                data = _read_descriptor(file_fd, limit + 1)
            finally:
                os.close(file_fd)
        except ToolExecutionError as error:
            # A directory that does not exist yet means a new file, which write_file creates.
            if display_path is not None and isinstance(error.__cause__, FileNotFoundError):
                return display_path, None
            return None
        except OSError:
            return None
        if len(data) > limit:
            return None
        try:
            return display_path, data.decode("utf-8")
        except UnicodeDecodeError:
            return None

    def list_directory(self, path: str = ".") -> str:
        """List a directory's entries, one per line; directories end in ``/``, links in ``@``.

        Args:
            path: Directory relative to the workspace.

        Returns:
            The sorted entries.
        """
        with self._open_directory(path, self._parts(path, is_file=False)) as directory_fd:
            with os.scandir(directory_fd) as entries:
                lines = sorted(_describe_entry(entry) for entry in entries)
        if len(lines) > _MAX_LISTED_ENTRIES:
            lines = [*lines[:_MAX_LISTED_ENTRIES], f"... {len(lines) - _MAX_LISTED_ENTRIES} more"]
        return "\n".join(lines) if lines else "(empty)"

    def search_files(self, pattern: str, path: str = ".") -> str:
        """Find lines that match a regular expression in the files under a directory.

        Symbolic links and files with other hard links are skipped.

        Args:
            pattern: A Python regular expression.
            path: Directory relative to the workspace.

        Returns:
            ``<path>:<line>: <text>`` per match, at most 200.
        """
        try:
            expression = re.compile(pattern)
        except re.error as error:
            raise ToolExecutionError(f"invalid pattern: {error}") from error
        parts = self._parts(path, is_file=False)
        matches: list[str] = []
        with self._open_directory(path, parts) as start_fd:
            walk = os.fwalk(".", dir_fd=start_fd, follow_symlinks=False)
            for directory, directory_names, file_names, directory_fd in walk:
                directory_names.sort()
                for file_name in sorted(file_names):
                    text = self._read_searched_file(file_name, directory_fd)
                    if text is None:
                        continue
                    relative = PurePosixPath(*parts, directory, file_name)
                    for number, line in enumerate(text.splitlines(), start=1):
                        if expression.search(line):
                            matches.append(f"{relative}:{number}: {line}")
                            if len(matches) >= _MAX_SEARCH_MATCHES:
                                return _truncate("\n".join(matches))
        return _truncate("\n".join(matches)) if matches else "no matches"

    def _read_text(self, path: str) -> str:
        return self._read_text_and_identity(path)[0]

    def _read_text_and_identity(
        self, path: str, limit: int = _MAX_READ_BYTES
    ) -> tuple[str, _FileIdentity]:
        """Read a file and return its text with the identity, size, time, and hash it had.

        With a ``limit`` below the read limit, a larger file is refused rather than cut.
        """
        parts = self._parts(path, is_file=True)
        with self._open_directory(path, parts[:-1]) as directory_fd:
            try:
                file_fd = os.open(parts[-1], _READ_FLAGS, dir_fd=directory_fd)
            except FileNotFoundError as error:
                raise ToolExecutionError(f"{path} is not a file in the workspace") from error
            except OSError as error:
                raise self._refusal(path, error, parts[-1], directory_fd) from error
        try:
            status = self._check_opened_file(path, file_fd)
            data = _read_descriptor(file_fd, limit + 1)
        finally:
            os.close(file_fd)
        if limit < _MAX_READ_BYTES and len(data) > limit:
            raise ToolExecutionError(
                f"{path} is larger than {limit} bytes; edit_file does not change it"
            )
        return data.decode("utf-8", errors="replace"), _identity_of(status, data)

    def _read_searched_file(self, name: str, directory_fd: int) -> str | None:
        """Read one file for ``search_files``, or ``None`` for a file it must skip."""
        try:
            file_fd = os.open(name, _READ_FLAGS, dir_fd=directory_fd)
        except OSError:
            return None
        try:
            try:
                self._check_opened_file(name, file_fd)
            except ToolExecutionError:
                return None
            data = _read_descriptor(file_fd)
        finally:
            os.close(file_fd)
        try:
            return data.decode("utf-8")
        except UnicodeDecodeError:
            return None

    def _replace(
        self,
        path: str,
        parts: list[str],
        content: str,
        expected: _FileIdentity | None = None,
    ) -> None:
        """Write a temporary file in the checked directory and rename it over the name.

        Every step uses the directory's descriptor, so the rename lands in the
        directory that was checked even if its path changes meanwhile.
        """
        with self._open_directory(path, parts[:-1], is_created=True) as directory_fd:
            name = parts[-1]
            with contextlib.suppress(FileNotFoundError):
                existing = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
                if stat.S_ISDIR(existing.st_mode):
                    raise ToolExecutionError(f"{path} is a directory")
                if stat.S_ISLNK(existing.st_mode):
                    raise ToolExecutionError(
                        f"{path} is outside the workspace {self._root}: it is a symbolic link"
                    )
            temporary = f".{name}.dex-{secrets.token_hex(4)}"
            file_fd = os.open(temporary, _CREATE_FLAGS, 0o644, dir_fd=directory_fd)
            try:
                _write_descriptor(file_fd, content.encode("utf-8"))
                written = os.fstat(file_fd)
                if expected is not None:
                    self._check_unchanged(path, name, directory_fd, expected)
                # The directory may have moved since it was opened; check it before renaming.
                self._check_inside(path, directory_fd)
                os.rename(temporary, name, src_dir_fd=directory_fd, dst_dir_fd=directory_fd)
            except BaseException:
                with contextlib.suppress(OSError):
                    os.unlink(temporary, dir_fd=directory_fd)
                raise
            finally:
                os.close(file_fd)
            self._check_renamed(path, name, directory_fd, written)

    def _check_unchanged(
        self, path: str, name: str, directory_fd: int, expected: _FileIdentity
    ) -> None:
        conflict = f"{path} changed after edit_file read it; read the file again and retry the edit"
        try:
            current_fd = os.open(name, _READ_FLAGS, dir_fd=directory_fd)
        except OSError as error:
            raise ToolExecutionError(conflict) from error
        try:
            status = os.fstat(current_fd)
            data = _read_descriptor(current_fd, expected.size + 1)
        finally:
            os.close(current_fd)
        if _identity_of(status, data) != expected:
            raise ToolExecutionError(conflict)

    def _check_renamed(
        self, path: str, name: str, directory_fd: int, written: os.stat_result
    ) -> None:
        """Fail loudly unless the name now holds exactly the file this call wrote."""
        self._check_inside(path, directory_fd)
        renamed = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        if (renamed.st_dev, renamed.st_ino) != (written.st_dev, written.st_ino):
            raise ToolExecutionError(
                f"{path} was replaced by another file right after it was written; check it"
            )

    @contextlib.contextmanager
    def _open_directory(
        self, path: str, parts: list[str], *, is_created: bool = False
    ) -> Iterator[int]:
        """Open a workspace directory one component at a time, never following a link."""
        directory_fd = os.open(self._root, _DIRECTORY_FLAGS)
        try:
            for part in parts:
                try:
                    next_fd = os.open(part, _DIRECTORY_FLAGS, dir_fd=directory_fd)
                except FileNotFoundError as error:
                    if not is_created:
                        raise ToolExecutionError(f"{path} is not in the workspace") from error
                    os.mkdir(part, 0o755, dir_fd=directory_fd)
                    next_fd = os.open(part, _DIRECTORY_FLAGS, dir_fd=directory_fd)
                except OSError as error:
                    raise self._refusal(path, error, part, directory_fd) from error
                os.close(directory_fd)
                directory_fd = next_fd
            self._check_inside(path, directory_fd)
            yield directory_fd
        finally:
            os.close(directory_fd)

    def _check_opened_file(self, path: str, file_fd: int) -> os.stat_result:
        status = os.fstat(file_fd)
        if not stat.S_ISREG(status.st_mode):
            raise ToolExecutionError(f"{path} is not a regular file")
        if status.st_nlink > 1:
            raise ToolExecutionError(
                f"{path} has other hard links, which may be outside the workspace; it is refused"
            )
        self._check_inside(path, file_fd)
        return status

    def _check_inside(self, path: str, fd: int) -> None:
        opened = _descriptor_path(fd)
        if opened is None:
            return
        root = self._canonical_root
        if opened != root and not opened.startswith(root.rstrip("/") + "/"):
            raise ToolExecutionError(f"{path} is outside the workspace {self._root}")

    def _parts(self, path: str, *, is_file: bool, is_final_link_followed: bool = True) -> list[str]:
        """Return a path's real components below the workspace, refusing every way out.

        Symbolic links are resolved one hop at a time with ``readlink``
        relative to their directory's descriptor, and every hop must stay
        inside the workspace. ``..`` may not climb above it. With
        ``is_final_link_followed`` false, a link as the last component is left
        as it is, so a writer can refuse it.
        """
        if "\x00" in path:
            raise ToolExecutionError("the path contains a NUL character")
        candidate = PurePosixPath(path)
        relative = self._relative_to_root(candidate) if candidate.is_absolute() else candidate
        if relative is None:
            raise ToolExecutionError(f"{path} is outside the workspace {self._root}")
        parts = self._resolve_links(path, list(relative.parts), is_final_link_followed)
        if is_file and not parts:
            raise ToolExecutionError(f"{path} is a directory")
        return parts

    def _resolve_links(
        self, path: str, pending: list[str], is_final_link_followed: bool
    ) -> list[str]:
        resolved: list[str] = []
        hops = 0
        while pending:
            part = pending.pop(0)
            if part in ("", "."):
                continue
            if part == "..":
                if not resolved:
                    raise ToolExecutionError(f"{path} is outside the workspace {self._root}")
                resolved.pop()
                continue
            with self._open_directory(path, resolved) as directory_fd:
                try:
                    status = os.stat(part, dir_fd=directory_fd, follow_symlinks=False)
                except FileNotFoundError:
                    return self._append_missing(path, resolved, [part, *pending])
                is_last = not pending
                if not stat.S_ISLNK(status.st_mode) or (is_last and not is_final_link_followed):
                    resolved.append(part)
                    continue
                target = os.readlink(part, dir_fd=directory_fd)
            hops += 1
            if hops > _MAX_LINK_HOPS:
                raise ToolExecutionError(f"{path} has too many symbolic links")
            target_path = PurePosixPath(target)
            if target_path.is_absolute():
                inside = self._relative_to_root(target_path)
                if inside is None:
                    raise ToolExecutionError(
                        f"{path} is outside the workspace {self._root}: "
                        "a symbolic link leads outside"
                    )
                resolved = []
                pending = [*inside.parts, *pending]
            else:
                pending = [*target_path.parts, *pending]
        return resolved

    def _append_missing(self, path: str, resolved: list[str], rest: list[str]) -> list[str]:
        """Return the components of a path whose remaining components do not exist yet."""
        parts = list(resolved)
        for part in rest:
            if part == "..":
                if not parts:
                    raise ToolExecutionError(f"{path} is outside the workspace {self._root}")
                parts.pop()
            elif part not in ("", "."):
                parts.append(part)
        return parts

    def _relative_to_root(self, absolute: PurePosixPath) -> PurePosixPath | None:
        """Return an absolute path below the workspace relative to it, or ``None`` outside.

        The workspace may be spelled by its real path, its canonical case on a
        case-insensitive disk, or a path whose directories resolve to it.
        """
        roots = (self._root.as_posix(), self._canonical_root)
        for root in roots:
            with contextlib.suppress(ValueError):
                return absolute.relative_to(root)
        if self._is_case_insensitive:
            text = absolute.as_posix()
            for root in roots:
                prefix = root.rstrip("/") + "/"
                if text.lower() == root.lower():
                    return PurePosixPath(".")
                if text.lower().startswith(prefix.lower()):
                    return PurePosixPath(text[len(prefix) :])
        # Directories outside the workspace, such as /var on macOS, may be links to it.
        parent = Path(absolute.parent).resolve()
        for root_path in (self._root, Path(self._canonical_root)):
            with contextlib.suppress(ValueError):
                return PurePosixPath(parent.relative_to(root_path)) / absolute.name
        return None

    def _refusal(
        self, path: str, error: OSError, name: str, directory_fd: int
    ) -> ToolExecutionError:
        if error.errno in _LINK_ERRNOS:
            try:
                is_link = stat.S_ISLNK(
                    os.stat(name, dir_fd=directory_fd, follow_symlinks=False).st_mode
                )
            except OSError:
                is_link = False
            if is_link:
                return ToolExecutionError(
                    f"{path} is outside the workspace {self._root}: it goes through a symbolic link"
                )
            if error.errno == errno.ENOTDIR:
                return ToolExecutionError(f"{path} goes through {name}, which is not a directory")
        return ToolExecutionError(f"cannot open {path}: {error.strerror}")


@dataclass(frozen=True)
class _FileIdentity:
    device: int
    inode: int
    size: int
    modified_ns: int
    content_sha256: str


def _identity_of(status: os.stat_result, data: bytes) -> _FileIdentity:
    return _FileIdentity(
        device=status.st_dev,
        inode=status.st_ino,
        size=status.st_size,
        modified_ns=status.st_mtime_ns,
        content_sha256=hashlib.sha256(data).hexdigest(),
    )


def _read_descriptor(file_fd: int, limit: int = _MAX_READ_BYTES) -> bytes:
    with os.fdopen(file_fd, "rb", closefd=False) as file:
        return file.read(limit)


def _write_descriptor(file_fd: int, data: bytes) -> None:
    view = memoryview(data)
    while view:
        written = os.write(file_fd, view)
        view = view[written:]


def _describe_entry(entry: os.DirEntry[str]) -> str:
    if entry.is_symlink():
        return f"{entry.name}@"
    return entry.name + ("/" if entry.is_dir(follow_symlinks=False) else "")


def _descriptor_path(fd: int) -> str | None:
    """Return the real path the kernel reports for an open descriptor, or ``None``."""
    if sys.platform == "darwin":
        import fcntl

        path = fcntl.fcntl(fd, fcntl.F_GETPATH, bytes(1024))
        return os.fsdecode(path.split(b"\x00", 1)[0])
    if sys.platform.startswith("linux"):
        return os.readlink(f"/proc/self/fd/{fd}")
    return None


def _truncate(text: str) -> str:
    if len(text) <= MAX_FILE_TOOL_OUTPUT_CHARACTERS:
        return text
    omitted = len(text) - MAX_FILE_TOOL_OUTPUT_CHARACTERS
    return f"{text[:MAX_FILE_TOOL_OUTPUT_CHARACTERS]}\n... ({omitted} more characters)"
