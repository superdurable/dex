### Command to build this `my_service_mock.go`

Run this at the root of the project:
```shell
mockgen -source=workflows/service/my_service.go -package=service -destination=workflows/service/my_service_mock.go
```