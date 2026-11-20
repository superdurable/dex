# Payload encryption format `dex/1`

This document specifies how Dex SDKs encrypt payloads on the client. It is
written for SDK, dexcli, Dex Web, and codec server implementers. Application
developers read the Developer reference instead.

The key words MUST, MUST NOT, SHOULD, and MAY are used as in RFC 2119.

## Contents

1. [Overview](#1-overview)
2. [The envelope Value](#2-the-envelope-value)
3. [`PayloadEnvelope`](#3-payloadenvelope)
4. [The built-in AES-256-GCM codec](#4-the-built-in-aes-256-gcm-codec)
5. [Key IDs and the key file](#5-key-ids-and-the-key-file)
6. [String fields: the `dex-encoded:` prefix](#6-string-fields-the-dex-encoded-prefix)
7. [Decoding](#7-decoding)
8. [Encoding](#8-encoding)
9. [`PayloadContext`](#9-payloadcontext)
10. [SDK interfaces](#10-sdk-interfaces)
11. [Codec server HTTP protocol](#11-codec-server-http-protocol)
12. [Golden vectors](#12-golden-vectors)
13. [Compatibility](#13-compatibility)

## 1. Overview

An SDK encrypts a `Value` before it sends the `Value` to the Server. It
decrypts the `Value` after it receives and hydrates it. The Server stores
only ciphertext. The Server never holds keys and never reads an envelope.

```text
write: user value -> typed encode -> Value -> serialize -> codec encode -> PayloadEnvelope -> envelope Value -> Server
read:  Server -> Value -> hydrate blobs -> envelope Value? -> codec decode -> parse Value -> typed decode -> user value
```

- Encryption happens after typed encoding. Decryption happens after blob
  hydration and before typed decoding.
- Blob caches hold ciphertext.
- A decoder decrypts only Values that carry the `dex/1` marker. Every other
  Value passes through unchanged. Encrypted and plaintext Flows can therefore
  share one Worker, and encryption can roll out gradually.
- The Server needs no change. Older Servers store encrypted Values as ordinary
  objects.

A **codec** turns serialized Values into `PayloadEnvelope` messages and back.
An SDK has exactly one codec. Codecs do not chain. The built-in codec is
AES-256-GCM with optional gzip compression. A custom codec, such as one that
calls a KMS, uses `PAYLOAD_CIPHER_CUSTOM`.

## 2. The envelope Value

An encrypted Value is an object Value:

```text
Value { obj_value: EncodedObject { encoding: "dex/1", payload: <serialized PayloadEnvelope> } }
```

- `dex/1` means "Dex `PayloadEnvelope` format, version 1". The trailing `1`
  is the format version. Cipher and compression live in the envelope, so the
  marker does not name them.
- Every encoding that starts with the five bytes `dex/` is reserved for Dex.
  The comparison is exact and case-sensitive. An encoding such as `dex` or
  `DEX/1` is an ordinary application encoding.
- An SDK MUST reject an application object whose encoding starts with `dex/`.
  It rejects it when it encodes the object, before any encryption. This keeps
  application data from being mistaken for an envelope.

### What is encrypted

The plaintext is the protobuf serialization of the original `Value`. The
`Value` oneof records the kind, including an object's encoding. An object whose
encoding is the text `int` therefore decodes back to `obj_value`, never to
`int_value`.

Only these kinds are ever encrypted:

| `Value.kind` | Encrypted |
| --- | --- |
| `string_value` | Yes, unless the application turns string encryption off |
| `obj_value` | Yes. Its encoding never starts with `dex/` |
| `int_value`, `double_value`, `bool_value` | Yes, unless the application turns primitive encryption off |
| `null_value` | Never. In an Attribute write, null deletes the Attribute, so the Server must see it |
| `internal_blob_id_for_string_value`, `internal_blob_id_for_obj_value` | Never. The Server writes these references. Hydrate them first; the loaded content is the envelope |
| unset | Never |

Proto3 writes a oneof field even when its value is the default. An empty
string, `0`, `0.0`, and `false` therefore still produce non-empty plaintexts.

## 3. `PayloadEnvelope`

`PayloadEnvelope` is defined in [`dex.proto`](dex.proto). SDKs serialize it
themselves. The Server never reads it.

| Field | Number | Meaning |
| --- | --- | --- |
| `cipher` | 1 | The `PayloadCipher` that produced `data` |
| `key_id` | 2 | The key that encrypted `data`. It is visible in clear |
| `data` | 3 | The cipher output. For AES-256-GCM: nonce, then ciphertext, then tag |
| `compression` | 4 | The `PayloadCompression` applied before encryption |
| `metadata` | 5 | Custom codec parameters. Not covered by the built-in AAD. Empty for the built-in codec |

| `PayloadCipher` | Value | Meaning |
| --- | --- | --- |
| `PAYLOAD_CIPHER_UNSPECIFIED` | 0 | Invalid. Decoders reject it |
| `PAYLOAD_CIPHER_AES256_GCM` | 1 | The built-in codec. See section 4 |
| `PAYLOAD_CIPHER_CUSTOM` | 2 | A custom codec. Its parameters live in `metadata` |

| `PayloadCompression` | Value | Meaning |
| --- | --- | --- |
| `PAYLOAD_COMPRESSION_NONE` | 0 | No compression |
| `PAYLOAD_COMPRESSION_GZIP` | 1 | gzip ([RFC 1952](https://www.rfc-editor.org/rfc/rfc1952)), applied before encryption |

Proto3 enums are open. A decoder that reads an unknown enum number keeps the
number, so it can reject it. See section 7.

### Metadata integrity

Nothing in the envelope format authenticates `metadata`. Anyone who can write
a Value, including the Server, can change it.

- A custom codec MUST authenticate every `metadata` key and value that affects
  security or decoding. For example, it can include them in its own AAD in a
  canonical order.
- Otherwise, the custom codec MUST reject any `metadata` it does not
  authenticate.
- The built-in AES-256-GCM codec uses no metadata. It rejects every envelope
  that has any.

### Why gzip

Compression must have mature implementations in Go, Python, and
TypeScript/Node without new dependencies. gzip is in all three standard
libraries: Go `compress/gzip`, Python `gzip`, and Node `zlib`. Faster
algorithms, such as zstd, need a third-party package in at least one of these
languages. A later release may add another `PayloadCompression` value. See
section 13.

## 4. The built-in AES-256-GCM codec

- The key is 32 bytes (AES-256).
- The nonce is 12 bytes. The encoder MUST generate it with a
  cryptographically secure random number generator for every encryption. It
  MUST never reuse a nonce with the same key.
- The tag is 16 bytes.
- `data` is `nonce || ciphertext || tag`. Its length is the sealed plaintext
  length plus 28 bytes.
- `metadata` MUST be empty. The AAD does not cover `metadata`, so the built-in
  decoder rejects an envelope that has any metadata.
- With compression, the encoder gzips the serialized `Value` first and
  encrypts the gzip bytes. It sets `compression` to
  `PAYLOAD_COMPRESSION_GZIP`.

### Additional authenticated data

The AAD binds the envelope header to the ciphertext. It is the exact byte
string:

```text
"dex/1" 0x00 <cipher> 0x00 <compression> 0x00 <key_id>
```

- `"dex/1"` is the five ASCII bytes of the marker.
- `<cipher>` and `<compression>` are the enum numbers written as ASCII decimal
  digits, without leading zeros or a sign. For example, `1` is the byte `0x31`.
- `<key_id>` is the key ID's ASCII bytes. Nothing follows it.
- The AAD is not a protobuf serialization. Protobuf output is not guaranteed
  to be identical across languages.
- Prefix-format text (section 6) uses the same AAD, including the literal
  `dex/1`.
- The AAD does not include the Flow type, Flow ID, or Run ID. See section 9.

Worked example: cipher `PAYLOAD_CIPHER_AES256_GCM` (1), compression
`PAYLOAD_COMPRESSION_NONE` (0), key ID `k1`:

```text
 d  e  x  /  1 \0  1 \0  0 \0  k  1
64 65 78 2f 31 00 31 00 30 00 6b 31
```

With gzip (1) and key ID `k2`, the AAD is
`64 65 78 2f 31 00 31 00 31 00 6b 32`.

### Size

Each encrypted Value grows by about 50 bytes. AES-GCM adds 28 bytes. The
marker, the enum fields, the key ID, and the protobuf field headers add the
rest. For example, the int `42` with key ID `k1` grows from 2 bytes to about
49 bytes. The Server moves Values above its inline threshold to the blob
store, so more short Values become blob references.

## 5. Key IDs and the key file

A key ID is 1 to 64 bytes. Each byte is printable ASCII from `0x21` to `0x7e`.
Spaces and `0x00` are not allowed. Key IDs are visible in clear in every
envelope. They MUST NOT contain sensitive data.

### Key file

dexcli, the example codec servers, and every SDK file key provider read one
JSON format:

```json
{
  "current_key_id": "k2",
  "keys": {
    "k1": "<standard base64 of 32 bytes>",
    "k2": "<standard base64 of 32 bytes>"
  }
}
```

- `current_key_id` names the key that new encryptions use. It MUST appear in
  `keys`.
- `keys` maps each key ID to its key in standard base64 with padding. Each
  key decodes to exactly 32 bytes.
- Keep old keys in `keys` after rotation. Envelopes that name them stay
  decodable.
- A loader MUST reject a file with an invalid key ID, a key that is not 32
  bytes, or a missing current key. Its error MUST NOT contain key bytes.
- A loader SHOULD warn when the file is readable by other users.
  `dexcli codec-server keygen` writes the file with mode `0600`.

## 6. String fields: the `dex-encoded:` prefix

Some protocol fields are plain strings, not `Value`s. They carry error text
and reasons. This format encrypts them without a Server change. A later
Server release will carry them as `Value`s and retire the prefix
([#2066](https://github.com/superdurable/dex/issues/2066)).

The encoded text is:

```text
"dex-encoded:" + base64(serialized PayloadEnvelope)
```

- The base64 uses the standard alphabet with padding (RFC 4648 section 4). It
  has no line breaks or whitespace.
- The plaintext is the serialized `Value { string_value: <original text> }`.
- The same codec and the same envelope apply. The AAD still uses `dex/1`.
- The prefix `dex-encoded:` is reserved. It must start at the first byte of
  the field.

These fields use the format:

| Field | Note |
| --- | --- |
| `WorkerErrorResponse.detail` | |
| `WorkerErrorResponse.stack_trace` | |
| `StopFlowRequest.reason` | |
| `ResetFlowRequest.reason` | |
| `CloneForkFlowRequest.reason` | |
| ForceFail `close_input` | It is a `Value`, but the Server requires `string_value` and uses the text as the error message. The SDK encrypts the text into the prefix format and sends it as `string_value` |

These texts stay plaintext:

- Blob-loading error text that the SDK generates. The Server matches this
  text.
- Decryption error text that the SDK generates. It never contains payload
  bytes.
- An empty `detail`. The Server gives an empty `detail` a special meaning.
- `error_type`, `grpc_status`, and `retry_after_seconds`.

### Writers never emit plaintext that starts with the prefix

An error text or reason can legitimately start with `dex-encoded:`. Readers
would then try to decode it and fail. So writers (SDKs, Dex Web, dexcli) MUST
NOT write plaintext that starts with `dex-encoded:` into these fields:

- **A codec is configured:** the writer encrypts that text into the prefix
  format. It does so even when error-text encryption is turned off.
- **No codec is configured:** the writer prepends one ASCII space (`0x20`)
  and writes the result as plaintext.

### Readers

Readers (SDKs, Dex Web, dexcli) decode text only when it starts with
`dex-encoded:` at the first byte. They show other text unchanged. That
includes the space-prefixed form above: readers keep the space and do not
strip it.

Readers stay strict. Text that starts with `dex-encoded:` must decode. A
failure is an error, never a fallback to plaintext.

### Read points

The Server copies the text of the fields above, unchanged, into other fields.
Readers decode the prefix in these read points too:

| Field | Note |
| --- | --- |
| `ServiceErrorResponse.detail` | |
| gRPC `Status.message` | Only of a status that carries a `ServiceErrorResponse` detail, such as a failed `InvokeRPC`. A status without one, such as a transport error, stays unchanged |
| `ServiceErrorResponse.original_worker_error_detail` | |
| `ServiceErrorResponse.original_worker_error_stack_trace` | |
| `RecoveryErrorInfo.detail` | Including `Context.recovery_error` in Worker requests |
| `FlowResult.error_message` | Including SubFlow results in `ConditionResults.sub_flow_results` and `StepExecutionCompletedConditions.completed_sub_flow_results` |
| `FlowClosedHistoryEvent.error_message` | |
| `StepMethodFailure.details` | A `ServiceErrorResponse`, in `StepMethodEventContext.last_failure_info`, `StepMethodFailedOutput.failure`, and `ActiveStepExecutionState.last_failure_info` |

Readers decode the prefix only in the writer fields above and in these read
points. They leave every other string unchanged, even when it starts with
`dex-encoded:`. Server-composed text that embeds an encoded field after other
text does not start with the prefix, so readers show it unchanged.

## 7. Decoding

### What a decoder decodes

- An object Value whose encoding starts with `dex/`.
- A string field that starts with `dex-encoded:`.

Everything else passes through unchanged, including null, blob references,
and Values that the Server writes.

A decoder MUST NOT treat a marked Value or prefixed text as plaintext when
decoding fails. Every failure in this section is an error.

### Check order and error categories

Decoders check in this order. A failure stops decoding with the category
shown. The golden vectors name the category of the first rule each negative
case breaks.

| Step | Check | Category on failure |
| --- | --- | --- |
| 1 | Object Value: the encoding is exactly `dex/1` | `unsupported_marker` |
| 2 | Before base64 decoding or protobuf parsing. Object Value: the payload is at most `maxEnvelopeBytes`. Prefix text: the text after the prefix is at most `maxEncodedTextBytes` | `limit_exceeded` |
| 3 | Prefix text: the text after the prefix is canonical padded base64. Its decoded bytes are at most `maxEnvelopeBytes` | `malformed`, or `limit_exceeded` for the size |
| 4 | The envelope bytes parse as a `PayloadEnvelope` | `malformed` |
| 5 | `key_id` follows section 5 | `malformed` |
| 6 | `metadata` is within the limits below | `limit_exceeded` |
| 7 | The configured codec supports `cipher`. The built-in codec supports only `PAYLOAD_CIPHER_AES256_GCM` | `unsupported_cipher` |
| 8 | Built-in codec: `metadata` is empty | `malformed` |
| 9 | `compression` is a known value | `unsupported_compression` |
| 10 | The key provider knows `key_id` | `unknown_key` |
| 11 | Before allocating AEAD output. Built-in codec: `data` has at least 28 bytes and at most `maxDataBytes` | `malformed` when too short; `limit_exceeded` when too long |
| 12 | AEAD decryption with the AAD succeeds | `authentication_failed` |
| 13 | After decryption, before parsing the `Value`. Without compression: the decrypted plaintext is at most `maxPlaintextBytes`. With gzip: decompression succeeds and its output is at most `maxPlaintextBytes` | `limit_exceeded` for the size; `malformed` for invalid gzip |
| 14 | The plaintext parses as a `Value` of an encryptable kind (section 2). An object's encoding does not start with `dex/`. Prefix text: the kind is `string_value` | `invalid_plaintext` |

Notes:

- Canonical base64 means that re-encoding the decoded bytes gives back the
  identical text. This rejects missing padding, whitespace, other alphabets,
  and non-zero trailing bits. Some base64 decoders, such as Node's
  `Buffer.from(text, "base64")`, accept invalid input silently. Check the
  round trip.
- Unknown `PayloadEnvelope` fields are ignored. See section 13.
- Envelopes do not nest. A plaintext that is itself a `dex/1` object is
  `invalid_plaintext`.
- A custom codec defines its own checks for steps 8 to 13. The SDK still runs
  steps 1 to 7 around it. After the codec returns, the SDK checks that each
  plaintext is at most `maxPlaintextBytes`, then runs step 14.
- The SDK passes its `maxPlaintextBytes` to every decode call of a custom
  codec. A custom codec that decompresses MUST stop once the output exceeds
  it. The SDK's check after the codec returns cannot stop the allocation.

### Limits

Size limits stop malicious or corrupt envelopes from exhausting memory. Each
check runs at the step shown above, before the work it protects. Every limit
failure has the category `limit_exceeded`.

| Limit | Default | Checked |
| --- | --- | --- |
| `maxEnvelopeBytes`: object payload, or decoded prefix text | 16,842,752 bytes (16 MiB + 64 KiB) | Steps 2 and 3, before protobuf parsing |
| `maxEncodedTextBytes`: base64 text after `dex-encoded:` | 22,457,004 bytes, the base64 length of `maxEnvelopeBytes` | Step 2, before base64 decoding |
| `maxDataBytes`: `PayloadEnvelope.data` | 16,842,752 bytes, equal to `maxEnvelopeBytes` | Step 11, before allocating AEAD output |
| `maxPlaintextBytes`: decrypted or decompressed plaintext | 67,108,864 bytes (64 MiB) | Step 13, before parsing the `Value` |
| `key_id` length | 1 to 64 bytes | Step 5 |
| `metadata` entries | At most 16 | Step 6 |
| `metadata` size | The sum of all key and value lengths is at most 4096 bytes | Step 6 |

- The defaults fit the Server's default maximum Value size of 16 MiB, plus
  64 KiB of headroom for the envelope overhead.
- `maxEncodedTextBytes` is `4 * ceil(maxEnvelopeBytes / 3)`.
- SDKs SHOULD let applications change `maxEnvelopeBytes` and
  `maxPlaintextBytes`, for example for a Server configured with a larger
  maximum Value size. `maxEncodedTextBytes` and `maxDataBytes` follow
  `maxEnvelopeBytes`.
- `maxPlaintextBytes` also stops a gzip bomb. A decoder MUST stop
  decompressing once the output exceeds the limit. It MUST NOT decompress the
  whole input first.
- The key ID and metadata limits are fixed by this format.

### Errors

A decoding error MUST name the category and MAY name the key ID. It MUST NOT
contain payload bytes, plaintext, or key bytes.

An SDK that has no codec but meets a marked Value fails with the message
`value is encrypted; configure a PayloadCodec`.

## 8. Encoding

- Encrypt only the kinds in section 2. Never encrypt null or blob references.
- Serialize the `Value` with the language's standard protobuf serializer.
  Byte-identical output across SDKs is not required.
- Generate a fresh random nonce for every encryption. Following NIST SP
  800-38D, encrypt at most 2^32 Values with one key. Rotate keys regularly.
- The encoder MAY choose compression per Value, for example only when gzip
  makes the Value smaller. The decoder follows `compression`.
- The encoder MUST fail instead of writing an envelope that breaks a section 7
  limit, such as a plaintext over `maxPlaintextBytes` or an envelope over
  `maxEnvelopeBytes`. Readers could never decode it.
- Do not enable compression for Values that mix attacker-controlled content
  with secrets. Ciphertext length reveals how well the content compresses,
  as in the CRIME and BREACH attacks.
- When an SDK retries a gRPC call internally, it SHOULD resend the bytes it
  encrypted the first time.
- Keys stay in memory. They MUST NOT appear in logs, errors, or metrics.
- An SDK MUST NOT write decrypted plaintext to any cache or disk.
- A fixed-nonce hook exists only in test code, such as the vector generator.
  It MUST NOT be a public option of any SDK.

## 9. `PayloadContext`

A codec call carries a `PayloadContext`. It identifies the Flow that stores
the payloads. A key provider uses it to choose keys, for example one key per
tenant.

| Field | Meaning |
| --- | --- |
| Flow type | The Flow type that stores the Values, or the empty string when only the Flow ID is known |
| Flow ID | The Flow ID that stores the Values |

- Use the context of the Flow the Value is stored in.
- When an SDK knows only the Flow ID, the Flow type is the empty string. This
  happens, for example, for Client calls that name a Flow only by its Flow ID.
  The same Value can therefore be encrypted with a Flow type and decrypted
  without one. Key providers SHOULD select and validate keys by Flow ID.
- **SubFlow input** uses the child Flow's type and the child's real Flow ID.
  The child Flow ID is deterministic:
  `SubFlow:{parentFlowID}-{stepExecutionID}-{index}`. `index` is the zero-based
  SubFlow index in the Step's result. The Server builds and checks the same
  ID. SDKs already compute it, for example Go `dex.SubFlowID`. Encryption and
  decryption therefore see the same context. A tenant-aware key provider must
  handle the `SubFlow:` prefix.
- The context is **not** part of the AAD. Clone, fork, and reset copy Values
  into a new run or a new Flow ID. A Flow-bound ciphertext would not decrypt
  there.
- Because ciphertext is not bound to a Flow, the Server can move a valid
  ciphertext to another Flow or replay an old one. A multi-tenant key provider
  SHOULD reject, in `Key`, a key ID that does not belong to the context's
  tenant. Applications that need stronger binding include their own business
  identifiers in the Value and check them.

## 10. SDK interfaces

Each SDK implements these shapes. Codecs work in batches so that a KMS codec
can make one network call for many Values.

Common rules:

- `Encode` returns exactly one envelope per plaintext, in order.
- `Decode` returns exactly one plaintext per envelope, in order.
- A failure fails the whole batch. The error follows section 7.
- `Decode` receives the SDK's `maxPlaintextBytes` and stops decompressing once
  the output exceeds it (section 7).
- Plaintexts are serialized `Value`s. Envelopes are the generated
  `PayloadEnvelope` message of each SDK.
- `CurrentKeyID` chooses the key for new encryptions. `Key` returns the
  32-byte key for a key ID. The default file provider ignores the context in
  `Key`, so clones into another Flow ID still decrypt.

The names below are final. They follow the repository rule for complete,
precise names and avoid the existing type codecs: Python `Codec` and
`CodecRegistry`, and TypeScript `Codec<T>`. Acronyms follow each SDK's style.

| Concept | Go (`dex` package) | Python (`dex` package) | TypeScript |
| --- | --- | --- | --- |
| Codec interface | `PayloadCodec` | `PayloadCodec` (sync) and `AsyncPayloadCodec` | `PayloadCodec` |
| Context | `PayloadContext` | `PayloadContext` | `PayloadContext` |
| Key provider | `PayloadEncryptionKeyProvider` | `PayloadEncryptionKeyProvider` | `PayloadEncryptionKeyProvider` |
| Built-in codec | `AESGCMPayloadCodec`, `NewAESGCMPayloadCodec` | `AesGcmPayloadCodec` | `AesGcmPayloadCodec` |
| File key provider | `FilePayloadEncryptionKeyProvider`, `NewFilePayloadEncryptionKeyProvider` | `FilePayloadEncryptionKeyProvider` | `FilePayloadEncryptionKeyProvider` |

### Go

Go is synchronous and takes a `context.Context`. `Decode` reads the SDK's
`maxPlaintextBytes` from it with `dex.PayloadPlaintextLimitFromContext`.

```go
type PayloadContext struct {
    FlowType string
    FlowID   string
}

type PayloadCodec interface {
    Encode(ctx context.Context, payloadContext PayloadContext, plaintexts [][]byte) ([]*dexpb.PayloadEnvelope, error)
    Decode(ctx context.Context, payloadContext PayloadContext, envelopes []*dexpb.PayloadEnvelope) ([][]byte, error)
}

type PayloadEncryptionKeyProvider interface {
    CurrentKeyID(ctx context.Context, payloadContext PayloadContext) (string, error)
    Key(ctx context.Context, payloadContext PayloadContext, keyID string) ([]byte, error)
}

func NewAESGCMPayloadCodec(keyProvider PayloadEncryptionKeyProvider, options AESGCMPayloadCodecOptions) (*AESGCMPayloadCodec, error)
func NewFilePayloadEncryptionKeyProvider(path string) (*FilePayloadEncryptionKeyProvider, error)
```

`AESGCMPayloadCodecOptions` holds the compression choice and the decompressed
size limit.

### Python

Python has one synchronous and one asynchronous codec interface.

```python
@dataclass(frozen=True)
class PayloadContext:
    flow_type: str
    flow_id: str

class PayloadCodec(Protocol):
    def encode(self, context: PayloadContext, plaintexts: Sequence[bytes]) -> Sequence[PayloadEnvelope]: ...
    def decode(self, context: PayloadContext, envelopes: Sequence[PayloadEnvelope]) -> Sequence[bytes]: ...

class AsyncPayloadCodec(Protocol):
    async def encode(self, context: PayloadContext, plaintexts: Sequence[bytes]) -> Sequence[PayloadEnvelope]: ...
    async def decode(self, context: PayloadContext, envelopes: Sequence[PayloadEnvelope]) -> Sequence[bytes]: ...

class PayloadEncryptionKeyProvider(Protocol):
    def current_key_id(self, context: PayloadContext) -> str: ...
    def key(self, context: PayloadContext, key_id: str) -> bytes: ...
```

- The synchronous Client and Worker accept only a `PayloadCodec`.
- The async Client and Worker accept either interface. They call a
  `PayloadCodec` through `asyncio.to_thread`, so it never blocks the event
  loop.
- `AesGcmPayloadCodec(key_provider, *, compression=..., max_decompressed_bytes=...)`
  implements `PayloadCodec`.
- `FilePayloadEncryptionKeyProvider(path)` reads the key file when it is
  constructed.
- The standard library has no AES-GCM. The SDK uses the `cryptography`
  package as an optional extra.

### TypeScript

TypeScript returns `Promise`s.

```ts
interface PayloadContext {
  readonly flowType: string;
  readonly flowId: string;
}

interface PayloadCodec {
  encode(context: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]>;
  decode(context: PayloadContext, envelopes: readonly PayloadEnvelope[]): Promise<Uint8Array[]>;
}

interface PayloadEncryptionKeyProvider {
  currentKeyId(context: PayloadContext): Promise<string>;
  key(context: PayloadContext, keyId: string): Promise<Uint8Array>;
}
```

- `new AesGcmPayloadCodec(keyProvider, { compression, maxDecompressedBytes })`
  implements `PayloadCodec`.
- `FilePayloadEncryptionKeyProvider.fromFile(path)` returns a
  `Promise<FilePayloadEncryptionKeyProvider>`.

### Options

`ClientOptions` and `WorkerOptions` take one codec and these switches. When a
codec is set, all three encrypt by default.

| Switch | Default with a codec | Effect when off |
| --- | --- | --- |
| String Values | Encrypt | `string_value` is sent in clear |
| Primitive Values | Encrypt | `int_value`, `double_value`, and `bool_value` are sent in clear |
| Error text and reasons | Encrypt | Section 6 fields are sent in clear |

Each SDK names the switches in its own idiom. In Go, the zero value of each
field must mean "encrypt".

## 11. Codec server HTTP protocol

A codec server lets Dex Web and dexcli encode and decode without holding keys.
The browser calls it directly; the Dex Web backend only passes envelopes
through.

### Endpoints

Both endpoints take `POST` with `Content-Type: application/json` and return
`application/json`.

`POST /decode`

```json
{
  "context": { "flow_type": "OrderFlow", "flow_id": "order-1001" },
  "envelopes": ["<base64 PayloadEnvelope>", "..."]
}
```

```json
{ "values": [{ "stringValue": "hello" }, { "intValue": "42" }] }
```

`POST /encode`

```json
{
  "context": { "flow_type": "OrderFlow", "flow_id": "order-1001" },
  "values": [{ "stringValue": "hello" }]
}
```

```json
{ "envelopes": ["<base64 PayloadEnvelope>"] }
```

- `context` is required. `flow_id` is a non-empty string. `flow_type` may be
  the empty string when the caller knows only the Flow ID (section 9). Servers
  MUST NOT reject an empty `flow_type`.
- The response has one entry per request entry, in the same order.
- An empty list returns an empty list.
- Envelopes are bare serialized `PayloadEnvelope`s in standard padded base64.
  The caller handles the outer layer:
  - Object format: the envelope is the `payload` of the `dex/1` object.
  - Prefix format: the envelope is the base64 text after `dex-encoded:`.
    To encode text, send `{"stringValue": text}` and prepend `dex-encoded:`
    to the returned envelope.
- `/decode` checks each envelope's base64 length against
  `maxEncodedTextBytes` before decoding it. It then runs section 7 from step
  3 (decoded size) through step 14.
- `/encode` accepts only the kinds in section 2. It rejects an object whose
  encoding starts with `dex/`.

### Value JSON

Values use the proto3 JSON mapping of `dex.Value`:

- Field names are lowerCamelCase: `stringValue`, `objValue`, `intValue`,
  `doubleValue`, `boolValue`. Readers also accept the proto names, as proto3
  JSON parsers do.
- `intValue` is a decimal string, such as `"-7"`.
- `doubleValue` is a JSON number, or one of the strings `"NaN"`,
  `"Infinity"`, and `"-Infinity"`. Negative zero is written `-0`.
- `objValue.payload` is standard base64 with padding.
- `objValue.encoding` and `objValue.payload` are omitted when empty.

### Errors

A batch succeeds or fails as a whole. A failure returns a non-2xx status and
this body:

```json
{ "error": { "category": "unknown_key", "message": "key id k9 is unknown", "index": 0 } }
```

| Status | When | `category` |
| --- | --- | --- |
| 400 | The request is not valid JSON or breaks this protocol | `malformed` |
| 401 | Credentials are missing or invalid | `unauthenticated` |
| 403 | The caller may not use keys for this context | `forbidden` |
| 413 | The body or the entry count is over the server's limit | `limit_exceeded` |
| 422 | One entry fails to encode or decode. `index` names the first failing entry | A section 7 category |
| 500 | Any other failure | `internal` |

- `message` and logs MUST NOT contain payload bytes, plaintext, or keys.
- Servers MUST limit the request body size and the entries per request.

### Security

- A codec server can decrypt everything encrypted with its keys. `/encode`
  can also create valid ciphertext. Production servers MUST authenticate
  every request. Their access should match who may view Dex Web.
- The request `context` comes from the browser and is not trusted. A
  multi-tenant server chooses the tenant from the authenticated identity,
  not from `context`.
- CORS allows only configured origins, never `*`.
- Expose production servers only over HTTPS.
- dexcli's local codec server also checks that the `Host` header is a
  localhost name and requires a per-start random token. This blocks DNS
  rebinding.
- Clients keep decoded Values only in memory.

## 12. Golden vectors

[`conformance/payload-encryption/vectors.json`](conformance/payload-encryption/vectors.json)
is the cross-SDK contract. Its [README](conformance/payload-encryption/README.md)
describes the file and how to regenerate it.

- **The contract is decoding.** Every SDK MUST decode each positive case to
  its exact plaintext bytes and its expected `Value`. It MUST return each
  passthrough input unchanged. It MUST reject each negative case with the
  expected category.
- Size limit cases lower one limit through the case's `limits` field instead
  of shipping huge files. An SDK test configures its decoder with those
  limits for that case. Each limit has an at-limit case that decodes and a
  one-byte-over case that fails.
- A negative case with a `checkOrder` field breaks more than one rule on
  purpose. It tests the check order. Another case covers each rule alone,
  where that is possible.
- Encoding output need not match byte for byte across SDKs. Interoperability
  tests check encoding instead: SDK X encodes, SDK Y decodes, and the decoded
  structure matches.

## 13. Compatibility

The `dex/1` marker, the `PayloadEnvelope` message, the AAD, the prefix
format, and the key file format are persistent formats. Data written today
must stay decodable by every later SDK.

- Later releases may add `PayloadEnvelope` fields and enum values. They never
  renumber, reuse, or retype a field or a value.
- Older decoders ignore unknown fields. So a new field may only add
  information that older decoders can safely skip. A change that alters how
  `data` must be decoded needs a new `PayloadCipher` or `PayloadCompression`
  value, or a new marker version such as `dex/2`.
- Older decoders reject unknown enum values and unknown marker versions with
  a clear error. They never pass such Values through as plaintext.
- SDKs keep decoding every earlier marker version after a new one ships.
