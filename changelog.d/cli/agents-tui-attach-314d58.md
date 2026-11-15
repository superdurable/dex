---
section: Fixed
---
- **dexcli agents-tui**: Enter right after pasting an image no longer sends the message without it and attaches the image to the next message. Enter waits until the draft's pasted images are stored, shows "attaching image… sends when ready", and then sends one message with them. If an image does not attach, the message stays unsent with a notice, and the next Enter sends it without the image.
- **dexcli agents-tui**: live output turns off only when the Dex server's Stream Store is disabled. Before, any failed precondition on reading the session's events turned it off for the rest of the chat; now such a failure is retried.
