FROM golang:1.22-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . ./

RUN CGO_ENABLED=0 go build -o /app/mailservice

FROM alpine:3.20
WORKDIR /app
RUN adduser -D -H mailservice
COPY --from=builder /app/mailservice /app/mailservice
USER mailservice
EXPOSE 8080
ENTRYPOINT ["/app/mailservice"]
