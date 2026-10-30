# Go JSON codec fixture

`go_result.json` is the output of the standard library `encoding/json` encoder
in `generate.go`. Its `time.Time` value uses Go's RFC3339Nano JSON representation.
Regenerate from the repository root:

```bash
GOWORK=off go run sdk-python/tests/fixtures/json_codec/generate.go > sdk-python/tests/fixtures/json_codec/go_result.json
```

The Python test decodes this payload into a generic dataclass with nested values,
optional fields, camelCase wire names, and a datetime. Re-encoding must preserve
the JSON values, including the UTC timestamp spelling, regardless of key order.
