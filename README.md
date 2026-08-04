# Mail Service

A basic Go web service containerized with Docker.

## Run locally

```bash
go run .
```

## Build and run with Docker

```bash
docker build -t mailservice .
docker run -p 8080:8080 mailservice
```
