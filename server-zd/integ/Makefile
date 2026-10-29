.PHONY: zdIntegTests compile check-scripts

dexServerZDBinary ?= ../target/debug/dex-server-zd
totalPartitions ?= 1
partitionNum ?= 0
zdIntegTestJSON ?= /tmp/test-zd-integ-$(partitionNum).json

zdIntegTests:
	./script/run-zd-integ-tests.sh $(abspath $(dexServerZDBinary)) $(totalPartitions) $(partitionNum) $(abspath $(zdIntegTestJSON)) $(if $(testAllowlist),$(abspath $(testAllowlist)))

compile:
	GOWORK=off go test -run '^$$' ./...
	GOWORK=off go test -tags=attributestore_integration -run '^$$' ./...

check-scripts:
	PYTHONDONTWRITEBYTECODE=1 python3 script/summarize_integ_test_json_test.py
