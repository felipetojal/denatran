# Estágio 1
FROM golang:1.25-alpine AS builder
WORKDIR /build

# Dependências 
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o fines-service .

# Estágio 2
FROM alpine:3.20
WORKDIR /app

COPY --from=builder /build/fines-service /app/fines-service
RUN chmod +x /app/fines-service

CMD ["/app/fines-service"]
