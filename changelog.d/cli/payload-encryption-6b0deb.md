---
section: Added
---
- **dexcli codec-server -key-file key.json** serves a local codec server (**POST /encode**, **POST /decode**) for encrypted payloads. It is for local development only: it listens only on 127.0.0.1, accepts only localhost **Host** headers, requires a random bearer token generated at each start, and allows CORS only from the given Dex Web origins.
- **dexcli codec-server keygen -o key.json** writes a payload key file with one random 32-byte key and mode 0600. It refuses to replace an existing file unless you pass **-force**.
- **dexcli dev -codec-key-file key.json** starts that codec server next to Dex Web and hands its endpoint and token to Dex Web and to **dexcli flow** commands against the same local server. dexcli never generates a key.
- **dexcli flow** commands accept **--codec-key-file**, or **--codec-endpoint** with a token from **--codec-token-file** or **DEX_CODEC_TOKEN**. They decrypt stored values and **dex-encoded:** error texts before they print them. They encrypt the **flow start** input, the **flow stop --result** output, and the Stop, Time Travel, and clone-fork reasons. New Attributes stay plaintext, because dexcli cannot know which Attributes the application declared **Plaintext**.
- Without a codec, a Stop, Time Travel, or clone-fork reason that starts with **dex-encoded:** is sent with one leading space, so readers never try to decrypt it.
