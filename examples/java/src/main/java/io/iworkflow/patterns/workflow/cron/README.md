# Cron Schedule Workflow Implementation

This package demonstrates a how to use a Cron Scheduled workflow to replace Orc jobs.

## Key Components

1. **Config**: (PatternWorkflowsConfig) Initializes the cron scheduled workflow as an IwfStartupAction and sets the scheduled
   time.
2. **CronScheduleWorkflow**: The workflow that the schedule will call upon triggering.
3. **CronScheduleState**: The state used within the workflow.

## Useful Links

- [Workflow Options](https://github.com/superdurable/iwf/wiki/WorkflowOptions#cron-schedule)
- [CRON Expression Format](https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format)
- [CRON Expression Tester](https://crontab.guru/)

## Implementation Details

- The IwfWorkflowRegistrationProcessor  will invoke all IwfStartupActions after the
  workflows are registered.
- The config specifying your IwfStartupAction will initialize and start the CRON workflow using the schedule specs defined.
- Each time schedule is triggered, the workflow will be invoked.

## Schedule Management

The CRON schedule workflow can be managed in the [Temporal Cloud UI](https://cloud.temporal.io/welcome).

- View triggered workflow executions
    - In the cloud UI, select **Workflows** from the left navigation bar.
    - Filter by the workflow ID (e.g. `cron-schedule-workflow`)
- Pause, Trigger, or Delete the schedule
    - In the cloud UI, select **Schedules** from the left navigation bar.
    - Find and select your scheduled workflow.
    - The top right of the page will have an action button that allows for Pausing. Clicking the arrow button will give more
      options such as manually Triggering or Deleting the schedule.
        - *Pause*: Pausing the schedule will stop the workflow from being triggered, ignoring the schedule until it is
          resumed.
        - *Trigger*: Manually triggers the workflow to run.
        - *Delete*: Deletes the schedule.
            - NOTE: The schedule will be re-created during the next code deployment if the logic in the code is still
              present. Remove the IwfStartupAction that creates the schedule and follow the instructions listed for updating
              the schedule.
    - Navigate to the workflow config file that is defining the IwfStartupAction containing the CRON schedule.
    - Update the schedule specs, workflow, or states as needed.
    - Commit the changes and deploy the code.
    - Delete the schedule in the Temporal UI (see section above).
    - Restart your deployed instance (e.g. Marvin)
    - After the instance has fully stood back up, verify the new schedule in the Temporal UI.

## Current Limitations

- Currently, the [Schedule Overlap Policy](https://python.temporal.io/temporalio.client.ScheduleOverlapPolicy.html) is not
  exposed and is defaulted to `Skip`.

## Usage Example

- The CRON scheduled workflow should support most ORC use cases, unless the job is supposed to utilize local GPU/memory/disk,
  etc.
