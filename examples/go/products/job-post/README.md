# Job posting

CRUD job posting with indexed Attributes and job-board updates. Create seeds initial Attributes; update starts the LinkedIn and Indeed Steps in parallel; search uses Dex SearchFlows.

With the sample server running:

```text
http://localhost:8080/products/job-post/create?title=<title>&description=<description>
http://localhost:8080/products/job-post/read?workflowId=<flow-id>
http://localhost:8080/products/job-post/update?workflowId=<flow-id>&title=<title>&description=<description>&notes=<notes>
http://localhost:8080/products/job-post/delete?workflowId=<flow-id>
http://localhost:8080/products/job-post/search?query=<query>
```
