PREFIX?=$(shell pwd)

CSI_DRIVER_NAME := crusoe-csi-driver
CSI_DRIVER_PKG := github.com/crusoecloud/crusoe-csi-driver/cmd/$(CSI_DRIVER_NAME)

BUILDDIR := ${PREFIX}/dist
# Set any default go build tags
BUILDTAGS :=

GOLANGCI_VERSION = v1.62.0
GO_ACC_VERSION = v0.2.8
GOTESTSUM_VERSION = v1.12
# Pin gocover-cobertura: v1.5.0 requires Go >= 1.25 and breaks the go-ci-1.24
# image (test-ci fails at `go install ...@latest`). v1.4.0 needs only Go 1.22.
GOCOVER_VERSION = v1.4.0

export CRUSOE_CSI_DRIVER_VERSION?=$(shell git describe --always --tags --dirty)
GO_LDFLAGS=-ldflags "-X github.com/crusoecloud/crusoe-csi-driver/internal/common.PluginVersion=$$CRUSOE_CSI_DRIVER_VERSION"

.PHONY: run
run:
	go run ${GO_LDFLAGS} cmd/crusoe-csi-driver/main.go

.PHONY: dev
dev: test build-deps lint ## Runs a build-deps, test, lint

.PHONY: ci
ci: test-ci build-deps lint-ci ## Runs test, build-deps, lint

.PHONY: build-deps
build-deps: ## Install build dependencies
	@echo "==> $@"
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_VERSION}

.PHONY: precommit
precommit: ## runs various formatters that will be checked by linter (but can/should be automatic in your editor)
	@echo "==> $@"
	@go mod tidy
	@golangci-lint run --fix ./...

.PHONY: test
test: ## Runs the go tests.
	@echo "==> $@"
	@go test -tags "$(BUILDTAGS)" -cover -race -v ./...

.PHONY: test-ci
test-ci: ## Runs the go tests with additional options for a CI environment
	@echo "==> $@"
	@go mod tidy
	@git diff --exit-code go.mod go.sum # fail if go.mod is not tidy
	@go install github.com/ory/go-acc@${GO_ACC_VERSION}
	@go install gotest.tools/gotestsum@${GOTESTSUM_VERSION}
	@go install github.com/boumenot/gocover-cobertura@${GOCOVER_VERSION}
	@gotestsum --junitfile tests.xml --raw-command -- go-acc -o coverage.out ./... -- -json -tags "$(BUILDTAGS)" -race -v
	@go tool cover -func=coverage.out
	@gocover-cobertura < coverage.out > coverage.xml

.PHONY: lint
lint: ## Verifies `golangci-lint` passes
	@echo "==> $@"
	@golangci-lint version
	@golangci-lint run ./... --timeout=10m

.PHONY: lint-ci
lint-ci: ## Verifies `golangci-lint` passes and outputs in CI-friendly format
	@echo "==> $@"
	@golangci-lint version
	@golangci-lint run ./... --timeout=10m --out-format code-climate > golangci-lint.json

# Pin the testing repo tag: changes there can affect how these tests should be run here. Same
# consumption pattern as region-coordinator, kubernetes-manager, and storms. Overridable so a run can
# point at a testing branch before that work is tagged.
FUNCTEST_VERSION ?= v0.0.414
GOTESTSUM_VERSION ?= v1.13.0

.PHONY: functest-ci
functest-ci: ## Runs the CSI storage tests from the testing repo against this build of the driver
	@echo "==> $@"
	@go install gotest.tools/gotestsum@${GOTESTSUM_VERSION}
	@go get gitlab.com/crusoeenergy/island/testing/functionality/utils@${FUNCTEST_VERSION}
	@git clone --branch ${FUNCTEST_VERSION} --single-branch https://gitlab.com/crusoeenergy/island/testing.git && \
		go run testing/functionality/cmd/slack_claim/main.go -service="crusoe-csi-driver" -timestampfile="functest_slack_timestamp" && \
		cd testing/functionality/v1alpha5 && \
		gotestsum --format standard-verbose --junitfile $(CURDIR)/functests.xml -- -json -race -v -timeout 60m \
		-cluster-version $(FUNCTEST_CLUSTER_VERSION) -cmk-cluster-configuration=standard \
		-csi-image=$(CSI_IMAGE) \
		-fail-on-skipped-csi-fs \
		-run 'TestKubernetesSuite' -csi-tests=ssd,fs $(EXTRA_FUNCTEST_FLAGS) && \
		cd ../../..

.PHONY: functest-ci-cleanup
functest-ci-cleanup: ## Releases the staging claim, whether or not the functests passed
	@go run testing/functionality/cmd/slack_unclaim/main.go -service="crusoe-csi-driver" -timestamp=$(shell cat functest_slack_timestamp)

.PHONY: build
build: ## Builds the executable and places it in the build dir
	@go build -o ${BUILDDIR}/${NAME} ${CSI_DRIVER_PKG}

# FIXME: https://crusoe.atlassian.net/browse/CRUSOE-35425
.PHONY: cross
cross: ## Builds the cross compiled executable for use within a container
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ${BUILDDIR}/${CSI_DRIVER_NAME} ${GO_LDFLAGS} ${CSI_DRIVER_PKG}

.PHONY: install
install: ## Builds and installs the executable on PATH
	@go install ${CSI_DRIVER_PKG}

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
