# Money transfer saga

The Flow checks the source balance, creates debit and credit memos, and moves
funds. Each external operation has an Execute retry policy and proceeds to the
`Compensate` Step after exhausting retries.

Steps are plain Python objects wired together in `MoneyTransferFlow.__init__`.
They are constructed back to front so every Step can hold a typed reference to
the one that follows it.

With the sample server running:

```text
http://localhost:8080/moneytransfer/start?fromAccount=test1&toAccount=test2&amount=100&notes=hello
```
