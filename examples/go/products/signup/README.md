# Signup

User signup with email verification reminders. Submit starts the flow; verify completes it via RPC.

With the sample server running:

```text
http://localhost:8080/signup/submit?username=<user>&email=<email>
http://localhost:8080/signup/verify?username=<user>
```
