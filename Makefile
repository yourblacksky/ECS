GOMOBILE_VERSION ?= v0.0.0-20260803200217-62cee1672c8e
MOBILE_GO_TOOLCHAIN ?= go1.25.12
GOVULNCHECK_VERSION ?= v1.6.0
MOBILE_ANDROID_TARGETS ?= android/arm,android/arm64,android/amd64
MOBILE_IOS_TARGETS ?= ios,iossimulator
GOMOBILE := $(shell go env GOPATH)/bin/gomobile

.PHONY: build build-android mobile-tools mobile-aar mobile-xcframework ios-project android-ui-test test vulncheck run mock-llm demo-meeting demo-agentteams-office eval-qwen17-live eval-qwen06-live demo-network demo-network-down verify-three-node-meeting sync-skill-docs verify-skill-docs verify-version verify-config-reference verify-third-party verify-agentteams-integration verify-delivery-archive verify-harness

build:
	go build -trimpath -o bin/ecs ./cmd/ecs

build-android:
	mkdir -p bin/android-arm64
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -trimpath -buildmode=pie \
		-ldflags='-s -w' -o bin/android-arm64/ecs ./cmd/ecs

mobile-tools:
	GOTOOLCHAIN=$(MOBILE_GO_TOOLCHAIN) go install golang.org/x/mobile/cmd/gomobile@$(GOMOBILE_VERSION)
	GOTOOLCHAIN=$(MOBILE_GO_TOOLCHAIN) go install golang.org/x/mobile/cmd/gobind@$(GOMOBILE_VERSION)

mobile-aar: mobile-tools
	GOTOOLCHAIN=$(MOBILE_GO_TOOLCHAIN) MOBILE_ANDROID_TARGETS=$(MOBILE_ANDROID_TARGETS) ./scripts/build-mobile-aar.sh

mobile-xcframework: mobile-tools
	GOTOOLCHAIN=$(MOBILE_GO_TOOLCHAIN) MOBILE_IOS_TARGETS=$(MOBILE_IOS_TARGETS) ./scripts/build-mobile-xcframework.sh

ios-project:
	./scripts/generate-ios-project.sh

android-ui-test:
	./scripts/test-android-ui.sh

test:
	go test ./...
	go vet ./...
	$(MAKE) verify-skill-docs
	$(MAKE) verify-version
	$(MAKE) verify-config-reference
	$(MAKE) verify-third-party
	$(MAKE) verify-harness
	python3 scripts/verify-agents-md.py
	./scripts/verify-delivery-source-archive.sh

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

run:
	go run ./cmd/ecs -config config.json

mock-llm:
	go run ./cmd/ecs-mock-llm

demo-meeting:
	./scripts/demo-meeting-followup.sh

demo-agentteams-office:
	./scripts/demo-agentteams-office-e2e.sh

eval-qwen17-live:
	./scripts/eval-qwen17-live.sh

eval-qwen06-live:
	./scripts/eval-qwen06-live.sh

demo-network:
	./scripts/demo-three-node.sh

demo-network-down:
	./scripts/stop-three-node-demo.sh

verify-three-node-meeting:
	./scripts/verify-three-node-meeting-local.sh

sync-skill-docs:
	go run ./scripts/sync-skill-doc-contracts.go

verify-skill-docs:
	go run ./scripts/sync-skill-doc-contracts.go -check

verify-version:
	./scripts/verify-version.sh

verify-config-reference:
	go run ./scripts/config-reference

verify-third-party:
	./scripts/verify-third-party-notices.sh

verify-agentteams-integration:
	./scripts/verify-agentteams-integration.sh

verify-delivery-archive:
	./scripts/verify-delivery-source-archive.sh

verify-harness:
	python3 scripts/verify-harness.py
	python3 scripts/test-harness.py

verify-harness-e2e:
	./scripts/test-codex-hook-e2e.sh
