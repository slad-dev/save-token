FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN go build -o /bin/agent-gateway ./cmd/gateway

FROM alpine:3.21

WORKDIR /app

RUN mkdir -p /app/data

COPY --from=builder /bin/agent-gateway /usr/local/bin/agent-gateway
COPY config.json.example /app/config.json

EXPOSE 8080

ENV CONFIG_PATH=/app/config.json

CMD ["agent-gateway"]
