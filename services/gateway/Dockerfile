# services/download/Dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY services/ ./services/

WORKDIR /app/services/download
RUN CGO_ENABLED=0 GOOS=linux go build -o download main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/services/download/download .

EXPOSE 8085
CMD ["./download"]