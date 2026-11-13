---
section: Added
---
- **objectStore.versionDiscovery: head** lets **aliyunOSS** find the newest version of a replaced object, such as the cluster manifest or a node lease, with HEAD requests instead of a listing. It then depends only on reads of single objects being current, which Aliyun documents, and not on listings showing each write at once. The default stays **list**. Nodes of 0.1.30 or later with either value can share a bucket. Set **head** only once every node sharing the bucket runs 0.1.29 or later, because older nodes delete replaced versions after 60 seconds. **dexzd_object_store_versioned_listing_stale_total** gains the **newest_version_lookup** site. [Reference](/reference/server/object-storage#version-discovery).
