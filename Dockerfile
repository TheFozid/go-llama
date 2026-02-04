# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o go-llama-backend ./cmd/server

# Final stage
FROM alpine:3.18
# Install poppler-utils for PDF text extraction
RUN apk add --no-cache poppler-utils
WORKDIR /app
COPY --from=builder /app/go-llama-backend .
COPY --from=builder /app/frontend ./frontend
COPY --from=builder /app/static ./static
EXPOSE 8070
CMD ["./go-llama-backend"]
