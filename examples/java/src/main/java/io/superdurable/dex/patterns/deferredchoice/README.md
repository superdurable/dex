# Decision with Deadline

- `GET /patterns/deferred-choice/start?workflowId={workflowId}&offerId={offerId}`
- `GET /patterns/deferred-choice/accept?workflowId={workflowId}&note={note}`
- `GET /patterns/deferred-choice/reject?workflowId={workflowId}&note={note}`

`OfferResponseFlow` waits in `AwaitOfferResponse` for an acceptance, a rejection, or the 3-day deadline. Both response RPCs lock the `offerResponseWindow` Attribute. The first response publishes and closes the window in one transaction, so a second response gets "window closed". `AwaitOfferResponse.execute` locks the same Attribute and closes it, with a short retry budget because it holds the lock through its retries. A response that committed after the Timer won, but before `execute` took the lock, goes through `RouteResponseBeforeExpiry`.

The routes retry a lock conflict a few times. A closed window, including a Flow that already closed, answers `409 offer response window closed`. An unknown `workflowId` keeps the Dex error.

See the [Decision with Deadline design pattern](https://docs.superdurable.io/design-patterns/external-interaction/decision-with-deadline).
