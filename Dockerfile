# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Compile static binary (CGO_ENABLED=0 with modernc.org/sqlite pure Go)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /src/uptime-monitor ./cmd/server

# Final runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /src/uptime-monitor /app/uptime-monitor

# Create data directory for persistent SQLite database
RUN mkdir -p /data

EXPOSE 8080

VOLUME ["/data"]

ENTRYPOINT ["/app/uptime-monitor"]
CMD ["-db", "/data/uptime.db", "-port", "8080"]
