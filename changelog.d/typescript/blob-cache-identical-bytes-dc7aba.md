---
section: Fixed
---
- **BlobCache.put** keeps a blob's file already on disk with the same bytes, such as one another cache on the same directory wrote, and returns true. It used to throw **blob ID content mismatch** (#2185). [API reference](pathname:///api/typescript/latest/interfaces/BlobCache.html#put).
