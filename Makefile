# Targets mirror the merge gate in the constitution's Development Workflow:
# go build, go vet, go test ./..., and a successful Docker image build.
#
# There is deliberately no `run` target. Constitution Principle IV (2.0.0) has
# no supported local runtime environment: running software is tested in a
# deployed Google Cloud environment, because a workstation stack terminates no
# TLS, runs no reverse proxy and answers a different OAuth callback, and a
# lower environment that quietly differs from production is worse than none.
# Everything below compiles, checks or packages. Nothing below serves.

.PHONY: build vet test docker check lint fmt tidy

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

docker:
	docker build -t tomb-platform .

# The full pre-merge gate. Run this before opening a pull request into develop.
check: build vet test docker

# The same linters CI runs (.golangci.yml). Not part of `check`, which is the
# constitution's gate; CI enforces this one, the way it already enforces gofmt.
lint:
	golangci-lint run ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy
