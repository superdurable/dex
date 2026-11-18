---
bump: patch
---
- **dex-app-builder**: the **managed-dex-app.md** reference adds Alternative settings. When an application needs one of several sets of environment variables, such as a model API key or a bring-your-own endpoint, it declares them with **application.requiredOneOf** instead of marking every alternative required or none of them.
