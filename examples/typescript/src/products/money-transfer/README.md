# Money transfer saga

The Flow checks the source balance, creates debit and credit memos, and moves funds. Each external operation has an Execute retry policy and proceeds to the compensation Step after exhausting retries.

With the sample server running:

```text
http://localhost:8080/products/money-transfer/start?fromAccount=test1&toAccount=test2&amount=100&notes=hello
```
