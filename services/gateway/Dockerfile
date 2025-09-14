# services/gateway/Dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY services/ ./services/

WORKDIR /app/services/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -o gateway main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/services/gateway/gateway .

EXPOSE 8080
CMD ["./gateway"]