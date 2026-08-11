FROM golang:1.25.12-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY version ./version
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ecs ./cmd/ecs \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ecs-mock-llm ./cmd/ecs-mock-llm

FROM alpine:3.20 AS mock-llm
RUN apk add --no-cache ca-certificates wget \
    && addgroup -S ecs \
    && adduser -S -G ecs ecs
COPY --from=build /out/ecs-mock-llm /usr/local/bin/ecs-mock-llm
USER ecs
EXPOSE 11434
HEALTHCHECK --interval=5s --timeout=2s --start-period=2s --retries=10 \
  CMD wget -qO- http://127.0.0.1:11434/healthz >/dev/null || exit 1
ENTRYPOINT ["ecs-mock-llm"]
CMD ["-listen", "0.0.0.0:11434"]

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates wget \
    && addgroup -S ecs \
    && adduser -S -G ecs ecs
WORKDIR /app
COPY --from=build /out/ecs /usr/local/bin/ecs
COPY prompts ./prompts
COPY tools.d ./tools.d
COPY skills.d ./skills.d
COPY agents.d ./agents.d
COPY pipelines.d ./pipelines.d
COPY office.d ./office.d
COPY config.docker.json ./config.json
RUN mkdir -p /app/data \
    && chown -R ecs:ecs /app
USER ecs
EXPOSE 8090
VOLUME ["/app/data"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- http://127.0.0.1:8090/healthz >/dev/null || exit 1
ENTRYPOINT ["ecs"]
CMD ["-config", "/app/config.json"]
