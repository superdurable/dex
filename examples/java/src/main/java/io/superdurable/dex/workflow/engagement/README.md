# Use case 
<img width="681" alt="Screenshot 2023-04-21 at 8 54 44 AM" src="https://user-images.githubusercontent.com/4523955/233681131-c87ebecd-4a75-4d69-a701-8abdcc02f81b.png">


* An engagement is initiated by an employer to reach out to a jobSeeker(via email/SMS/etc)
* The jobSeeker could respond with decline or accept
* If jobSeeker doesn't respond, it will get reminder
* An engagement can change from declined to accepted, but cannot change from accepted to declined

# API requirements

* Start an engagement
* Describe an engagement
* Opt-out email reminder for an engagement
* Decline engagement 
* Accept engagement
* Notify external systems about the engagement changes, with eventual consistency guarantee
* List engagements in different/any patterns (which would require a lot of indexes if using traditional DB)
  * By employerId, status order by updateTime
  * By jobSeekerId, status order by updateTime
  * By employerId + jobSeekerId
  * By status, order by updateTime

# Design

<img width="727" alt="Screenshot 2023-04-21 at 8 58 50 AM" src="https://user-images.githubusercontent.com/4523955/233681933-d881105f-a169-4c38-8063-1e08bd9ac897.png">

# Implementation Details 

## InitState
<img width="1442" alt="Screenshot 2023-04-21 at 9 02 18 AM" src="https://user-images.githubusercontent.com/4523955/233682961-e46607a1-dc21-4199-80f1-91dc7262ca83.png">

## ReminderState

<img width="1437" alt="Screenshot 2023-04-21 at 8 59 56 AM" src="https://user-images.githubusercontent.com/4523955/233682134-598b449a-19d1-4176-aa37-4efe74753cc3.png">

## RPC
<img width="1438" alt="Screenshot 2023-04-21 at 9 02 01 AM" src="https://user-images.githubusercontent.com/4523955/233682996-5cec2a81-5092-4755-9ed7-f78c12bf128f.png">

## NotifyExtState

<img width="1423" alt="Screenshot 2023-04-21 at 9 02 11 AM" src="https://user-images.githubusercontent.com/4523955/233682984-1fafe0cc-5229-4c0f-8cf2-21f6ff59d1dc.png">

## Controller
And controller is a very thin layer of calling Dex client APIs and workflow RPC stub APIs. See [EngagementController](https://github.com/superdurable/dex/blob/main/examples/java/src/main/java/io/dex/controller/EngagementWorkflowController.java).

# How to run

First of all, you need to register the required Search attributes 
## Search attribute requirement

If using Temporal:

* New CLI(Temporal)
```bash
temporal search-attribute create -name EmployerId -type Keyword -y
temporal search-attribute create -name JobSeekerId -type Keyword -y
temporal search-attribute create -name EngagementStatus -type Keyword -y
temporal search-attribute create -name LastUpdateTimeMillis -type Int -y
```

* Old CLI(tctl)
``` bash
tctl adm cl asa -n EmployerId -t Keyword
tctl adm cl asa -n JobSeekerId -t Keyword
tctl adm cl asa -n EngagementStatus -t Keyword
tctl adm cl asa -n LastUpdateTimeMillis -t Int

```

If using Cadence

```bash
cadence adm cl asa --search_attr_key EmployerId --search_attr_type 1
cadence adm cl asa --search_attr_key JobSeekerId --search_attr_type 1
cadence adm cl asa --search_attr_key Status --search_attr_type 1
cadence adm cl asa --search_attr_key LastUpdateTimeMillis --search_attr_type 2
```

## How to test the APIs in browser

* start API: http://localhost:8803/engagement/start
  * It will return the workflowId which can be used in subsequence API calls. 
* describe API: http://localhost:8803/engagement/describe?workflowId=<TODO>
* opt-out email API: http://localhost:8803/engagement/optout?workflowId=<TODO>
* decline API: http://localhost:8803/engagement/decline?workflowId=<TODO>&notes=%22not%20interested%22
* accept API: http://localhost:8803/engagement/accept?workflowId=<TODO>&notes=%27accept%27
* search API, use queries like:
  * ['EmployerId="test-employer" ORDER BY LastUpdateTimeMillis '](http://localhost:8803/engagement/list?query=<TODO>)
  * ['EmployerId="test-employer"'](http://localhost:8803/engagement/list?query=<TODO>)
  * ['EmployerId="test-employer" AND EngagementStatus="INITIATED"'](http://localhost:8803/engagement/list?query=<TODO>)
  * etc
