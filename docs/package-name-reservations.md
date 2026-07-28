# Package name reservations (Dex SDKs)

Placeholder packages under `sdk-*` reserve registry names for future Dex SDKs.

## Chosen names

| Language | Registry | Package name | Status (checked) | Directory |
|----------|----------|--------------|------------------|-----------|
| Java | Maven Central | `io.superdurable:dex-sdk` | Already registered by you | `sdk-java/` |
| Python | PyPI | `dex-python-sdk` | Already registered by you (`dex-sdk` / `dexsdk` conflicted) | `sdk-python/` |
| Go | module path | `github.com/superdurable/dex/sdk-go` | Verified by GitHub org (no flat-name squat) | `sdk-go/` |
| **Rust** | crates.io | **`dex-sdk`** | Available | `sdk-rust/` |
| **Ruby** | RubyGems | **`dex-sdk`** | Available | `sdk-ruby/` |
| **C# / .NET** | NuGet | **`Dex.Sdk`** | Available (NuGet form of `dex-sdk`) | `sdk-csharp/` |
| **PHP** | Packagist | **`superdurable/dex-sdk`** | Available | `sdk-php/` |
| **TypeScript** | npm | **`@superdurable/dex-sdk`** | Use scope — unscoped `dex-sdk` is **taken** | `sdk-typescript/` |

C# namespace in code: `SuperDurable.Dex`  
PHP namespace in code: `SuperDurable\Dex`

---

## 1. Rust — crates.io (`dex-sdk`)

1. Create account: https://crates.io/  
2. Verify email; enable 2FA if prompted.  
3. Get an API token: https://crates.io/settings/tokens → **New token** (scope: publish).  
4. Locally (needs Rust toolchain):

```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
cd sdk-rust
cargo login   # paste token
cargo publish --allow-dirty   # or commit first, then cargo publish
```

5. Confirm: https://crates.io/crates/dex-sdk  

Optional: add crates.io “Trusted Publishing” later for GitHub Actions.

---

## 2. Ruby — RubyGems (`dex-sdk`)

1. Create account: https://rubygems.org/sign_up  
2. Enable MFA: https://rubygems.org/settings/edit  
3. Create API key: https://rubygems.org/settings/api_keys → **New API key** (permission: Push rubygem).  
4. Locally:

```bash
cd sdk-ruby
gem build dex-sdk.gemspec
gem push dex-sdk-0.0.1.gem
# When prompted, use the API key as password (username can be your RubyGems username)
```

Or configure credentials:

```bash
mkdir -p ~/.gem
echo ':rubygems_api_key: YOUR_KEY' > ~/.gem/credentials
chmod 0600 ~/.gem/credentials
gem push dex-sdk-0.0.1.gem
```

5. Confirm: https://rubygems.org/gems/dex-sdk  

---

## 3. C# / .NET — NuGet (`Dex.Sdk`)

1. Create account: https://www.nuget.org/users/account/LogOn (Microsoft / GitHub login).  
2. Create API key: https://www.nuget.org/account/apikeys → **Create**  
   - Glob pattern: `Dex.Sdk` (or `*` for first publish)  
   - Push / Unlist as needed  
3. Install .NET SDK: https://dotnet.microsoft.com/download  
4. Locally:

```bash
cd sdk-csharp
dotnet pack -c Release
dotnet nuget push bin/Release/Dex.Sdk.0.0.1.nupkg \
  --api-key YOUR_NUGET_API_KEY \
  --source https://api.nuget.org/v3/index.json
```

5. Confirm: https://www.nuget.org/packages/Dex.Sdk  

Optional later: [ID prefix reservation](https://learn.microsoft.com/en-us/nuget/nuget-org/id-prefix-reservation) for `SuperDurable.` if you also publish `SuperDurable.*` packages.

---

## 4. PHP — Packagist (`superdurable/dex-sdk`)

Packagist packages are usually submitted **from a public Git repo**, not uploaded as a tarball.

1. Push this monorepo (or at least `sdk-php/`) to GitHub `superdurable/dex` on `main`.  
2. Create Packagist account: https://packagist.org/login (GitHub login recommended).  
3. Submit package: https://packagist.org/packages/submit  
   - Repository URL: `https://github.com/superdurable/dex`  
4. Packagist will read `sdk-php/composer.json` **only if** that file is at the repo root **or** you use a **subtree / separate repo**.

**Important:** Packagist expects `composer.json` at the **repository root** by default.

### Recommended options

**Option A — temporary dedicated repo (simplest for name squat)**  
1. Create empty public repo `superdurable/dex-sdk-php` (or `superdurable/dex-php`).  
2. Copy `sdk-php/*` into that repo root (so `composer.json` is at root).  
3. Tag `v0.0.1` and push.  
4. Submit that repo URL on Packagist.  

**Option B — monorepo with Packagist “subdirectory”**  
Packagist does not natively support subdirectory packages well; prefer Option A for the placeholder, then migrate later.

5. Confirm: https://packagist.org/packages/superdurable/dex-sdk  

Also claim the **vendor** `superdurable` by publishing this first package under that vendor name.

---

## 5. TypeScript — npm (`@superdurable/dex-sdk`)

Unscoped `dex-sdk` is already published by someone else on npm → use scoped package.

1. Create npm account: https://www.npmjs.com/signup  
2. Enable 2FA: https://www.npmjs.com/settings/~/account  
3. Create org (if needed): https://www.npmjs.com/org/create → org name **`superdurable`**  
   - Or: `npm org create superdurable` after `npm login`  
4. Install Node.js, then:

```bash
cd sdk-typescript
npm login
npm install
npm run build
npm publish --access public
```

First publish of a scoped package requires `--access public` (default for scoped is private/paid).

5. Confirm: https://www.npmjs.com/package/@superdurable/dex-sdk  

---

## Suggested order

1. **npm org `superdurable`** + publish `@superdurable/dex-sdk` (org claim)  
2. **crates.io** `dex-sdk`  
3. **RubyGems** `dex-sdk`  
4. **NuGet** `Dex.Sdk`  
5. **Packagist** via small dedicated GitHub repo (Option A)

---

## After publishing

You can leave these placeholders at `0.0.1` until real SDKs exist. Later, bump versions in-place under the same package names (except Python, which stays `dex-python-sdk`).
