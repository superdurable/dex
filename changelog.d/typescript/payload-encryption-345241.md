---
section: Added
---
- Added client-side payload encryption. Set **payloadCodec** in **ClientOptions** and **WorkerOptions**, and the SDK encrypts every Value it sends and decrypts every Value it receives, so the Dex Server stores only ciphertext and never holds keys. The built-in **AesGcmPayloadCodec** uses AES-256-GCM with optional gzip compression, and **FilePayloadEncryptionKeyProvider** reads keys from a JSON key file. Implement **PayloadCodec** for another scheme, such as a KMS.
- Worker error text and the reasons of **stopFlow**, **timeTravel**, and **cloneForkFlow** are encrypted too. Switch string, primitive, and error-text encryption off with **encryptStringValues**, **encryptPrimitiveValues**, and **encryptErrorText**.
- Indexed Attributes stay in clear so Flow search keeps working. Declare an Attribute or AttributeMap with the new **plaintext()** option when the Server must read its value, for example for **waitForAttributeMatch** or an Action condition. With a codec, the SDK rejects those uses on an encrypted Attribute.
- The Go, Python, and TypeScript SDKs share the encryption format. The Java and Rust SDKs cannot read encrypted Flows yet.
