# RPC locking and atomicity

The page is to answer the two questions:

* Does this mean that all reads/writes in an RPC method are done atomically (workflow-level lock), so that we can ensure no race condition even if multiple calls to the RPC method are made simultaneously? 
* Does this mean an RPC call will always block other RPC calls in the same workflow until it is finished? Or blocking happens only when the lock is needed (like RPC method writes persistence fields)


There are three levels of atomicity in Dex RPC 

* Level 1: Atomicity as the whole read+write
* Level 2: Atomicity of reading the results as a snapshot with strong read after write consistency, and atomicity of writing (updating persistence, publishing messages, trigger state movements etc). 
* Level 3: Same as above, but atomically reading the snapshot with eventual consistency – meaning that the read results could be stale for a few hundred milliseconds. 


Level 3 happens when you enable caching in RPC, without setting the bypassCachingForStrongConsistency 

Level 2 is the most common case, but means there could be a racing condition of updating the values for the same workflow. Though both read and write are atomic for each, the whole read+write is NOT. 

Level 1 requires to use `PARTIAL_WITH_EXCLUSIVE_LOCK` as persistence loading policy for the RPC, which is only supported for Temporal as backend.

For Cadence as backend, if this racing condition is a problem, the [workaround](Persistence.md#persistence-loading-policy) today is to move the write part to a state execution with persistence locking, and let RPC trigger a state movement to do that.

For example, if a workflow want to count some ID from an RPC, like this:

```java
// note that this is only supported by Temporal as backend, with enabling frontend.enableUpdateWorkflowExecution feature
@RPC( persistenceLoadingPolicy = PARTIAL_WITH_EXCLUSIVE_LOCK, 
      lockingKeys = [DA_KEY_COUNT, DA_KEY_HOLDER])  
public void countKeys(Context context, String key, Persistence persistence, Communication communication) {
  KeyHolder keys = persistence.getDataAttribute(DA_KEY_HOLDER KeyHolder.class);
  Integer count = persistence.getDataAttribute(DA_KEY_COUNT, Integer.class);
  if(!keys.contain(key)){
    count ++;
    persistence.setDataAttribute(DA_KEY_COUNT, count);
    keys.add(key);
    persistence.setDataAttribute(DA_KEY_HOLDER, keys);
  }
}
```

For Cadence as backend, you should do this instead because of missing level1 support 

```java
@RPC
public void countKeys(Context context, String key, Persistence persistence, Communication communication) {
  KeyHolder keys = persistence.getDataAttribute(DA_KEY_HOLDER, KeyHolder.class);
  Integer count = persistence.getDataAttribute(DA_KEY_COUNT, Integer.class);
  if (!keys.contain(key)) {
    communication.triggerStateMovements(
    StateMovement.create(CountState.class, key)
  );
}
}
 
 
class CountState implements WorkflowState<String> {
 
@Override
public Class<String> getInputType() {
  return String.class;
}
 
@Override
public WorkflowStateOptions getStateOptions(){
  return new WorkflowStateOptions()
    .dataAttributesLoadingPolicy(
      new PersistenceLoadingPolicy()
      .persistenceLoadingType(PersistenceLoadingType.PARTIAL_WITH_EXCLUSIVE_LOCK)
      .partialLoadingKeys(Arrays.asList(...other keys that don't need locking...))
      .lockingKeys(Arrays.asList(DA_KEY_HOLDER, DA_KEY_COUNT))
    );
}
 
@Override
public StateDecision execute(final Context context, final String key, final CommandResults commandResults, Persistence persistence, final Communication communication) {
  KeyHolder keys = persistence.getDataAttribute(DA_KEY_HOLDER, KeyHolder.class);
  Integer count = persistence.getDataAttribute(DA_KEY_COUNT, Integer.class);
  if(!keys.contain(key)){
    count ++;
    persistence.setDataAttribute(DA_KEY_COUNT, count);
    keys.add(key);
    persistence.setDataAttribute(DA_KEY_HOLDER, keys);
  }
  return StateDecision.deadEnd();
}
}
```
