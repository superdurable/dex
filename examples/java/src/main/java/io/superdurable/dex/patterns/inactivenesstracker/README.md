# Inactiveness Tracker Timer

- `GET /patterns/inactiveness-tracker-timer/start?workflowId={workflowId}`
- `GET /patterns/inactiveness-tracker-timer/activity?workflowId={workflowId}`

Activity loops through `TrackerStep` and resets the timer. Timer expiry moves to `ProcessInactivenessStep`.
