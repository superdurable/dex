# Design review checklist

Use this checklist before implementing a new Dex application, library, or a design change to either. Answer every item in the design document; one line each is enough.

1. **Prior art.** Before designing, search the baseline [examples](https://github.com/superdurable/dex/tree/sdk-go/v1.35.0/examples), [pattern selection](patterns.md), the [design pattern pages](https://docs.superdurable.io/design-patterns), and any Dex-native reference implementation the user has. Name the nearest one and say what the design keeps and changes.
2. **Premise verification.** Every "Dex can X" or "Dex can't X" claim cites a source file or a test at the pinned release, or the installed SDK version. An unverified premise is a question for the owner, not a design constraint.
3. **Primitive check.** Before building a workaround, check whether a Dex primitive already covers the need: canceling steps (see the language's **advanced-features.md**), [sequentially chunked and hash-partitioned AttributeMaps](patterns.md#partitioned-attributemap-state), Attribute locks, durable Timers, and SubFlows ([Flow modeling](modeling.md)).
4. **Persisted layout and wire format.** Settle Attribute, AttributeMap, and Channel names, instance keys, chunk size, partition count, hash algorithm, value schemas, and RPC input and output types before the first release. Changing them later means reading both forms for as long as old Flows or stored values exist ([versioning](versioning.md)).
5. **Existing features.** List the features of the nearest example or the version being replaced. Drop one only with an explicit, recorded decision.
6. **Owner review.** Before implementation starts, get the owner's review of the premises and decisions above, and record the outcome.
