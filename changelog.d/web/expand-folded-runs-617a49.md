---
section: Changed
---
- In **Graph** with **Folded** on, the **×N** badge of a node that folds consecutive executions of one Step type is a button. It opens the run in place, with each execution as its own node inside a frame. A long run shows its first, failed, selected, and last executions, with **… N more** between them. **Hide** on the frame folds it again. The run stays open as new events arrive, and selecting an event inside it opens it too.
- A **×N** node now folds only executions whose Execute returned the same Step decision, such as **GoTo** or **GracefulComplete**. A decision that also cancels Steps counts as a different decision. For example, eight **SyncBatch** executions that decide **GoTo** followed by one that decides **GracefulComplete** show as **SyncBatch ×8** and a separate **SyncBatch**. Rounds whose Steps decide differently no longer merge into one range.
