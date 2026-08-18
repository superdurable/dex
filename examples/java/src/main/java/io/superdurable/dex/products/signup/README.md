# Signup

User signup with email verification reminders. Submit starts the Flow; verify
completes it via RPC.

With the sample server running:

```text
http://localhost:8080/products/signup/submit?username=test1&email=abc@c.com
http://localhost:8080/products/signup/verify?username=test1
```
