# Mail Service

A queue-backed Go mail service that accepts outgoing mail jobs over HTTP and processes them asynchronously. It is designed for sending emails through SMTP or simulating delivery when SMTP is not configured.

## Features

- Accepts mail jobs through an HTTP API
- Stores jobs in an in-memory queue
- Processes jobs asynchronously using worker goroutines
- Supports either real SMTP delivery or a no-op simulation mode
- Exposes a health endpoint for monitoring
- Public landing page with live system status and an API key request form
- Detailed API documentation at `/docs/`
- Admin web UI for a super user to create and manage API keys and review access requests
- Per-key allowed IPs and hourly / daily / weekly / monthly mail limits
- Super API keys with no limits and no IP restriction
- PostgreSQL storage for API keys and a log of every authenticated request

## Quick start (Docker Compose)

```bash
cp .env.example .env
# edit .env: set POSTGRES_PASSWORD, SUPERUSER_PASSWORD and optionally SUPER_API_KEY
docker compose up --build
```

| Page | URL |
| --- | --- |
| Public site: overview, live status, request an API key | <http://localhost:8080/> |
| API documentation | <http://localhost:8080/docs/> |
| Admin console (super user) | <http://localhost:8080/admin/> |

## Run locally

Start only the database with Compose, then run the service with Go:

```bash
cp .env.example .env              # fill in the values, including DATABASE_URL
docker compose up -d postgres
go run .
```

The service reads `.env` from the working directory on startup (variables already set in the environment take precedence). It starts on port 8080 by default, or on `PORT` if set. The database tables are created automatically on startup.

The service **refuses to start** if the database is unreachable, `SUPERUSER_PASSWORD` is shorter than 12 characters, or `SUPER_API_KEY` is set but shorter than 32 characters.

## Public site and docs

- `/` describes the service, shows live status from `/api/status` (refreshed every 30 seconds) and has a form to **request an API key**.
- `/docs/` is the full API reference: authentication, IP rules, rate limits, every endpoint, error codes, and examples in cURL, Node.js, Python and Go. Code samples use the server's own address automatically.

Access requests are stored in PostgreSQL and show up in the admin console under **Access requests**. The form is protected by:

- a limit of 3 accepted requests per IP per hour
- a hidden honeypot field; submissions that fill it get a normal response but are dropped
- JSON-only submissions, so other websites cannot post to it with a plain HTML form
- server-side validation: name, email, phone number (7–15 digits, optional `+` and separators) and use case (at least 20 characters) are required; organization, address, expected volume, server IPs and a free-text message or questions (up to 5,000 characters) are optional

All pages ship with a strict Content Security Policy (`script-src 'self'`, no inline scripts or styles) and `X-Frame-Options: DENY`.

## Admin UI (super user)

The admin UI at `/admin/` is protected by the super user account from `SUPERUSER_USERNAME` / `SUPERUSER_PASSWORD`. From it you can:

- Create API keys with a **name**, **address**, **allowed IPs** and **limits**. The full key is shown only once; only its SHA-256 hash is stored.
- Edit, disable/enable or delete keys.
- See each key's current usage against its limits, when and from which IP it was last used, and which IP created it.
- Browse the request log (all keys or one key), filtered by status: time, key, IP, endpoint, mail count, status and message.
- Review **access requests** from the public site. **Approve & create key** opens the key form prefilled from the request (name, contact, server IPs), then creates the key and marks the request approved in one step. **Reject** marks it rejected. A request can only be reviewed once.

Security notes:

- Sessions are held in memory in an `HttpOnly`, `SameSite=Strict` cookie and last 12 hours. Restarting the service signs everyone out.
- After 5 failed logins from one IP within 15 minutes, further attempts from that IP are refused for the rest of the window.
- Serve the UI over HTTPS in production and set `COOKIE_SECURE=true`.

## Authentication

Every endpoint except `/health` and the admin UI requires an API key created in the admin UI (or `SUPER_API_KEY`). Send it with either header:

```http
X-API-Key: <your-api-key>
```

```http
Authorization: Bearer <your-api-key>
```

### API key settings

| Setting | Description |
| --- | --- |
| Name | Required. Who the key belongs to |
| Address | Optional. Owner address or contact |
| Allowed IPs | `*` for any IP, or a list of IPs / CIDR ranges such as `203.0.113.5, 10.0.0.0/8`. Empty means `*` |
| Limit per hour / day / week / month | Maximum **mails** in a rolling window of 1 hour, 24 hours, 7 days and 30 days. `0` means unlimited |
| Super | No limits and any IP allowed. Requests are still logged |

Limits count mails, not HTTP requests: a bulk request to 20 recipients uses 20. A request that would go over any limit is rejected as a whole and nothing is queued. Checks are serialized per key in the database, so parallel requests cannot overshoot a limit.

### Error responses

| Status | When | Example `error` |
| --- | --- | --- |
| `401` | Key missing | `missing API key: send it in the X-API-Key header or as Authorization: Bearer <key>` |
| `401` | Key unknown | `invalid API key` |
| `403` | Key disabled | `API key is disabled` |
| `403` | IP not in the key's allow list | `IP address 198.51.100.7 is not allowed for this API key` |
| `429` | A limit would be exceeded | `Hourly limit exceeded: 3 of 3 mails already used in the last hour and this request needs 1 more. Try again after 2026-09-18T17:23:08Z` |
| `429` | The request alone is larger than a limit | `Hourly limit exceeded: this request contains 4 mails but the limit is 3 mails per hour; split it into smaller requests` |

A `429` response also includes the window, limit, used and requested counts, and `retry_at` plus a `Retry-After` header when waiting will help:

```json
{
  "error": "Hourly limit exceeded: ...",
  "window": "hour",
  "limit": 3,
  "used": 3,
  "requested": 1,
  "retry_at": "2026-09-18T17:23:08Z"
}
```

### Super API key

Set `SUPER_API_KEY` in `.env` to register a key that bypasses all limits and IP rules. It is added to the database on startup and appears in the admin UI as *Super key (SUPER_API_KEY)*. You can also tick **Super key** when creating a key in the UI.

### Client IP behind a proxy

By default the client IP is the TCP peer address. If the service runs behind a reverse proxy you control, set `TRUST_PROXY=true` so the first `X-Forwarded-For` address is used instead. Do not enable this otherwise, because clients could forge the header to get around IP restrictions.

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
X-API-Key: <your-api-key>
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

If the API key is missing or invalid:

```http
HTTP/1.1 401 Unauthorized
{"error":"invalid API key"}
```

If the request is invalid:

```http
HTTP/1.1 400 Bad Request
recipient and body are required
```

If the key's IP rules or limits reject the request, see [Error responses](#error-responses). Invalid requests are rejected before they count towards any limit.

## Example with curl

```bash
export API_KEY=<your-api-key>
```

### Single mail

```bash
curl -X POST http://localhost:8080/send \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
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
  -H "X-API-Key: $API_KEY" \
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
  -H "X-API-Key: $API_KEY" \
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
5. Open the collection's **Variables** tab and set `apiKey` to one of your `API_KEYS`. All mail requests send it as `X-API-Key`; the health check does not.

## Docker

### Build image

```bash
docker build -t mailservice .
```

### Run container

```bash
docker run --env-file .env -p 8080:8080 mailservice
```

The container needs a reachable database in `DATABASE_URL`. Docker Compose (below) sets that up for you.

### Docker Compose

Compose starts the service and a PostgreSQL 16 database. Data is kept in the `pgdata` volume, and Postgres is published only on `127.0.0.1:5432`.

```bash
cp .env.example .env   # then fill in the required values
docker compose up --build
```

`.env` is excluded from git and from the Docker build context, so secrets are never baked into the image.

## Environment variables

See [.env.example](.env.example) for a complete template.

| Variable | Required | Description |
| --- | --- | --- |
| `DATABASE_URL` | Yes | PostgreSQL connection string (set automatically by Docker Compose) |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | For Compose | Credentials for the bundled Postgres container |
| `SUPERUSER_USERNAME` | Yes | Admin UI username |
| `SUPERUSER_PASSWORD` | Yes | Admin UI password, at least 12 characters |
| `SUPER_API_KEY` | No | Unlimited, IP-unrestricted API key, at least 32 characters |
| `TRUST_PROXY` | No | `true` to read the client IP from `X-Forwarded-For` |
| `COOKIE_SECURE` | No | `true` to mark the admin session cookie `Secure` (HTTPS) |
| `PORT` | No | HTTP port, defaults to `8080` |
| `SMTP_*` | No | SMTP settings below; without them mail is only logged |

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

| Endpoint | Method | Auth | Purpose | Success response |
| --- | --- | --- | --- | --- |
| `/health` | GET | None | Check if the service is running | `200 OK` with `ok` |
| `/api/status` | GET | None | Service status as JSON (database, delivery mode, uptime, version) | `200 OK` with JSON |
| `/api/key-requests` | POST | None | Submit an API key request (used by the public site) | `201 Created` with JSON |
| `/send` | POST | API key | Queue a single email for sending | `202 Accepted` with `mail queued` |
| `/send/bulk` | POST | API key | Queue multiple emails for sending | `202 Accepted` with a count such as `2 mails queued` |
| `/send/template` | POST | API key | Queue templated emails using placeholders like `{{name}}` | `202 Accepted` with a count such as `2 templated mails queued` |

## Example payload fields

| Field | Required | Description |
| --- | --- | --- |
| `to` | Yes, unless `target` is used | Recipient email address |
| `target` | Yes, unless `to` is used | Recipient email address |
| `subject` | No | Email subject |
| `body` | Yes | Email body/content |
| `meta` | No | Object used for template replacement such as `{"name": ["Alice"]}` |
