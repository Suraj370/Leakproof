# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS base
WORKDIR /src
COPY go.mod ./
COPY . .
# go.sum is generated here so the repo doesn't need one checked in for the first build.
RUN --mount=type=cache,target=/go/pkg/mod go mod tidy

# `docker build --target test` runs the unit tests (race detector needs cgo, so plain -v here).
FROM base AS test
RUN --mount=type=cache,target=/go/pkg/mod go vet ./... && go test ./... -count=1

# Integration tests need PostgreSQL: run via compose profile itest (sets TEST_DATABASE_URL).
FROM base AS itest
CMD ["go","test","./internal/catalog/...","./internal/reports/...","-run","Integration","-count=1","-v"]

FROM base AS build
RUN --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
