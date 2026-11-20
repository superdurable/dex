# Dex codec server (TypeScript)

A deployable codec server for Dex payload encryption. Dex Web and dexcli call
it to show and edit encrypted values without holding the keys themselves.

This is not a Worker. It runs no Flows and never connects to Dex.

## What a codec server does

With payload encryption, your Workers and Clients encrypt eligible values
before they reach the Dex server. Which values are eligible depends on the
codec options and on the Attribute rules: Indexed Attributes, Attributes
declared `Plaintext`, and null values stay in clear. Dex stores the rest only
as ciphertext. Dex Web and dexcli have no keys, so they show those values as
encrypted.

A codec server holds the same keys as your Workers. It exposes two endpoints:

- `POST /decode` turns envelopes into values. Dex Web calls it from the
  browser before it renders encrypted values.
- `POST /encode` turns values into envelopes. Dex Web calls it before it
  writes a value, such as a Channel message or an Attribute update.

The browser calls the codec server directly. The Dex Web backend never sees
keys or decrypted values. Decoded values stay in browser memory.

The protocol is section 11 of
[`protos/payload-encryption.md`](../../../protos/payload-encryption.md). This
server uses the TypeScript SDK's built-in `AesGcmPayloadCodec`, so it reads
and writes exactly what your Workers do. It uses `node:http` and has no
runtime dependency besides `@superdurable/dex`.

## Security: this server is a decryption oracle

Read this before you deploy.

- **Anyone who passes authentication can decrypt all data under these
  keys.** One request to `/decode` returns the plaintext of any envelope
  copied from Dex.
- **`/encode` can forge ciphertext.** An authenticated caller can create
  valid encrypted values that your Workers will trust as their own.
- **Give it the same audience as Dex Web.** Allow only the people who may
  already view Flow data in Dex Web. The server refuses to start without
  authentication.
- **Authorize each tenant by identity.** A multi-tenant server must choose
  the tenant, and so the keys, from the authenticated identity. The request
  `context` (`flow_type`, `flow_id`) comes from the browser and is not
  trusted. Never pick keys from it. Put that logic in a
  `CallerPayloadCodecSelector` in [`src/auth.ts`](src/auth.ts). It throws
  `CallerForbiddenError`, which answers 403, for a caller with no tenant.
- **Expose it only over HTTPS.** Run it behind a TLS-terminating reverse
  proxy or load balancer. It serves plain HTTP itself.
- **CORS allows only the origins you list.** `*` is rejected at startup.
- **Logs never contain payloads, keys, or tokens.** Each request logs one
  JSON line with method, path, status, duration, origin, caller, item count,
  and the error category and key ID on failure.

## Run locally

Use Node.js 22.18 or later, or Node.js 24. Node.js runs the TypeScript
sources directly by stripping types, so there is no build step.

Create a key file. Use the same file for your Workers and Clients. With
dexcli:

```bash
dexcli codec-server keygen -o payload-keys.json
```

Or write one by hand. Each key is 32 random bytes in standard padded base64.
Keep the file at mode `0600`:

```json
{
  "current_key_id": "k1",
  "keys": { "k1": "<base64 of 32 random bytes>" }
}
```

Install and start the server:

```bash
cd examples/typescript/codec-server
npm install
export DEX_CODEC_SERVER_BEARER_TOKEN="$(openssl rand -hex 32)"
npm start -- \
  --key-file payload-keys.json \
  --allowed-origins http://localhost:8802 \
  --auth-mode bearer-token \
  --listen-address 127.0.0.1:8080
```

For a local-only setup, `dexcli dev` can also start a built-in codec server.
This example is for shared and production deployments.

## Configuration

Each flag defaults to its environment variable. Secrets are read only from
the environment or from files, never from flags, because flags show up in
process lists.

| Flag | Environment variable | Default | Meaning |
| --- | --- | --- | --- |
| `--listen-address` | `DEX_CODEC_SERVER_LISTEN_ADDRESS` | `:8080` | HTTP listen address |
| `--key-file` | `DEX_CODEC_SERVER_KEY_FILE` | | Path of the key file |
| | `DEX_CODEC_SERVER_KEYS_JSON` | | The key file JSON itself. Set this or `--key-file`, not both |
| `--allowed-origins` | `DEX_CODEC_SERVER_ALLOWED_ORIGINS` | | Comma-separated Dex Web origins, such as `https://dex-web.example.com`. Required |
| `--allow-credentials` | `DEX_CODEC_SERVER_ALLOW_CREDENTIALS` | `false` | Let browsers send cookies, for an SSO proxy that uses cookies |
| `--auth-mode` | `DEX_CODEC_SERVER_AUTH_MODE` | | `bearer-token` or `trusted-proxy-header`. Required |
| | `DEX_CODEC_SERVER_BEARER_TOKEN` | | Shared bearer token, at least 32 bytes |
| `--bearer-token-file` | `DEX_CODEC_SERVER_BEARER_TOKEN_FILE` | | File that holds the bearer token instead |
| `--trusted-identity-header` | `DEX_CODEC_SERVER_TRUSTED_IDENTITY_HEADER` | | Header your SSO proxy sets to the user, such as `X-Forwarded-Email` |
| `--max-request-body-bytes` | `DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES` | `33554432` (32 MiB) | Larger bodies get 413 |
| `--max-items-per-request` | `DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST` | `1000` | More entries get 413 |
| `--max-decoded-bytes-per-request` | `DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST` | `67108864` (64 MiB) | Total decoded Value JSON of one `/decode` response. More gets 413. Bounds gzip amplification across entries |
| `--max-envelope-bytes` | `DEX_CODEC_SERVER_MAX_ENVELOPE_BYTES` | `16842752` | Envelope size limit. Match your Workers' `maxPayloadEnvelopeBytes` |
| `--max-plaintext-bytes` | `DEX_CODEC_SERVER_MAX_PLAINTEXT_BYTES` | `67108864` | Decrypted value limit. Match your Workers' `maxPayloadPlaintextBytes` |
| `--shutdown-timeout` | `DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT` | `15s` | Time for in-flight requests on SIGTERM, such as `500ms`, `15s`, or `1m` |

`GET /healthz` returns `{"status":"ok"}` without authentication, for load
balancer and orchestrator health checks.

The request `context.flow_id` is required. `context.flow_type` may be empty,
because clients that know only a Flow ID send it empty.

## Authentication

Pick one mode. To use your own identity provider, implement
`RequestAuthenticator` in [`src/auth.ts`](src/auth.ts).

**`bearer-token`.** Every request carries `Authorization: Bearer <token>`.
The server compares SHA-256 digests with `crypto.timingSafeEqual`, so the
comparison takes constant time. This is the simplest mode for a small team
or for dexcli. Every holder of the token can decrypt everything, so rotate it
like any shared secret.

**`trusted-proxy-header`.** An SSO proxy in front of the server, such as
oauth2-proxy or your load balancer's OIDC integration, authenticates the
user. It then sets a header, such as `X-Forwarded-Email`, which the server
records as the caller. A request with the header missing, empty, or repeated
gets 401. Use this mode only when both of these hold:

- The proxy removes any copy of that header sent by the client.
- Nothing but the proxy can reach the server, for example because it listens
  on a private network or on `127.0.0.1` in the same pod.

Otherwise anyone can send the header and claim any identity.

When the proxy authenticates with a session cookie, set `--allow-credentials`
and turn on credentials for the codec server in Dex Web. Two proxy settings
are then required:

- Pass CORS preflight `OPTIONS` requests through without authentication.
  Browsers send them without cookies. oauth2-proxy, for example, has
  `--skip-auth-preflight`. This server still checks their origin.
- Send the cookie cross-origin. When the codec server and Dex Web are on
  different sites, the session cookie needs `SameSite=None; Secure`. Sibling
  subdomains of one site work with `SameSite=Lax`.

Run one server per audience if you need both modes, such as one for Dex Web
and one for dexcli.

## Deploy

Build the image from this directory. It runs on `node:24-slim` as the
non-root `node` user:

```bash
docker build -t dex-codec-server .
docker run --rm -p 127.0.0.1:8080:8080 \
  -e DEX_CODEC_SERVER_KEYS_JSON="$(cat payload-keys.json)" \
  -e DEX_CODEC_SERVER_ALLOWED_ORIGINS=https://dex-web.example.com \
  -e DEX_CODEC_SERVER_AUTH_MODE=bearer-token \
  -e DEX_CODEC_SERVER_BEARER_TOKEN="$TOKEN" \
  dex-codec-server
```

The image installs the `@superdurable/dex` version that `package-lock.json`
pins from npm.

In production, mount the key file or inject `DEX_CODEC_SERVER_KEYS_JSON`
from your secret manager. Do not bake keys into the image.

Put a TLS-terminating reverse proxy in front of it. For example, with
Caddy:

```text
codec.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

The server stops accepting connections on SIGTERM and lets in-flight
requests finish within `--shutdown-timeout`. If they do not finish in time,
it closes the remaining connections and exits with status 1.

## Configure Dex Web and dexcli

In Dex Web settings, set the codec server endpoint to the public HTTPS URL,
such as `https://codec.example.com`. The setting stays in your browser. Add
the Dex Web origin to `--allowed-origins`, exactly as the browser shows it,
with scheme and port.

Browsers authenticate with credentials such as an SSO session cookie. For
Dex Web, use `trusted-proxy-header` behind an SSO proxy, set
`--allow-credentials`, and turn on credentials for the codec server in Dex
Web settings. `bearer-token` mode suits dexcli and other non-browser clients.

If Dex Web is served with a Content-Security-Policy, its `connect-src` must
allow the codec server URL.

dexcli flow commands connect to the same endpoint with their codec endpoint
option. dexcli reads the bearer token from an environment variable or a file.
See the Developer reference for the exact names.

Dex Web and dexcli create new Attributes in plaintext, because they do not
know whether your application declared them `Plaintext`. An update to an
existing Attribute keeps the stored value's form.

## Use a KMS or secret manager

The key source is `createPayloadEncryptionKeyProvider` in
[`src/keys.ts`](src/keys.ts). Replace it in one of two ways:

1. **Keep AES-256-GCM and load the keys from your KMS.** Implement
   `PayloadEncryptionKeyProvider`. `currentKeyId` returns the key ID for new
   encryptions. `key` returns the 32-byte key for a key ID, for example a
   data key that your KMS unwraps once and the provider caches in memory.
   Envelopes stay compatible with every Worker that uses the same keys.
2. **Encrypt in the KMS itself.** Implement `PayloadCodec` with
   `PAYLOAD_CIPHER_CUSTOM`. Your Workers and Clients must then use the same
   custom codec. A custom codec must authenticate or reject envelope
   `metadata`. See the `PayloadCodec` doc comment.

For a multi-tenant server, choose a provider or codec per tenant in a
`CallerPayloadCodecSelector`, from the authenticated caller.

`/decode` calls the codec once per envelope, so memory stays within the
per-request budget. A KMS-backed provider should cache keys by key ID rather
than call the KMS for every envelope.

Error responses and logs never include codec or key provider error text. A
decoding failure returns a fixed message for its category and the envelope's
key ID. Other failures return `internal` and log only an internal error code.

## Rotate keys

1. Add the new key to the key file and keep `current_key_id` on the old key.
   Deploy the file to every Worker, Client, and codec server, then restart
   them. Every reader can now decrypt both keys.
2. Point `current_key_id` at the new key. Deploy and restart again. New
   values use the new key.
3. Keep the old key in `keys`. Values written with it stay readable only
   while some key file still has it.

Encrypt at most 2^32 values with one key, and rotate regularly. Rotate the
bearer token separately: update the secret, restart the server, and update
Dex Web and dexcli.

## Errors

Failures return a non-2xx status and
`{"error": {"category", "message", "index"}}`. `index` names the first
failing entry for 422 errors.

| Status | Category | When |
| --- | --- | --- |
| 400 | `malformed` | Not valid protocol JSON, missing `context.flow_id`, not POST, or not `application/json` |
| 401 | `unauthenticated` | Missing or invalid credentials |
| 403 | `forbidden` | Origin not allowed, or the caller may not use these keys |
| 413 | `limit_exceeded` | Body, entry count, or total decoded size over the limit |
| 422 | A decoding category | One entry fails to encode or decode, such as `unknown_key` or `authentication_failed` |
| 500 | `internal` | Any other failure |

## Tests

```bash
npm run typecheck
npm test
```

The tests decode values encrypted by the TypeScript SDK's codec with a shared
key file, and decode the server's `/encode` output with the SDK. They run
every envelope case of the cross-SDK golden vectors in
[`protos/conformance/payload-encryption/vectors.json`](../../../protos/conformance/payload-encryption/vectors.json),
and check the hand-written `dex.Value` protobuf code against the vectors'
plaintexts. They also cover CORS, authentication, limits, error bodies,
graceful shutdown, and that responses and logs hold no payloads, keys,
tokens, or codec error text.
