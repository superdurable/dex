---
section: Added
---
- **objectStore.provider: azureBlob** and **objectStore.provider: googleCloudStorage** let **serve** keep its object storage in Azure Blob Storage or Google Cloud Storage through each cloud's native API. Set the Azure storage account in **objectStore.azureAccount** and the container or bucket in **objectStore.bucket**. Credentials come from the environment or each cloud's default identity chain. Existing configurations keep using S3, and a prefix cannot switch providers. [Reference](/reference/server/configuration#azure-gcs).
- **objectStore.hardDeleteStore.provider** selects the provider of the hard-delete destination: **s3**, **aliyunOSS**, **azureBlob**, or **googleCloudStorage**; **db9** is rejected. Without it, the destination uses **aliyunOSS** when the primary store does and **s3** otherwise, as before. **objectStore.hardDeleteStore.azureAccount** names its Azure storage account. [Reference](/reference/server/configuration#azure-gcs-switching).
