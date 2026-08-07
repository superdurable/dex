# User signup

A new user submits a signup form, the system emails a verification link, and the
Flow keeps sending reminders on a timer until the user clicks it.

`Submit` persists the form and moves to `Verify`, which waits for either the
`Verify` Channel message or the reminder timer. Receiving the message sends the
welcome email and completes the Flow; the timer sends another reminder and loops
back into the same Step.

The `verify` RPC is what the verification link calls: it flips the status
Attribute and publishes to the Channel in one atomic operation, so clicking the
link twice returns `already verified` instead of sending a second welcome email.

Note that the Channel is exposed as `verify_channel` because the Flow already
has a `verify` RPC method; its durable name is still `Verify`.

With the sample server running:

```text
http://localhost:8080/signup/submit?username=test1&email=abc@c.com
http://localhost:8080/signup/verify?username=test1
```
