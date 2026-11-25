# Decision with Deadline

- `GET /patterns/deferred-choice/start?workflowId={workflowId}&offerId={offerId}`
- `GET /patterns/deferred-choice/accept?workflowId={workflowId}&note={note}`
- `GET /patterns/deferred-choice/reject?workflowId={workflowId}&note={note}`

`OfferResponseFlow` waits in `AwaitOfferResponse` for an acceptance, a rejection, or the 3-day deadline. Its `execute` locks the `offerResponseWindow` Attribute and closes it. Both response RPCs lock the same Attribute. A response sent after the window closed answers `409 offer response window closed`. A response that committed after the Timer won, but before `execute` took the lock, goes through `RouteResponseBeforeExpiry`.

See the [Decision with Deadline design pattern](https://docs.superdurable.io/design-patterns/external-interaction/decision-with-deadline).
