# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -o api-gateway .

# Final stage
FROM alpine:3.19

WORKDIR /app

# Install ca-certificates in case we connect to external SSL endpoints
RUN apk --no-cache add ca-certificates curl

# Copy the pre-built binary
COPY --from=builder /app/api-gateway .

EXPOSE 8080

# Run the binary
CMD ["./api-gateway"]
