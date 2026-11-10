FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /server ./cmd/server

FROM mirror.gcr.io/library/alpine:3.23

# docker-cli + compose plugin: hosted stacks are deployed with `docker compose`
# (the same command an operator runs to boot them without the panel).
RUN apk --no-cache add ca-certificates curl docker-cli docker-cli-compose

WORKDIR /app

COPY --from=build /server /app/server

VOLUME /app/data
# Audit trail (audit.log): mount it to keep it across container recreations.
VOLUME /app/log

EXPOSE 2538

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD curl -f http://localhost:2538/_health || exit 1

ENTRYPOINT ["/app/server"]
