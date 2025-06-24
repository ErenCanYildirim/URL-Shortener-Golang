FROM golang:1.24.4-alpine AS builder

RUN apk add --no-cache git wget

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

FROM alpine:latest

RUN apk --no-cache add ca-certificates wget 

WORKDIR /app/

COPY --from=builder /app/main .
COPY --from=builder /app/templates ./templates

RUN adduser -D -s /bin/sh appuser

RUN chown -R appuser:appuser /app/templates

RUN find /app/templates -type d -exec chmod 755 {} \; && \
    find /app/templates -type f -exec chmod 644 {} \;

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./main"]