# Shortlist candidates

On a hiring site, an employer opts into an automation that emails shortlisted
candidates on their behalf. Two Flows cooperate:

- `EmployerOptInFlow` is one long-running Flow per employer. It stores the
  opt-in state as a durable Attribute and completes when the employer opts out.
- `ShortlistFlow` starts once per shortlisted candidate. It waits five minutes
  before emailing, and completes without sending if the employer revokes the
  shortlist first.

`workflow_ids` builds the deterministic Flow IDs that let the two Flows find
each other: `employer_opt_in(employer_id)` and
`shortlist(employer_id, candidate_id)`.

## Reading opt-in state across Flows

`SendEmail` has to ask a different Flow whether the employer is still opted in.
It does that through the `OptInChecker` protocol, which `ShortlistFlow` takes in
its constructor. `ClientOptInChecker` is the production implementation: it
invokes `EmployerOptInFlow.is_opted_in` over a `Client` and treats a
`FLOW_NOT_EXISTS` error as "not opted in", matching the Java sample.

`ClientOptInChecker` receives a `Callable[[], Client]` rather than a `Client`,
because a `Client` needs a `Registry` that already contains both Flows. Tests
can inject any object with an `is_opted_in(employer_id) -> bool` method instead.

The Worker synchronizes the Flow's Indexed Attributes automatically before
opening its listener.

With the sample server running:

```text
POST http://localhost:8080/products/shortlist-candidates/opt_in
POST http://localhost:8080/products/shortlist-candidates/opt_out
GET  http://localhost:8080/products/shortlist-candidates/is_opted_in?employerId=<id>
POST http://localhost:8080/products/shortlist-candidates/shortlist
POST http://localhost:8080/products/shortlist-candidates/revoke_shortlist
GET  http://localhost:8080/products/shortlist-candidates/email_sent_timestamp?employerId=<id>&candidateId=<id>
```
