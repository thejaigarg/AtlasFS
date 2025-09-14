# Use Go 1.22 instead of 1.21
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY services/gateway/ ./services/gateway/

# Build the binary
WORKDIR /app/services/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -o gateway main.go

# Final stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/services/gateway/gateway .

# Expose port
EXPOSE 8080

# Run the binary
CMD ["./gateway"]