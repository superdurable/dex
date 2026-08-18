# Shortlist candidates

Employer opt-in singleton plus shortlist/revoke automation. Shortlist waits five minutes (or revoke) before emailing when the employer is opted in.

With the sample server running:

```text
POST http://localhost:8080/shortlist_candidates/opt_in
POST http://localhost:8080/shortlist_candidates/opt_out
GET  http://localhost:8080/shortlist_candidates/is_opted_in?employerId=<id>
POST http://localhost:8080/shortlist_candidates/shortlist
POST http://localhost:8080/shortlist_candidates/revoke_shortlist
GET  http://localhost:8080/shortlist_candidates/email_sent_timestamp?employerId=<id>&candidateId=<id>
```
