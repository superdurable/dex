---
section: Changed
---
- With **objectStore.provider: aliyunOSS**, the server keeps each replaced version of the manifest, the registry, node leases, and Flow definitions for at least one hour instead of 60 seconds before it deletes it. Replacements stay correct even if an OSS listing lags behind a write by up to an hour. Each node now keeps about 1,200 lease versions at the default **lease.duration**, and it deletes the old versions of one key at most once every 5 minutes, so it sends fewer listing and delete requests than before. The new **dexzd_object_store_versioned_listing_stale_total** counter, with a **site** label, counts listings that missed a version the node had just written; alert on any increase. [Reference](/reference/server/object-storage#aliyun-oss).
