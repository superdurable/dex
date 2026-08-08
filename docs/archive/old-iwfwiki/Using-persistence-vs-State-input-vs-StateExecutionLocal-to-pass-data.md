<!---
---

---
--->

<!---## DOCHUB-PATH: advanced-concepts/persistence-v-input-v-local.mdx :DOCHUB-PATH ##--->

<!---
# Using persistence vs State input vs StateExecutionLocal to pass data
--->

Some tips for making decisions:

* If using OptimizeActivity, using data/search attributes will save Temporal storage cost(because the payload won't be shown in activity input). 
* Input can be used as thread local data, but data attributes is shared across threads.
* If there will be only one state needs the value, then it’s easier to use state input so that it save code to declare data attributes
  * if it’s more than one, you probably want to use data or search attributes to save the code of passing values around 
* By default , all state api / RPC will load all data attributes unless you set a different persistence loading policy. So Having too many data attributes could be wasteful for communication for default loading, and could exceed the 2MB limit . Otherwise you will have to use PartialLoading policy to selectively load data into states. 
* StateExecutionLocal is only for passing data from the waitUntil to execute API, within a stateExecutionLocal. This is just useful for this use case. It can be replaced by data attributes. It's like using data attributes without declaring it, and the value will be removed after state is completed.


Basically if you can use input and it’s easier, then use input. Or if you can use stateExecutionLocal, then use it without hesitation. Or none of them works well, then use persistence. 