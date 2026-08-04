FROM golang:1.22-alpine AS builder
WORKDIR /app

COPY go.mod ./
COPY main.go ./

RUN go build -o /app/mailservice

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/mailservice /app/mailservice
EXPOSE 8080
ENTRYPOINT ["/app/mailservice"]
