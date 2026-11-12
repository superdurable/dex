---
section: Added
---
- **objectStore.provider: azureBlob** and **objectStore.provider: googleCloudStorage** let **serve** keep its object storage in Azure Blob Storage or Google Cloud Storage through each cloud's native API. Set the Azure storage account in **objectStore.azureAccount** and the container or bucket in **objectStore.bucket**. Credentials come from the environment or each cloud's default identity chain. **objectStore.hardDeleteStore** accepts the same providers. Existing configurations keep using S3, and a prefix cannot switch providers. [Reference](/reference/server/configuration#azure-gcs).
