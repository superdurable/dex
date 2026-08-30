# User onboarding process

Submit starts onboarding. The user verifies their email, accomplishes task 1,
and then accomplishes task 2. Every waiting stage has a durable reminder Timer.

With the sample server running:

```text
http://localhost:8080/products/signup/submit?username=<user>&email=<email>
http://localhost:8080/products/signup/verify?username=<user>
http://localhost:8080/products/signup/accomplish-task-1?username=<user>
http://localhost:8080/products/signup/accomplish-task-2?username=<user>
```
