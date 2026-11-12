---
bump: patch
---
- Move release-specific UI details and edge-case semantics out of the Skills into the Developer reference, and keep one short rule with a link in each place. The Developer reference now holds Step cancellation, completion requests, and Flow outputs (new Step and Flow close page), Attribute lock order, Channel waiter order, Join edge cases, and SubFlow reuse by later parent runs and parent close.
- Shorten the Flow ID reuse policy, StartFlow retry dedupe, Flow closing request, canceled Step message, Channel delete, and SubFlow reuse policy rules in `dex-sdk`, and point to the existing reference pages.
- Remove Dex Web page layout, Experimental labels, Start Flow placement, routing details, local Connector connection details, and Flow Definition Graph replacement details from `dex-sdk` and `dex-app-builder`. The Dex Web and Server references hold them.
