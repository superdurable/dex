
![Screenshot 2023-04-19 at 11 16 06 AM](https://user-images.githubusercontent.com/4523955/233166062-a0702f2a-cbe5-494a-8c36-d394ec9b1e92.png)
* Also
  see [JobPostController](https://github.com/superdurable/iwf/blob/main/examples/java/src/main/java/io/iworkflow/controller/JobPostController.java)
  for how to implement APIs based on this workflow implementation

How to test the APIs in browser:

* start API: http://localhost:8803/jobpost/create?title="Software Engineer"&description="in Seattle"
    * It will return the workflowId which can be used in subsequence API calls.
* read API: http://localhost:8803/jobpost/read?workflowId=<TODO>
* update API: http://localhost:8803/jobpost/update?workflowId=<TODO>&title="Senior Software Engineer"&description="in Portland"&notes=testnotes
* search API, Title and Description can be used as full-text searching. Try queries like:
    * ['Title="software" AND JobDescription="Portland" ORDER BY LastUpdateTimeMillis '](http://localhost:8803/jobpost/search?query='Title="software" AND JobDescription="Seattle" ORDER BY LastUpdateTimeMillis' )
    * etc
* soft delete API: http://localhost:8803/jobpost/delete?workflowId=<TODO>

### Search attribute requirement

If using Temporal:

* New CLI(temporal)

```bash
temporal search-attribute create -name Title -type Text -y
temporal search-attribute create -name JobDescription -type Text -y
temporal search-attribute create -name LastUpdateTimeMillis -type Int -y
```

* Old CLI(tctl)

``` bash
tctl adm cl asa -n Title -t Text
tctl adm cl asa -n JobDescription -t Text
tctl adm cl asa -n LastUpdateTimeMillis -t Int

```

If using Cadence

```bash
cadence adm cl asa --search_attr_key Title --search_attr_type 0
cadence adm cl asa --search_attr_key JobDescription --search_attr_type 0
cadence adm cl asa --search_attr_key LastUpdateTimeMillis --search_attr_type 2
```
