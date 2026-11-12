---
section: Added
---
- **objectStore.provider: aliyunOSS** lets **serve** keep its object storage in an Aliyun OSS bucket through the OSS S3-compatible endpoint, such as **https://s3.oss-cn-hangzhou.aliyuncs.com**. The bucket must never have had versioning enabled; startup checks this. Objects that the server replaces, such as the cluster manifest and node leases, are stored as versions that the server manages. Data cannot move between **aliyunOSS** and other providers. [Reference](/reference/server/object-storage#aliyun-oss).
