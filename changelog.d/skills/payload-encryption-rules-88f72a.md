---
bump: patch
---
- New payload encryption rules: enable it for data the server must not read; every Worker and Client shares one codec and keys; Attributes the server compares stay plaintext; IDs and names are never encrypted, so they hold no sensitive data.
