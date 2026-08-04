# Licensing guide

This guide summarizes the repository licenses. The license texts control if
this guide conflicts with them.

## Core product, SDKs, and protocol

Super Durable changes after the Legacy Cutoff are distributed under the
[Super Durable Source License 1.0](LICENSE). Non-production use is free.
Production use is free while the user and its affiliates have no more than
US$10 million in consolidated annual revenue. A user that exceeds the threshold
has 90 days after the relevant fiscal year ends to obtain a subscription.

Competitive SDKs, Dex replacements, and hosted Dex-compatible services always
require a written commercial license. Contact licensing@superdurable.io.

## Applications

An application may bundle Dex SDK and protocol components when the application's
primary purpose is its own business or end-user functionality. The developer or
operator is responsible for satisfying the revenue rule. An end user that only
runs the application does not need a separate Dex subscription, unless it also
deploys Dex or uses the SDK independently.

Connectors, plug-ins, and adapters that interoperate with Dex are not competitive
products merely because they use a Dex API.

## Legacy code

Code present at the Legacy Cutoff remains under its original MIT or Apache-2.0
terms. A modified legacy file therefore contains separately licensed portions:
the legacy material remains under its former license and later Super Durable
modifications use the Super Durable Source License 1.0. See
[LEGACY_NOTICES.md](LEGACY_NOTICES.md).

Previously released MIT and Apache-2.0 versions remain available under their
original terms.

## Exclusions

Examples keep their existing MIT or Apache-2.0 licenses. The `docs/` directory
is not relicensed and receives no new license grant from this change.

## Common scenarios

| Scenario | Result |
| --- | --- |
| Internal evaluation or development | Free |
| Production use at or below US$10M consolidated annual revenue | Free |
| Production use above US$10M | Subscription required after the 90-day grace period |
| Ordinary application bundling a Dex SDK | Allowed, subject to the developer/operator revenue rule |
| End user only running that application | No separate subscription |
| Independent Dex-compatible SDK or hosted replacement | Separate commercial license required |

## Frequently asked questions

### Is the US$10 million threshold ARR?

No. The license uses consolidated annual gross revenue, not recurring revenue.
It includes the user and its affiliates and follows the most recently completed
fiscal year, with the rules in the license for new entities and currency
conversion.

### When does the 90-day period begin?

It begins after the end of the first fiscal year for which consolidated annual
revenue exceeds US$10 million. The same production use may continue during that
period while the user obtains a subscription.

### Can an application ship a Dex SDK or generated protocol client?

Yes, when the component is embedded or bundled and the application's primary
purpose is its own business or end-user function. The exception does not permit
extracting or independently redistributing the SDK.

### Can someone fork an SDK into a competing client?

Not under the source license. Developing or distributing an independent
Dex-compatible SDK, replacement product, or hosted service requires a separate
commercial license regardless of revenue.

### Why do some files show two sets of terms?

Their cutoff content remains under its original license. Post-cutoff Super
Durable modifications use the source license, so the file uses a mixed header
that identifies both portions without replacing the legacy notice.

This guide is not legal advice. Have counsel review the license and its
application to a particular use.
