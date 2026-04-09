FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api ./cmd/api

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
