# Old-skool build tools.

.DEFAULT_GOAL := help

TAG ?= openshift/archivist
DOCKERFILE := Dockerfile

# Builds and installs the archivist binary.
build: check-gopath
	go build -i -o $(GOPATH)/bin/archivist
.PHONY: build


# Runs the integration tests.
#
# Args:
#   TESTFLAGS: Flags to pass to `go test`. The `-v` argument is always passed.
#
# Examples:
#   make test TESTFLAGS="-run TestSomething"
test: build
	go test -v $(TESTFLAGS) \
		github.com/openshift/online-archivist/pkg/...
.PHONY: test

# Precompile everything required for development/test.
test-prepare: build
	go build -i github.com/openshift/online-archivist/test/...
.PHONY: test-prepare

# Build a release image. The resulting image can be used with test-release.
#
# Args:
#   TAG: Docker image tag to apply to the built image. If not specified, the
#     default tag "openshift/archivist" will be used.
#
# Example:
#   make release TAG="my/archivist"
release:
	docker build --rm -f $(DOCKERFILE) -t $(TAG) .
.PHONY: release


# Tests a release image.
#
# Prerequisites:
#   A release image must be built and tagged (make release)
#
# Examples:
#
#   make test-release
#   make test-release TAG="my/archivist"
test-release:
	docker run --rm -ti \
		$(DOCKERFLAGS) \
		--entrypoint make \
		$(TAG) \
		test
.PHONY: test-release


# Verifies that source passes standard checks.
verify:
	$(GOPATH)/src/github.com/openshift/online/hack/verify-source.sh
	go vet \
		github.com/openshift/online-archivist/cmd/... \
		github.com/openshift/online-archivist/pkg/...
.PHONY: verify


# Prints a list of useful targets.
help:
	@echo ""
	@echo "OpenShift Online Archivist Controller"
	@echo ""
	@echo "make build                compile binaries"
	@echo "make release              build release image using Dockerfile"
	@echo "make test-release         run unit and integration tests in Docker container"
	@echo "make verify               lint source code"
	@echo ""
.PHONY: help

# Checks if a GOPATH is set, or emits an error message
check-gopath:
ifndef GOPATH
	$(error GOPATH is not set)
endif
.PHONY: check-gopath
