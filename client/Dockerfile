# Estágio 1
FROM golang:1.25-alpine AS builder
WORKDIR /build

# Dependências
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o client-service .

# Estágio 2
FROM alpine:3.20
WORKDIR /app

COPY --from=builder /build/client-service /app/client-service
RUN chmod +x /app/client-service

ENTRYPOINT ["/app/client-service"]
