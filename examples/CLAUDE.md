# Examples Rules

The repository root `CLAUDE.md` rules also apply.

# FDG 2.0 Step Explanations

Every Step type in a Flow Definition Graph 2.0 Go source must declare exactly
one `// dex:explanation text:"..."` directive next to its `dex:group`. The text
is one sentence that states what the Step does. The analyzer stores it on the
Step node as `metadata.explanation` for Dex Web v2 Definition.

# Python Examples

Do not use `del` in Python examples merely to mark parameters or local values
as unused. Keep required callback parameters unused instead.

# RPC Names Start With Verbs

Every application-example RPC handler and explicit RPC name must begin with a
concrete action verb. Use verbs such as `get`, `list`, or `describe` for reads
and domain actions such as `send`, `update`, `delete`, or `move` for writes. Do
not use noun-only names such as `queuedMessages`, `history`, or `snapshot`, and
do not use placeholders such as `exampleRPC`.
