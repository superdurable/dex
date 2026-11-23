---
section: Fixed
---
- **BlobCache.put** keeps a blob's file that is already on disk with the same bytes, such as one written by another cache on the same directory, and returns True. Before, it raised **blob ID content mismatch**. A file with different bytes still raises (#2185).
