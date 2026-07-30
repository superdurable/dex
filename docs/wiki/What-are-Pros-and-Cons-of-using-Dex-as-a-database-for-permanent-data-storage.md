# Dex as permanent data storage

Dex provides persistence like a database out of the box. The main purpose is to make development fast and easy. Because workflow development always involve persisting states. 

Typically, workflow only need the storage during the workflow execution. After workflow closed, all the data will be deleted once the configured retention period elapses.

However, by setting the workflow timeout to zero(0 means infinite), or very large, and then never complete/fail the workflow, user can essentially use Dex workflow as a "permanent storage". 

## Pros
* Much simpler to use, without maintaining a database at all. The schema is defined in code along with the actual workflow business logic
* Much more powerful to build complicated logic than just storage. E.g. A workflow RPC can trigger state execution to synchronize with another system, or using timers for more complicated logic, all in one place. 
* The search attributes are much more powerful/flexible than indexes in traditional databases, because the indexing is backed by ElasticSearch. 
* Much better debugging/troubleshooting experience, with history to show every write/update
* It’s very easy to migrate from workflow to database later(but no opposite) 
  * Let every write to also write to DB
  * Add a new state in the workflow
  * Trigger the state execution to sync to database using a [batch signal command](https://docs.temporal.io/cli/batch) 
  * Change the code to write to DB after the triggering is done execution 

## Cons
* More expensive to write, and store than a traditional database. Since every write will be recorded as a history event. So not a good choice for very heavy write applications
* More complicated and not efficient to implement transactions across workflows(like updating multiple rows in a database)
* It’s not a popular pattern yet. So some may feel surprised to use workflow as storage. In fact it’s already a popular pattern in Temporal. 
* Temporal Cloud doesn’t support “ORDER BY” in the cloud service anymore…They disable this by default and we have to request manually. 
  * Temporal don’t want us to use “ORDER BY” anymore because it could cause high latency to ElasticSearch. So this would be a big cons of using Dex search attribute for flexibility 


Also compared Dex with Temporal directly for this :
 
There is **NO** history scaling limit because of Dex’s auto continueAsNew (Temporal Java SDK will require a[ continueAsNew implementation](https://legacy-documentation-sdks.temporal.io/java/how-to-continue-as-new-in-java))

To summarize, it's not a "wrong" or "bad" idea to use Dex as permanent storage, as long as the cost are well considered and acceptable. (e.g. if the amount/size is small to worry about, or just for MVP). And we already have production use case doing that – by using Dex to store email templates.
