# Dex Web trusted-mount acceptance

This checklist verifies request-specific reverse-proxy mounting before a Dex
release is used by a host application.

## Automated checks

```bash
cd web
npm run check
npm run build
GOWORK=off go test ./...
```

The Go suite covers standalone header rejection, malformed trusted metadata,
frame policy, HTML bootstrap and asset rewriting, and two simultaneous proxy
mounts backed by one Dex Web server. The frontend suite covers router/API path
prefixing and CSRF propagation on mutating requests.

## Proxy contract

1. Enable `web.trustForwardedEmbeddingHeaders` only on a Dex Web listener that
   the public network cannot reach directly.
2. Remove every browser-supplied `X-Forwarded-Prefix`, `X-Dex-Web-Embedded`, and
   `X-Dex-Web-CSRF-Token` value before injecting the trusted values.
3. Strip the public prefix before sending the path to Dex Web. Preserve its
   query string.
4. Validate `X-CSRF-Token` against the authenticated host session for every
   method other than GET, HEAD, and OPTIONS. Do not forward that browser header
   after validation.
5. Inject the canonical public path, embedding mode, and a session-bound CSRF
   bootstrap token on every request sent to Dex Web.
6. Keep the iframe and parent application on the same origin. Embedded Dex Web
   intentionally uses `frame-ancestors 'self'`.

## Manual verification

1. Open the same Dex Web service under two different project paths in separate
   tabs. Confirm each tab keeps its own project path for assets, navigation, API
   reads, edits, and Actions.
2. Refresh a nested v1 run URL and a nested v2 Run and Work Queue URL. Confirm
   the SPA loads directly without a redirect to the origin root.
3. Confirm embedded mode keeps Run, Work Queue, timezone, and theme controls but
   omits the redundant Dex product brand and version selector.
4. Send forged forwarded embedding headers through the public endpoint. Confirm
   the proxy replaces them. Send them directly to a default Dex Web listener
   and confirm Dex returns HTTP 403.
5. Change the active Flow Definition revision while a v2 page is open. Confirm
   the existing revision recovery reloads the catalog within the same mount
   path and requires Action confirmation again.
6. Stop the FlowService upstream. Confirm `/readyz` fails through each mount and
   returns healthy again after the upstream recovers.

Dex Web does not authenticate host users, map project membership, authorize
permissions, or validate the host CSRF token. Those remain host control-plane
and reverse-proxy responsibilities.
