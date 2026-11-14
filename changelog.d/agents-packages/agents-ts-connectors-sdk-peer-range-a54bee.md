---
section: Fixed
---
- `@superdurable/dex-agents`: the optional peer `@superdurable/connectors-sdk`
  now accepts any 0.9.x or 0.10.x (`^0.9.0 || ^0.10.0`, that is
  `>=0.9.0 <0.11.0`) instead of exactly 0.9.0. An application that installs
  a later Connector SDK, such as 0.9.1 or 0.10.0, or a Connector package that
  pins one, such as `@superdurable/connector-slack` 0.6.0, no longer fails
  `npm install` with `ERESOLVE` or needs npm `overrides`. This also applies
  through the adapters that peer-depend on `@superdurable/dex-agents`, such as
  `@superdurable/dex-agents-pi`. Connector SDK 0.11 and later stay outside the
  range.
