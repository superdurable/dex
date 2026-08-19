# Shortlist candidates

Employer opt-in singleton plus shortlist/revoke automation. Shortlist waits five
minutes (or revoke) before emailing when the employer is opted in.

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
