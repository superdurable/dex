---
section: Fixed
---
- **dexcli version check** now checks the server against the protocol range of the embedded Go SDK, which every other dexcli command uses. It reports that range as **clientProtocol** and the protocol those commands negotiate as **negotiatedProtocol**. Earlier releases checked a fixed range of 1-1, so the command could report a compatible server that the other commands reject. When the ranges do not overlap, the **reason** says whether to upgrade the server or dexcli. **dexcli web** warns with the same check.
