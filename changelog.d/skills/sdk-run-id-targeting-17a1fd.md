---
bump: patch
---
- **dex-app-builder**: **core/flow.md** "Child jobs with their own identity" ties a claimed child run by reading, waiting for, and stopping it by its run ID. It drops the describe-before-and-after confirmation and the child stop RPC that compared run IDs. A stop with the claimed run ID never affects a newer run, and its not-found result means the run already stopped. **core/client.md** states that DescribeFlow and the Client Attribute read take an optional run ID.
