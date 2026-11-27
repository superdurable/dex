---
section: Changed
---
- **dexcli visualize** separates definite from uncertain calls in Step methods that hold a lock an RPC also takes. A definite call, such as an injected dependency, a Dex client, known I/O, or a dynamic call, keeps the warning **locked_method_external_call**, which now also counts the uncertain calls. A method with only uncertain calls, such as a third-party library that may be pure, a helper the analyzer does not follow, or console output, gets the new info diagnostic **locked_method_uncertain_call**. Diagnostics sort as error, warning, then info, and the Flow Definition Graph schema accepts the severity **info**.
