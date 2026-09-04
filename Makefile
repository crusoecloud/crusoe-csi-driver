PREFIX?=$(shell pwd)

CSI_DRIVER_NAME := crusoe-csi-driver
CSI_DRIVER_PKG := github.com/crusoecloud/crusoe-csi-driver/cmd/$(CSI_DRIVER_NAME)

BUILDDIR := ${PREFIX}/dist
# Set any default go build tags
BUILDTAGS :=

GOLANGCI_VERSION = v1.62.0
GO_ACC_VERSION = v0.2.8
GOTESTSUM_VERSION = v1.13.0
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
FUNCTEST_VERSION ?= v0.0.454

# Different repos hold the Slack credentials under different names: region-coordinator uses these
# names directly, kubernetes-manager maps them from FUNCTEST_STAGING_SLACK_TOKEN and
# K8s_ALERT_STAGING_CHANNEL_ID. Resolve here rather than in the job's script, because after_script
# runs in a fresh shell: an export in script never reaches functest-ci-cleanup, so the claim would
# be taken and never released wherever only the FUNCTEST_STAGING_* names are set.
# Tested for emptiness rather than with ?=, which only fills an undefined variable. CI can define a
# variable as empty, and an empty value has to fall back too.
ifeq ($(strip $(SLACK_TOKEN)),)
SLACK_TOKEN := $(FUNCTEST_STAGING_SLACK_TOKEN)
endif
ifeq ($(strip $(SLACK_STAGING_ALERTS_CHANNEL)),)
SLACK_STAGING_ALERTS_CHANNEL := $(K8s_ALERT_STAGING_CHANNEL_ID)
endif
export SLACK_TOKEN
export SLACK_STAGING_ALERTS_CHANNEL

# Report every missing CI variable at once, before doing any work.
#
# The suite reads these from the environment and fails on the first one it happens to need, so a
# missing set is discovered one 30-second job at a time. This lists all of them in a single run.
#
# What each is for, and why none can be dropped:
#
#   CI_PROJECT_ID_CRUSOE_TEST_ACC   the Crusoe project the tests create clusters and disks in
#   CI_{ACCESS,SECRET}_KEY_...      that project's API credentials
#   CI_{PUB,PRIV}_SSH_KEY_...       injected into test VMs so the suite can SSH in. The CSI tests
#                                   never SSH anywhere, but internal/configs/setFromCIVars errors
#                                   when either is empty, and parses the private key at startup, so
#                                   the suite will not boot without them. The private key is
#                                   base64-encoded (StdEncoding).
#   SLACK_TOKEN                     posts the staging claim. resource_group only serialises within
#   SLACK_STAGING_ALERTS_CHANNEL    one GitLab project, so the Slack claim is the only mutex that
#                                   stops this run colliding with region-coordinator's.
#   CSI_IMAGE                       the driver build under test, derived by the job
#
# Values live in 1Password under cloud-compute-devs+region-testing@crusoeenergy.com, per the comment
# on internal/configs/setFromCIVars in the testing repo. They are set by hand per GitLab project,
# not group-inherited and not in Terraform, which is why a new consumer starts with none of them.
FUNCTEST_REQUIRED_VARS = \
	CI_API_ENDPOINT_ENV \
	CI_PROJECT_ID_CRUSOE_TEST_ACC \
	CI_ACCESS_KEY_CRUSOE_TEST_ACC \
	CI_SECRET_KEY_CRUSOE_TEST_ACC \
	CI_PUB_SSH_KEY_CRUSOE_TEST_ACC \
	CI_PRIV_SSH_KEY_CRUSOE_TEST_ACC \
	SLACK_TOKEN \
	SLACK_STAGING_ALERTS_CHANNEL \
	CSI_IMAGE

.PHONY: functest-preflight
functest-preflight: ## Fail with the full list of CI variables this job still needs
	@echo "==> $@"
	@missing=""; \
	for v in $(FUNCTEST_REQUIRED_VARS); do \
		eval "val=\$$$$v"; \
		if [ -z "$$val" ]; then \
			missing="$$missing $$v"; \
			echo "  MISSING  $$v"; \
		else \
			echo "  set      $$v"; \
		fi; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "" >&2; \
		echo "FAIL: these CI variables are empty or unset:$$missing" >&2; \
		echo "      Set them in this project's CI settings; they are not group-inherited." >&2; \
		echo "      Values: 1Password, cloud-compute-devs+region-testing@crusoeenergy.com." >&2; \
		echo "      The private SSH key must be base64-encoded. See CRUSOE-95943." >&2; \
		exit 1; \
	fi; \
	echo "all required variables are present"

.PHONY: functest-ci
functest-ci: ## Runs the CSI storage tests from the testing repo against this build of the driver
	@echo "==> $@"
	@go install gotest.tools/gotestsum@${GOTESTSUM_VERSION}
	@go get gitlab.com/crusoeenergy/island/testing/functionality/utils@${FUNCTEST_VERSION}
	@git clone --branch ${FUNCTEST_VERSION} --single-branch https://gitlab.com/crusoeenergy/island/testing.git && \
		go run testing/functionality/cmd/slack_claim/main.go -service="crusoe-csi-driver" -timestampfile="functest_slack_timestamp" && \
		cd testing/functionality/v1alpha5 && \
		gotestsum --format standard-verbose --junitfile $(CURDIR)/functests.xml -- -json -race -v -timeout 50m \
		-cluster-version $(FUNCTEST_CLUSTER_VERSION) -cmk-cluster-configuration=standard \
		-csi-image=$(CSI_IMAGE) \
		-fail-on-skipped-csi-fs \
		-run 'TestKubernetesSuite' -csi-tests=ssd,fs $(EXTRA_FUNCTEST_FLAGS) && \
		cd ../../..

.PHONY: functest-ci-cleanup
functest-ci-cleanup: ## Releases the staging claim, whether or not the functests passed
	@echo "==> $@"
# Runs from after_script, so it also runs when setup failed before the claim was taken. Without these
# guards it fails on a missing timestamp file and buries the real error under its own.
	@if [ ! -f functest_slack_timestamp ]; then \
		echo "no staging claim was taken, nothing to release"; \
		exit 0; \
	fi; \
	if [ ! -f testing/functionality/cmd/slack_unclaim/main.go ]; then \
		echo "testing repo was never cloned, cannot release the claim; check the job log above" >&2; \
		exit 0; \
	fi; \
	go run testing/functionality/cmd/slack_unclaim/main.go -service="crusoe-csi-driver" \
		-timestamp="$$(cat functest_slack_timestamp)"

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
