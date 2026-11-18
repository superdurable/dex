# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

"""A Flow whose RPCs the TypeScript Client calls on a Python Worker."""

from dex import Attribute, Context, Flow, PersistenceSchema, RPCResult, rpc

class TypeScriptClientNoteFlow(Flow[None]):
    note = Attribute("note", str)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.note)

    @rpc
    def set_note(self, context: Context, note: str) -> RPCResult[str]:
        self.note.set(context, note)
        return RPCResult(note)

    @rpc
    def get_note(self, context: Context) -> RPCResult[str]:
        return RPCResult(self.note.get(context) or "")

    @rpc
    def clear_note(self, context: Context) -> None:
        self.note.set(context, "")
