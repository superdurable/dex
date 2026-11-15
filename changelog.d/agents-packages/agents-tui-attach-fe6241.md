---
section: Fixed
---
- TypeScript CLI commands: when the terminal UI attaches an image and the media store fails, the answer names a known cause, such as a full disk, denied permission, or an S3 access error, which the terminal UI shows; other failures say only that the image could not be stored. The Worker host log records the same reason, never the store's error text, which can hold bucket URLs or credentials. A file that exists but cannot be read says so with its reason instead of "does not exist", and size limits are written with comma separators.
