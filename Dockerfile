FROM golang:1.26-alpine AS builder
WORKDIR /build

# Dependencies layer — invalidated only when go.mod/go.sum/vendor change
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY migrations/ migrations/

# Source layer — invalidated only when Go source changes
COPY cmd/ cmd/
COPY internal/ internal/
COPY docs/ docs/

RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /api ./cmd/api

FROM alpine:3.21 AS runtime
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
