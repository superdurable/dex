---
section: Changed
---
- **dexcli visualize** keeps the warning **locked_method_external_call** for definite external calls under an RPC-shared lock. A method with only uncertain calls gets the new info diagnostic **locked_method_uncertain_call**. [Reference](/reference/web/flow-visualization#locks-shared-with-rpcs)
- The Flow Definition Graph schema accepts the diagnostic severity **info**. Diagnostics sort as error, warning, then info.
