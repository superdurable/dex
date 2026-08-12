# PostgreSQL Entity Store example

This shared setup demonstrates the Entity Store pattern in the Go, Java, Python,
and TypeScript examples. One `UserProfileFlow` owns each profile, while the Dex
Server asynchronously projects opted-in Attributes to PostgreSQL.

Rust is intentionally not included because its example has not been implemented.

## Start PostgreSQL and Dex

From this directory:

```bash
docker compose up -d --wait
dexcli dev \
  --attribute-store-config ./attribute-store.yaml \
  --temporal-db-filename /tmp/dex-entity-store.db
```

Compose starts PostgreSQL 17 and creates `public.user_profiles` on
`localhost:55432`. `dexcli dev` starts the local Dex environment and loads the
Server store named `entityStore` from `attribute-store.yaml`.

Start any one language example according to its README, then create and update a
profile through that example's HTTP server:

```bash
curl -X POST http://localhost:8080/design-pattern/entity-store/profile \
  -H 'content-type: application/json' \
  -d '{"userId":"user-42","displayName":"Ada Lovelace","email":"ada@example.com","marketingOptIn":true,"credits":120,"weight":59.5,"lastLoggedInTime":"2026-08-11T15:30:00Z","metadata":{"source":"example","tags":["computing","poetry"]}}'

curl -X POST http://localhost:8080/design-pattern/entity-store/profile/update \
  -H 'content-type: application/json' \
  -d '{"userId":"user-42","displayName":"Ada Byron","email":"ada.byron@example.com","marketingOptIn":false,"credits":180,"weight":60.25,"lastLoggedInTime":"2026-08-12T09:45:00Z","metadata":{"source":"example","tags":["computing","history"]}}'
```

All four examples default to port `8080`; run one language application at a time
or override its HTTP address.

## Observe the projection

Projection is asynchronous and latest-state only, so poll PostgreSQL rather than
assuming the HTTP response means the SQL row is already visible:

```bash
docker compose \
  exec entity-postgres psql -U entity_store -d entity_store \
  -c "SELECT * FROM user_profiles WHERE user_id = 'user-42';"
```

Clear the synced values with:

```bash
curl -X POST 'http://localhost:8080/design-pattern/entity-store/profile/clear?userId=user-42'
```

The row remains and its seven projected columns become SQL `NULL`. A projection
failure is logged but never rolls back the authoritative Flow Attribute write.

Stop `dexcli` with Ctrl+C, then remove PostgreSQL with `docker compose down -v`.
