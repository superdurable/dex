# Decision with Deadline

- `GET /patterns/deferred-choice/start?workflowId={workflowId}&offerId={offerId}`
- `GET /patterns/deferred-choice/accept?workflowId={workflowId}&note={note}`
- `GET /patterns/deferred-choice/reject?workflowId={workflowId}&note={note}`

`OfferResponseFlow` waits in `AwaitOfferResponse` for an acceptance, a rejection, or the 3-day deadline. Both response RPCs lock the `offerResponseWindow` Attribute. The first response is recorded and closes the window; every later response answers `409 offer response window closed`. `AwaitOfferResponse.Execute` locks the same Attribute, closes the window, and has a short retry budget. A response that committed after the Timer won, but before `Execute` took the lock, goes through `RouteResponseBeforeExpiry`. A response sent after the Flow ended also answers `409`, and a response to a Flow that never existed answers `404 offer response flow not found`.

See the [Decision with Deadline design pattern](https://docs.superdurable.io/design-patterns/external-interaction/decision-with-deadline).
