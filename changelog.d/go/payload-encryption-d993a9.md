---
section: Added
---
- Client-side payload encryption. Set **PayloadEncryptionOptions** on **ClientOptions** and **WorkerOptions**, and the SDK encrypts every value it sends, so the Dex server stores only ciphertext. Readers decrypt values after loading them and pass plaintext values through, so encrypted and plaintext Flows can share one Worker.
- The built-in **AESGCMPayloadCodec** uses AES-256-GCM, with optional gzip compression. **FilePayloadEncryptionKeyProvider** reads keys from a JSON key file and supports key rotation. Implement **PayloadCodec** or **PayloadEncryptionKeyProvider** to use your own key management.
- Worker error details and stack traces, ForceFail reasons, and Stop, Time Travel, and clone-fork reasons are encrypted too, in a **dex-encoded:** text format.
- The **Plaintext** Attribute option keeps an Attribute in clear. **WaitForAttributeMatch** and Action conditions need an Indexed or **Plaintext** Attribute when encryption is on. An encrypted Attribute synced to the Attribute Store needs a bytea column.
- The Java and Rust SDKs cannot read encrypted Flows yet.
