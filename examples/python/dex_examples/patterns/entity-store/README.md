# Entity Store Flow

One `UserProfileFlow` per user keeps the durable profile in Dex while projecting
selected Attributes to PostgreSQL.

The projection covers text, boolean, integer, double, RFC3339 datetime, and JSON
metadata columns.

## Endpoints

- `POST /patterns/entity-store/profile`
- `POST /patterns/entity-store/profile/update`
- `GET /patterns/entity-store/profile?userId=...`
- `POST /patterns/entity-store/profile/clear?userId=...`

The Flow ID becomes PostgreSQL's `user_id` primary key. Clearing a profile
deletes the Dex Attributes and asynchronously writes SQL `NULL` to its columns.

See the shared [PostgreSQL setup](https://github.com/superdurable/dex/tree/main/examples/entity-store)
for Dex Server configuration.
