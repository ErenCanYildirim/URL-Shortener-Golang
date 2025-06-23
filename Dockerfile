FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git 

COPY go.mod go.sum ./

RUN go mod download 

COPY main.go .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o url-shortener .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup    

WORKDIR /app

COPY --from=builder /app/url-shortener .

RUN mkdir -p /app/data && chown -R appuser:appgroup /app

USER appuser

EXPOSE 8080


CMD ["./url-shortener"]