# Estágio 1
FROM golang:1.25-alpine AS builder
WORKDIR /build

# Dependências
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o driver-service .

# Estágio 2
FROM alpine:3.20
WORKDIR /app

COPY --from=builder /build/driver-service /app/driver-service
RUN chmod +x /app/driver-service

CMD ["/app/driver-service"]
