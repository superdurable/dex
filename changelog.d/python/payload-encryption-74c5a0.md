---
section: Added
---
- Client-side payload encryption. Set **payload_encryption** on **ClientOptions** and **WorkerOptions** to a **PayloadEncryptionOptions**, and the SDK encrypts every Value before it reaches the Server and decrypts it after it returns. The Server stores only ciphertext and never holds keys.
- **AesGcmPayloadCodec** is the built-in codec. It uses AES-256-GCM with a random nonce per Value and optional gzip compression. Install it with **pip install 'dex-python-sdk[encryption]'**, which adds the **cryptography** package. **FilePayloadEncryptionKeyProvider** reads keys from the shared key file format and supports key rotation.
- Plug in your own encryption, such as a KMS, with **PayloadCodec**, or with **AsyncPayloadCodec** for **AsyncClient** and **AsyncWorker**. **PayloadContext** tells the codec which Flow stores the Values, so a key provider can use one key per tenant.
- String, object, int, double, and bool Values are encrypted by default. Indexed Attributes stay in plaintext so search works. Declare **plaintext=True** on an **Attribute** or **AttributeMap** that **wait_for_attribute_match**, an Action condition, or an external reader needs in plaintext. With encryption on, the SDK rejects **wait_for_attribute_match** and Action conditions on an Attribute that would be encrypted.
- Worker error text and the reasons of **force_fail**, **stop_flow**, **time_travel**, and **clone_fork_flow** are encrypted too, and the SDK decrypts them where it exposes them.
- Encrypted and plaintext Flows can share one Worker. A Client or Worker without a codec that reads an encrypted Value fails with "value is encrypted; configure a PayloadCodec". The Java and Rust SDKs cannot read encrypted Flows yet.
