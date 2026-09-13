# syntax=docker/dockerfile:1

# Principle IV: the image built here is the image promoted to production.
# Environment-specific behaviour comes from env vars, never a different build.

FROM golang:1.27 AS build
WORKDIR /src

# Dependency layer first so source edits do not re-download modules.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static binary so it can run on a distroless/static base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tomb ./cmd/tomb

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

# Templates, the stylesheet and SQL migrations are all embedded in the binary
# via go:embed, so the runtime needs nothing but the binary.
COPY --from=build /out/tomb /app/tomb

# The production Compose project travels with the image. The VM extracts these
# on boot and on every deploy, which keeps compose.yaml and the Caddyfile in
# lockstep with the code that expects them, and means the VM never needs a
# checkout of this repository.
COPY --from=build /src/deploy/compose.yaml /deploy/compose.yaml
COPY --from=build /src/deploy/Caddyfile    /deploy/Caddyfile
COPY --from=build /src/deploy/deploy.sh    /deploy/deploy.sh
COPY --from=build /src/deploy/configure.sh /deploy/configure.sh

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/tomb"]
