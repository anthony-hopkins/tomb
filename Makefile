# Targets mirror the merge gate in the constitution's Development Workflow:
# go build, go vet, go test ./..., and a successful Docker image build.

.PHONY: build vet test docker check run fmt tidy

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

docker:
	docker build -t tomb-platform .

# The full pre-merge gate. Run this before opening a pull request.
check: build vet test docker

run:
	docker compose up --build

fmt:
	gofmt -l -w .

tidy:
	go mod tidy
