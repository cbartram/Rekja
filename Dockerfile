FROM golang:1.26.4-alpine AS builder

RUN apk add --no-cache make protobuf protobuf-dev

WORKDIR /app

COPY . .

RUN go mod download && go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

RUN make proto PROTOC=protoc && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/sidecar /app/cmd/sidecar

FROM alpine:3.23.5

WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/sidecar .

RUN mkdir -p /config/valheimplus/plugins

USER 1000

EXPOSE 8080

ENTRYPOINT ["./sidecar"]