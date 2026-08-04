# Mail Service

A queue-backed Go mail service that accepts outgoing mail jobs over HTTP and processes them asynchronously. It is designed for sending emails through SMTP or simulating delivery when SMTP is not configured.

## Features

- Accepts mail jobs through an HTTP API
- Stores jobs in an in-memory queue
- Processes jobs asynchronously using worker goroutines
- Supports either real SMTP delivery or a no-op simulation mode
- Exposes a health endpoint for monitoring

## Run locally

```bash
go run .
```

The service starts on port 8080 by default.

## Health check

### Endpoint

```http
GET /health
```

### Response

```http
HTTP/1.1 200 OK
ok
```

## Send a message

### Endpoint

```http
POST /send
Content-Type: application/json
```

### Request body

```json
{
  "to": "recipient@example.com",
  "subject": "Hello from Mail Service",
  "body": "This message was queued and processed by the service."
}
```

### Alternative field

You can also use `target` instead of `to`:

```json
{
  "target": "recipient@example.com",
  "subject": "Hello from Mail Service",
  "body": "This message was queued and processed by the service."
}
```

### Response

If the request is accepted and queued successfully:

```http
HTTP/1.1 202 Accepted
mail queued
```

If the request is invalid:

```http
HTTP/1.1 400 Bad Request
invalid JSON
```

If the queue rejects the message:

```http
HTTP/1.1 500 Internal Server Error
recipient and body are required
```

## Example with curl

### Single mail

```bash
curl -X POST http://localhost:8080/send \
  -H "Content-Type: application/json" \
  -d '{
    "to": "user@example.com",
    "subject": "Hello",
    "body": "Welcome to the mail service"
  }'
```

### Bulk mail

```bash
curl -X POST http://localhost:8080/send/bulk \
  -H "Content-Type: application/json" \
  -d '{
    "recipients": [
      "user1@example.com",
      "user2@example.com"
    ],
    "subject": "Bulk notification",
    "body": "This is a bulk mail message"
  }'
```

### Templated mail

```bash
curl -X POST http://localhost:8080/send/template \
  -H "Content-Type: application/json" \
  -d '{
    "recipients": [
      "user1@example.com",
      "user2@example.com"
    ],
    "subject": "Hello",
    "body": "Hello {{name}}, welcome to our service.",
    "meta": {
      "name": ["Alice", "Bob"]
    }
  }'
```

## Postman collection

A ready-to-import Postman collection is available at [postman_collection.json](postman_collection.json).

### Import into Postman

1. Open Postman.
2. Click Import.
3. Select [postman_collection.json](postman_collection.json).
4. The collection will appear with requests for:
   - Health check
   - Single mail
   - Bulk mail
   - Templated mail

## Docker

### Build image

```bash
docker build -t mailservice .
```

### Run container

```bash
docker run -p 8080:8080 mailservice
```

### Docker Compose

```bash
docker compose up --build
```

## SMTP configuration

Set these environment variables when you want the service to send real emails through SMTP:

```bash
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=your-user
SMTP_PASSWORD=your-password
SMTP_FROM=no-reply@example.com
```

### Notes

- `SMTP_HOST` is the outgoing mail server address.
- `SMTP_PORT` is usually `587` for STARTTLS or `465` for SSL.
- `SMTP_USERNAME` is usually the full email address.
- `SMTP_PASSWORD` is the mailbox password or app password.
- `SMTP_FROM` is the sender address shown to recipients.

## Request and response summary

| Endpoint | Method | Purpose | Success response |
| --- | --- | --- | --- |
| `/health` | GET | Check if the service is running | `200 OK` with `ok` |
| `/send` | POST | Queue a single email for sending | `202 Accepted` with `mail queued` |
| `/send/bulk` | POST | Queue multiple emails for sending | `202 Accepted` with a count such as `2 mails queued` |
| `/send/template` | POST | Queue templated emails using placeholders like `{{name}}` | `202 Accepted` with a count such as `2 templated mails queued` |

## Example payload fields

| Field | Required | Description |
| --- | --- | --- |
| `to` | Yes, unless `target` is used | Recipient email address |
| `target` | Yes, unless `to` is used | Recipient email address |
| `subject` | No | Email subject |
| `body` | Yes | Email body/content |
| `meta` | No | Object used for template replacement such as `{"name": ["Alice"]}` |
