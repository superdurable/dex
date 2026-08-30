# User onboarding process

A new user submits a signup form, verifies their email, accomplishes task 1,
and then accomplishes task 2. Every waiting stage has a durable reminder Timer.

With the sample server running:

```text
http://localhost:8080/products/signup/submit?username=test1&email=abc@c.com
http://localhost:8080/products/signup/verify?username=test1
http://localhost:8080/products/signup/accomplish-task-1?username=test1
http://localhost:8080/products/signup/accomplish-task-2?username=test1
```
