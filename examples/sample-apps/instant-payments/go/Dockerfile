# Patch-pinned build/runtime images make upgrades explicit; the Go stage emits a static binary.
FROM golang:1.26.1-alpine3.22 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/instantpayments ./cmd/main

# Keep the compiler out of runtime; certificates support AWS TLS and curl supports readiness.
FROM alpine:3.22.2
RUN apk add --no-cache ca-certificates curl \
    && addgroup -S app \
    && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/instantpayments /app/instantpayments
# A dedicated unprivileged identity limits the impact of application compromise.
USER app
EXPOSE 8080
ENTRYPOINT ["/app/instantpayments"]
