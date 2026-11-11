# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""``LocalDirectoryMediaStore``: image bytes of agent sessions in a local directory.

Use it when every client and Worker of a session runs on one machine, such as
a command-line tool in local mode. Use ``S3MediaStore`` from ``dex_agents.s3``
when they run on different machines.

Example:
    >>> store = LocalDirectoryMediaStore(Path.home() / ".my-agent" / "media")
    >>> reference = await store.put_image(Path("chart.png").read_bytes())
"""

from __future__ import annotations

import asyncio
import contextlib
import errno
import os
import posixpath
import secrets
import stat
import time
from urllib.parse import quote

from dex_agents.media import (
    MediaStoreError,
    build_image_reference,
    require_short_image_uris,
    require_store_image_reference,
    verify_image_bytes,
)
from dex_agents.values import AgentImageReference

__all__ = ["LocalDirectoryMediaStore"]

_IMAGE_DIRECTORY_NAME = "sha256"
_DIRECTORY_MODE = 0o700
_FILE_MODE = 0o600
# Errors of an O_NOFOLLOW | O_NONBLOCK open that name a symbolic link or a non-regular file.
_NOT_REGULAR_FILE_ERRNOS = frozenset({errno.ELOOP, errno.EISDIR, errno.ENXIO, errno.EOPNOTSUPP})
_NOT_DIRECTORY_ERRNOS = frozenset({errno.ELOOP, errno.ENOTDIR})
# Opens to try, and seconds between them, while concurrent puts replace the file.
_OPEN_ATTEMPTS = 10
_OPEN_RETRY_DELAY = 0.005


class LocalDirectoryMediaStore:
    """Keep image bytes in a local directory, one file per distinct image.

    The file of an image is ``<root>/sha256/<sha256>``, and a reference's
    ``uri`` is ``file://<root>/sha256/<sha256>``, where ``<root>`` is the
    absolute root with every UTF-8 byte other than ASCII letters, digits,
    ``-``, ``.``, ``_``, ``~``, and ``/`` written as ``%XX`` in uppercase hex.
    The root is made absolute as text: ``.`` and ``..`` segments and repeated
    or trailing ``/`` characters are resolved without reading the file system,
    symbolic links are not resolved, and Unicode is not normalized. A client
    and a Worker that pass the same root therefore build the same references.

    ``put_image`` copies the bytes into a temporary file in the same directory,
    syncs it, and renames it into place, so a reader never sees a partial
    image. An existing file with the same bytes is success, so retried and
    concurrent uploads of the same bytes leave one file; an existing file with
    other bytes is replaced. Directories the store creates get mode ``0700``
    and image files mode ``0600``, narrowed further by the process umask.
    When ``put_image`` writes the image, it syncs the file before the rename.
    Before every ``put_image`` returns, including when an identical file
    already exists and nothing is written, it syncs the ``sha256`` directory,
    the root, and the root's parent. It also syncs the parent of each directory
    it creates. Directories above the root's parent must already be durable: a
    concurrent creator of a deeper missing ancestor is out of scope.

    The store follows no symbolic link under the root: it opens every path
    relative to an open directory, and refuses a ``sha256`` directory or an
    image file that is a symbolic link or not a regular file, and an image file
    with more than one hard link, with ``MediaStoreError``, on both upload and
    read. The root itself may be a symbolic link. ``get_image`` reads only
    references under this store's root, refuses a file whose size differs from
    the reference before reading it, reads at most one byte more than
    ``size_bytes``, and checks the bytes with ``verify_image_bytes``. It needs
    a POSIX file system; on Windows the constructor raises.

    Session deletion does not delete files. A runner reads a session's images
    again whenever a model call replays them, so keep every file at least as
    long as any session that references it can still run a turn or replay its
    history. A missing file makes those turns fail with ``MediaStoreError``.

    Example:
        >>> store = LocalDirectoryMediaStore("/var/lib/my-agent/media")
        >>> reference = await store.put_image(screenshot_png)
        >>> reference.uri
        'file:///var/lib/my-agent/media/sha256/9f2c...'

    .. versionadded:: 0.5.0
    """

    def __init__(self, root: str | os.PathLike[str]) -> None:
        """Create a store; it touches no file until the first upload or read.

        Args:
            root: Absolute path of the directory that holds the images, such
                as ``"/var/lib/my-agent/media"``. The first upload creates it
                and any missing parent directory with mode ``0700``.

        Raises:
            ValueError: If ``root`` is not absolute, is not well-formed
                Unicode, contains NUL, is the file system root, or makes image
                URIs longer than ``MAX_IMAGE_URI_LENGTH``.
            TypeError: If ``root`` is a bytes path.
            NotImplementedError: On a system without POSIX file access, such
                as Windows.
        """
        if os.name != "posix" or not hasattr(os, "O_NOFOLLOW"):
            raise NotImplementedError("LocalDirectoryMediaStore needs a POSIX file system")
        path = os.fspath(root)
        if not isinstance(path, str):
            raise TypeError("LocalDirectoryMediaStore root must be a str path")
        self._root = _absolute_root(path)
        self._store_uri = "file://" + quote(self._root, safe="/")
        require_short_image_uris(self._store_uri)

    def __repr__(self) -> str:
        return f"LocalDirectoryMediaStore({self._store_uri!r})"

    @property
    def root(self) -> str:
        """Return the absolute root directory of the store.

        Returns:
            The root as given, made absolute as text, without a trailing ``/``.
        """
        return self._root

    @property
    def store_uri(self) -> str:
        """Return the location that every reference of this store starts with.

        Returns:
            ``"file://<root>"``, with the root percent-encoded.
        """
        return self._store_uri

    def image_path(self, sha256: str) -> str:
        """Return the path of the file of the image with this SHA-256.

        Args:
            sha256: SHA-256 of the bytes as 64 lowercase hex digits.

        Returns:
            ``"<root>/sha256/<sha256>"``.
        """
        return f"{self._root}/{_IMAGE_DIRECTORY_NAME}/{sha256}"

    async def put_image(self, data: bytes) -> AgentImageReference:
        """Store one image unless the directory already has it, and return its reference.

        Args:
            data: PNG, JPEG, GIF, or WebP bytes, at most ``MAX_IMAGE_BYTES``.

        Returns:
            The reference.

        Raises:
            ImageValidationError: If the bytes are not a valid image for
                ``build_image_reference``. Nothing is written.
            MediaStoreError: If the ``sha256`` directory or the image's file
                is a symbolic link or not a directory or regular file, or the
                image's file has more than one hard link.
            OSError: If the file system refuses a write, such as for missing
                permissions or a full disk. Retrying is safe.
        """
        reference = build_image_reference(data, self._store_uri)
        await asyncio.to_thread(self._write_image, reference, bytes(data))
        return reference

    async def get_image(self, reference: AgentImageReference) -> bytes:
        """Return the bytes of one image in this store.

        Args:
            reference: A reference under this store's root.

        Returns:
            The exact bytes, checked with ``verify_image_bytes``.

        Raises:
            ImageValidationError: If the reference's fields are invalid.
            MediaStoreError: If the reference is outside this store's root,
                the file is missing, is a symbolic link or not a regular file,
                has more than one hard link, or its bytes do not match.
            OSError: If the file system refuses the read for another reason.
        """
        require_store_image_reference(reference, self._store_uri)
        data = await asyncio.to_thread(self._read_image, reference)
        return verify_image_bytes(reference, data)

    def _write_image(self, reference: AgentImageReference, data: bytes) -> None:
        self._make_root()
        root_fd = os.open(self._root, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
        try:
            try:
                directory_fd = self._open_image_directory(root_fd)
            except FileNotFoundError:
                with contextlib.suppress(FileExistsError):
                    os.mkdir(_IMAGE_DIRECTORY_NAME, _DIRECTORY_MODE, dir_fd=root_fd)
                directory_fd = self._open_image_directory(root_fd)
            try:
                if not self._has_image_file(directory_fd, reference, data):
                    _replace_file(directory_fd, reference.sha256, data)
                # Every put syncs its directory chain: a concurrent creator may not have yet.
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
            os.fsync(root_fd)
        finally:
            os.close(root_fd)
        _sync_directory(posixpath.dirname(self._root))

    def _make_root(self) -> None:
        missing: list[str] = []
        path = self._root
        while not os.path.isdir(path):
            missing.append(path)
            path = posixpath.dirname(path)
        for directory in reversed(missing):
            with contextlib.suppress(FileExistsError):
                os.mkdir(directory, _DIRECTORY_MODE)
            _sync_directory(posixpath.dirname(directory))

    def _has_image_file(
        self, directory_fd: int, reference: AgentImageReference, data: bytes
    ) -> bool:
        """Return whether the image's file exists and holds exactly ``data``."""
        try:
            file_fd = self._open_image_file(directory_fd, reference)
        except FileNotFoundError:
            return False
        try:
            if os.fstat(file_fd).st_size != len(data):
                return False
            return _read_at_most(file_fd, len(data) + 1) == data
        finally:
            os.close(file_fd)

    def _read_image(self, reference: AgentImageReference) -> bytes:
        try:
            root_fd = os.open(self._root, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
            try:
                directory_fd = self._open_image_directory(root_fd)
            finally:
                os.close(root_fd)
            try:
                file_fd = self._open_image_file(directory_fd, reference)
            finally:
                os.close(directory_fd)
        except FileNotFoundError as error:
            raise MediaStoreError(f"image {reference.uri} is not in the media store") from error
        try:
            if os.fstat(file_fd).st_size != reference.size_bytes:
                raise MediaStoreError(f"image {reference.uri} does not match its sha256 and size")
            # One byte more than expected tells a file that grew apart without reading all of it.
            data = _read_at_most(file_fd, reference.size_bytes + 1)
        finally:
            os.close(file_fd)
        if len(data) != reference.size_bytes:
            raise MediaStoreError(f"image {reference.uri} does not match its sha256 and size")
        return data

    def _open_image_directory(self, root_fd: int) -> int:
        try:
            return os.open(
                _IMAGE_DIRECTORY_NAME,
                os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                dir_fd=root_fd,
            )
        except OSError as error:
            if error.errno in _NOT_DIRECTORY_ERRNOS:
                directory = f"{self._root}/{_IMAGE_DIRECTORY_NAME}"
                raise MediaStoreError(
                    f"media store directory {directory} is a symbolic link or not a directory"
                ) from error
            raise

    def _open_image_file(self, directory_fd: int, reference: AgentImageReference) -> int:
        """Open the reference's image file, refusing links and special files."""
        for attempt in range(_OPEN_ATTEMPTS):
            file_fd = self._open_image_file_once(directory_fd, reference)
            if file_fd is not None:
                return file_fd
            time.sleep(_OPEN_RETRY_DELAY * (attempt + 1))
        raise MediaStoreError(f"image {reference.uri} was replaced while the media store opened it")

    def _open_image_file_once(
        self, directory_fd: int, reference: AgentImageReference
    ) -> int | None:
        """Return the open file, or ``None`` when a rename replaced it after the open."""
        not_regular_file = MediaStoreError(
            f"image {reference.uri} is a symbolic link or not a regular file in the media store"
        )
        try:
            # O_NONBLOCK keeps a planted FIFO from blocking the open.
            file_fd = os.open(
                reference.sha256,
                os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                dir_fd=directory_fd,
            )
        except OSError as error:
            if error.errno in _NOT_REGULAR_FILE_ERRNOS:
                raise not_regular_file from error
            raise
        status = os.fstat(file_fd)
        if not stat.S_ISREG(status.st_mode):
            os.close(file_fd)
            raise not_regular_file
        if status.st_nlink == 0:
            os.close(file_fd)
            return None
        if status.st_nlink != 1:
            os.close(file_fd)
            # Another name for the inode may lie outside the root.
            raise MediaStoreError(
                f"image {reference.uri} has more than one hard link in the media store"
            )
        return file_fd


def _absolute_root(path: str) -> str:
    if not path.startswith("/"):
        raise ValueError("LocalDirectoryMediaStore root must be an absolute path")
    try:
        path.encode("utf-8")
    except UnicodeEncodeError:
        raise ValueError("LocalDirectoryMediaStore root must be well-formed Unicode") from None
    if "\0" in path:
        raise ValueError("LocalDirectoryMediaStore root must not contain NUL")
    absolute = posixpath.normpath(path)
    # POSIX keeps a leading "//"; collapse it, as Node's path.posix.resolve does.
    if absolute.startswith("//"):
        absolute = "/" + absolute.lstrip("/")
    if absolute == "/":
        raise ValueError("LocalDirectoryMediaStore root must not be the file system root")
    return absolute


def _replace_file(directory_fd: int, name: str, data: bytes) -> None:
    temporary_name = f".{name}.{secrets.token_hex(8)}.tmp"
    file_fd = os.open(
        temporary_name,
        os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
        _FILE_MODE,
        dir_fd=directory_fd,
    )
    try:
        try:
            view = memoryview(data)
            while view:
                view = view[os.write(file_fd, view) :]
            os.fsync(file_fd)
        finally:
            os.close(file_fd)
        # rename replaces the entry itself, never a link's target, and is atomic.
        os.rename(temporary_name, name, src_dir_fd=directory_fd, dst_dir_fd=directory_fd)
    except BaseException:
        # The original error matters more than a temporary file left behind.
        with contextlib.suppress(OSError):
            os.unlink(temporary_name, dir_fd=directory_fd)
        raise


def _sync_directory(path: str) -> None:
    directory_fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)


def _read_at_most(file_fd: int, limit: int) -> bytes:
    chunks: list[bytes] = []
    remaining = limit
    while remaining > 0:
        chunk = os.read(file_fd, remaining)
        if not chunk:
            break
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)
