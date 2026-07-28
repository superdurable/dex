<!---
---

---
--->

<!---## DOCHUB-PATH: advanced-concepts/Persistence.mdx :DOCHUB-PATH ##--->

## Overview

As writing code with programming model, you must have to deal with _data_ everywhere. 
iWF provides a Key-Value storage out of the box. This eliminates the need to depend on a database to implement your workflow.

Your data are stored as Data Attributes and Search Attributes. Together both define the "persistence schema".
The persistence schema is defined and maintained in the code along with other business logic. 

Search Attributes work like infinite indexes in a traditional database. You
only need to specify which attributes should be indexed, without worrying about complications you might be used to in
a traditional database like the number of shards, and the order of the fields in an index. Note that Search Attributes must be created accordingly in the Temporal namespace/Cadence domain, and shared across the whole namespace/domain. 

Logically, the workflow definition displayed in the example workflow diagram will have a persistence schema as follows:

| Workflow Execution   | Search Attr A | Search Attr B | Data Attr C | Data Attr D |
|----------------------|---------------|:-------------:|------------:|------------:|
| Workflow Execution 1 | val 1         |     val 2     |       val 3 |       val 4 |
| Workflow Execution 2 | val 5         |     val 6     |       val 7 |       val 8 |
| ...                  | ...           |      ...      |         ... |         ... |

With Search attributes, you can write [customized SQL-like queries to find any workflow execution(s)](https://docs.temporal.io/visibility#search-attribute), just like using a database query.

Note:
* The scope of the data/search attribute are isolated within its own workflow execution
* Lifecycle: after workflows are closed(completed, timeout, terminated, canceled, failed), all the data retained in your persistence schema will be deleted once the configured retention period elapses.

The iWF persistence is mainly for storing the workflow intermediate states/data.
**It is important to not abuse iWF persistence for things like any large dataset, permanent storage, or for tracking/analytics purpose.**

## Persistence best practices and size limits
DO NOT abuse iWF persistence for large dataset. You should store reference(key, Id) to external storage(database,S3) instead.
 
Best practices for ease of mind is to make sure never store large blob of data into data attributes, channel messages, or state inputs:
* Total data attributes stored in a workflow execution is not greater than 500KB
* For each state execution, the total state input + data/search attributes loaded + commandResults shouldn't be greater than 2MB
* Each data attribute should not greater than 100KB


But if you need to go beyond above best practices, make sure you understand the limits:
* Using "OptimizeActivity" to save the history size for reading the data attribute
* Any write to the data attribute is a full value replacement . So avoid too many updates on large data attributes – because it is always a full write record in the Cadence/Temporal history could cause history size problems 
* For large data attributes(>100KB), do not update it once set(readonly after writing)
  * Delete (setting to NULL) is okay. 
* If using optimizeActivity=true (LocalActivity), each state execution should not update data attributes exceeding 40KB.
* Each signal/internal channel message, should not greater than 100KB 
* If a workflow execution has more than two 2 MB data+search attributes, specify [another persistence loading policy](Persistence.md#persistence-loading-policy) to reduce the loading data. By default, a state is loading all data/search attributes, which could exceed the 2MB limit of activity input.
  * Using dynamic data attributes will help break into smaller pieces for write . 
  * For execute from waitUntil, this also includes the commandResults(eg signal messages)
* Total history size cannot exceed 50MB

## Persistence loading policy

When a workflowState/RPC API loads DataAttributes/SearchAttributes, by default it will use `LOAD_ALL_WITOUT_LOCKING` to load everything. There are other options:

* For WorkflowState, there is a 2MB limit by default to load data. User can use another loading policy `LOAD_PARTIAL_WITHOUT_LOCKING`
to specify certain DataAttributes/SearchAttributes only to load.

* `LOAD_NONE` will skip the loading to save the data transportation/history cost.

* `WITHOUT_LOCKING` here means if multiple StateExecutions/RPC try to upsert the same DataAttribute/SearchAttribute, they can be
done in parallel without locking.

* If racing conditions could be a problem, using`PARTIAL_WITH_EXCLUSIVE_LOCK` or `LOAD_ALL_WITH_PARTIAL_LOCK` allow specifying some keys to be locked during the execution. Both are exclusive locks(like write locks in database). 
  * `PARTIAL_WITH_EXCLUSIVE_LOCK` let you specify a list of data attributes to load, and a list to lock (can be different).   
  * `LOAD_ALL_WITH_PARTIAL_LOCK` means load all the data attributes, but you can provide a list of data attributes to lock. `LOAD_ALL` is like `SELECT *  FROM TABLE ...`. 
  * Note that the locking is only affective to state/RPCs that are also locking. If there is a state/RPC try to read without locking, the locking states/RPC won't prevent them. This is similar to locking in database that the regular read operation will not be blocked by any lock operations/transactions. 

### Locking in RPC
The locking with RPC is only supported by Temporal as backend using [synchronous update feature](https://docs.temporal.io/encyclopedia/workflow-message-passing#sending-updates). 
See [more in this wiki page](RPC-locking%3A-What-does-the-atomicity-of-RPC-really-mean%3F.md).


## SDKs
Defining iWF persistence schema is simply declaring in code the key and value types(if applicable). 
With the type defined for the attribute, the SDK will check the type matching when read/write. (note that the type enforcement is only on the SDK. The server doesn't care about the types for a data/search attribute -- they are just transparent data blobs.


<!---
import { FaJava, FaPython } from "react-icons/fa";
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<div style={{
    "border": "1px darkgray solid",
    "borderRadius": "1rem",
    "padding": "0.5rem"
}}>
<Tabs>
    <TabItem value="java" label={<><FaJava size={26} /> Java</>}>
--->
<!--- ## GITHUB-ONLY ## --->
### Java
<!--- ## END-GITHUB-ONLY ## --->

An [example](../../examples/java/src/main/java/io/iworkflow/workflow/signup/UserSignupWorkflow.java) of Java workflow definition with persistence:
```java
public class UserSignupWorkflow implements ObjectWorkflow {

    public static final String DA_FORM = "Form";

    public static final String DA_Status = "Status";
...

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(SignupForm.class, DA_FORM),
                DataAttributeDef.create(String.class, DA_Status)
        );
    }

...
}
```
Example of read/write persistence in the workflow states or RPCs:
```
        String status = persistence.getDataAttribute(DA_Status, String.class);
        persistence.setDataAttribute(DA_Status, "verified");
```

To access the persistence outside of workflow, you can use [RRC](RPC.md) via client, since RPC has read/write access.
Alternatively, you can use direct APIs:

```
client.getDataAttributes(...)
client.getSearchAttributes(...)
```
The below APIs are WIP:
```
client.setDataAttributes(...) 
client.setSearchAttributes(...)
```


<!---
</TabItem>
<TabItem value="python" label={<><FaPython size={26} /> Python</>}>
--->
<!--- ## GITHUB-ONLY ## --->
### Python
<!--- ## END-GITHUB-ONLY ## --->

[Example](../../examples/python/signup/signup_workflow.py) in Python with persistence:
```python
class UserSignupWorkflow(ObjectWorkflow):

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(data_attribute_form, Form),
            PersistenceField.data_attribute_def(data_attribute_status, str),
            PersistenceField.data_attribute_def(data_attribute_verified_source, str),
        )
```

To read/write persistence in workflow states or RPCs:
```
status = persistence.get_data_attribute(data_attribute_status)
persistence.set_data_attribute(data_attribute_status, "verified")
```
<!---
</TabItem>
<TabItem value="go" label="Golang">
--->
<!--- ## GITHUB-ONLY ## --->
### Golang
<!--- ## END-GITHUB-ONLY ## --->

Due to the limitation of Golang, the Golang SDK doesn't let you define "type" of an attribute. So there is no type checking in the SDK.

This is an [example](../../examples/go/workflows/microservices/workflow.go) of a Golang workflow definition with persistence:
```go
type OrchestrationWorkflow struct {
	iwf.WorkflowDefaults
}

func (e OrchestrationWorkflow) GetPersistenceSchema() []iwf.PersistenceFieldDef {
	return []iwf.PersistenceFieldDef{
		iwf.DataAttributeDef(keyData),
	}
}

func (e OrchestrationWorkflow) GetCommunicationSchema() []iwf.CommunicationMethodDef {
	return []iwf.CommunicationMethodDef{
		iwf.SignalChannelDef(SignalChannelReady),

		iwf.RPCMethodDef(e.MyRPC, nil),
	}
}
```
And example to read/write the persistence:
```go
func (s *MyState)Execute(...){
	var oldData string
	persistence.GetDataAttribute(keyData, &oldData)
	var newData string
	input.Get(&newData)
	persistence.SetDataAttribute(keyData, newData)
        ...
}
```
<!---
</TabItem>
</Tabs>
</div>
--->