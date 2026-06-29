# Build stage
FROM golang:1.24-alpine AS builder

# Install protoc-gen tools
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

WORKDIR /app
COPY sidecar .

# Generate gRPC code and build sidecar binary
RUN make proto && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /rekja-sidecar ./cmd/rekja-sidecar

# Runtime stage - minimal image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

# Create the default plugins directory structure
RUN mkdir -p /config/valheimplus/plugins

COPY --from=builder /rekja-sidecar /usr/local/bin/rekja-sidecar

EXPOSE 50051

ENTRYPOINT ["rekja-sidecar"]
