FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates wget
WORKDIR /app
COPY --from=builder /app/server .
EXPOSE 3000
CMD ["/app/server"]
