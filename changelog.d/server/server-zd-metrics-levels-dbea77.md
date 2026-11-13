---
section: Added
---
- **metrics.level** selects which metric families **GET /metrics** exports: **basic** (golden signals for SLOs and paging alerts), **advanced** (the default; adds capacity, cost, and the lag and failure signals of each optional component), or **verbose** (adds internal mechanisms, throughput detail, and the Linux process metrics). Each level includes the families of the levels below it, and a family keeps the same labels and buckets at every level. The metrics page now lists every family with its type, labels, buckets, and level. [Reference](/reference/server/metrics#metrics-levels).
