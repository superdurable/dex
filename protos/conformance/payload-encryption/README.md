# Payload encryption golden vectors

[`vectors.json`](vectors.json) holds the decode vectors for the `dex/1`
payload encryption format. The format is specified in
[`../../payload-encryption.md`](../../payload-encryption.md).

## The contract

Every SDK test suite loads `vectors.json` and checks:

- **positive**: decoding `input` gives exactly `expectedPlaintext` (the
  serialized inner `Value`) and a `Value` equal to `expectedValue`. Text cases
  also give `expectedText`.
- **passthrough**: `input` is not an envelope and comes back unchanged. This
  includes `text-space-escaped-prefix`, the form a writer without a codec
  produces for plaintext that starts with `dex-encoded:`.
- **negative**: decoding `input` fails with the `expectedError` category.

Encoding output need not match these bytes. Each encryption uses a random
nonce, and protobuf serializers may differ across languages.

## File format

- `keyFile` uses the key file JSON format. Load it with the SDK's file key
  provider. `keysHex` repeats the keys in hex.
- `k1-alias` has the same bytes as `k1`. Relabeling `k1` as `k1-alias` breaks
  only the AAD, so `aad-mismatch-key-id` tests the AAD alone.
- `input` holds exactly one of:
  - `value`: a `dex.Value` in proto3 JSON, such as
    `{"objValue": {"encoding": "dex/1", "payload": "<base64 envelope>"}}`;
  - `text`: the content of a string field, such as an error `detail`.
- `envelope` in positive cases summarizes the header, the nonce, and the AAD
  for debugging.
- `expectedValue` is proto3 JSON. TypeScript generates no JSON methods, so
  parse it by hand: `intValue` is a decimal string, and `doubleValue` may be
  `"NaN"` or `"Infinity"`.
- Compare doubles carefully. `double-negative-zero` must keep its sign bit.
  `double-nan` must be NaN; do not compare NaN bits or use `==`.
- The top-level `limits` and `errorCategories` repeat the specification's
  defaults. Decode every case with these limits unless it has its own.
- A case with its own `limits` lowers one size limit, so the file stays
  small. Configure the decoder with exactly those limits for that case. Each
  of `maxEnvelopeBytes`, `maxEncodedTextBytes`, `maxDataBytes`, and
  `maxPlaintextBytes` has an at-limit positive case and a one-byte-over
  negative case. The key ID and metadata negatives are one over their fixed
  limits.
- **Check-order cases.** A negative case with a `checkOrder` field breaks
  more than one rule on purpose. It checks which rule the decoder applies
  first, and `checkOrder` explains why. Other cases cover each rule alone,
  except the metadata limits: for the built-in codec, any metadata also
  breaks its empty-metadata rule.

## Regenerate

The generator is [`generate_vectors.go`](generate_vectors.go). It uses only
the Go standard library and encodes protobuf by hand. It derives a fixed
nonce from each case name, so its output is reproducible. Fixed nonces exist
only here, never in an SDK. The gzip bytes of `string-gzip` are pinned in the
generator, so a Go deflate change cannot alter the file.

```bash
cd protos/conformance/payload-encryption
GOWORK=off go run .          # rewrite vectors.json
GOWORK=off go run . -check   # fail if vectors.json differs from the generator output
GOWORK=off go test ./...     # freshness, AAD worked example, and key file checks
```

Generated Code CI runs these tests for every change under `protos/`.

Each SDK decodes the vectors in its own test suite, like the other
conformance files. Go does so in
[`sdk-go/dex/payload_encryption_conformance_test.go`](../../../sdk-go/dex/payload_encryption_conformance_test.go).
That test decodes with the generated `dexpb` types, so it also checks the
generator's hand-written protobuf against the schema.

`vectors.json` is a frozen contract, not a derived file. Do not change an
existing case. Add new cases instead, and keep every old case decodable.
