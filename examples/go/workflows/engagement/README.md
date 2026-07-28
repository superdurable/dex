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
![Screenshot 2023-05-23 at 4 19 07 PM](https://github.com/superdurable/iwf/assets/4523955/1104f0d6-1933-4842-b22b-0b31cb055092)

## ReminderState

![Screenshot 2023-05-23 at 4 19 18 PM](https://github.com/superdurable/iwf/assets/4523955/2cdfc832-36ff-49d0-addf-d9101108aeb9)

## RPC

![Screenshot 2023-05-23 at 4 19 28 PM](https://github.com/superdurable/iwf/assets/4523955/b498439c-c79a-40ee-9d56-f0961727865d)

## NotifyExtState
![Screenshot 2023-05-23 at 4 19 46 PM](https://github.com/superdurable/iwf/assets/4523955/e7e52e94-b383-4565-a1d2-d50b9c184745)


## Controller
And controller is a very thin layer of calling iWF client APIs and workflow RPC stub APIs. See [engagement_controller](../../cmd/server/iwf/engagement_controller.go).

# How to run

First of all, you need to register the required Search attributes 
## Search attribute requirement

If using Temporal:

* New CLI
```bash
tctl search-attribute create -name EmployerId -type Keyword -y
tctl search-attribute create -name JobSeekerId -type Keyword -y
tctl search-attribute create -name EngagementStatus -type Keyword -y
tctl search-attribute create -name LastUpdateTimeMillis -type Int -y
```

* Old CLI
``` bash
tctl adm cl asa -n EmployerId -t Keyword
tctl adm cl asa -n JobSeekerId -t Keyword
tctl adm cl asa -n Status -t Keyword
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
  * ['EmployerId="test-employer-id" ORDER BY LastUpdateTimeMillis '](http://localhost:8803/engagement/list?query=<TODO>)
  * ['EmployerId="test-employer-id"'](http://localhost:8803/engagement/list?query=<TODO>)
  * ['EmployerId="test-employer-id" AND EngagementStatus="Initiated"'](http://localhost:8803/engagement/list?query=<TODO>)
  * etc
