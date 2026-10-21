FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
# The image has no .git, so VCS info cannot be read back at runtime. Inject the
# identity explicitly; unset args yield empty values, which the service reports
# as an explicit unknown build instead of guessing.
ARG BUILD_COMMIT=""
ARG BUILD_TIME=""
RUN go build -ldflags "\
    -X codearts2api/internal/server.BuildCommit=${BUILD_COMMIT} \
    -X codearts2api/internal/server.BuildTime=${BUILD_TIME}" \
    -o /out/codearts2api ./cmd/server && \
    go build -o /out/codearts2api-login ./cmd/login && \
    go build -o /out/codearts2api-credit ./cmd/credit && \
    go build -o /out/codearts2api-apply ./cmd/apply

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/
COPY config.example.json ./config.example.json
VOLUME ["/app/auths", "/app/data"]
EXPOSE 7866
CMD ["codearts2api", "-config", "config.json"]
