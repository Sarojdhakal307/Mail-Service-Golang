# Run Mail Service from the published image

Everything needed to run Mail Service on a server with the ready-made image
`ghcr.io/sarojdhakal307/mailservice`. You don't need the source code or Go.

Requirements: Docker with the Compose plugin.

## Setup

1. Copy this folder (`docker-compose.yml` and `.env.example`) to the server, for example to `~/mailservice`.
2. Create your configuration and fill in every empty value:

   ```bash
   cp .env.example .env
   openssl rand -hex 24   # POSTGRES_PASSWORD
   openssl rand -hex 32   # API_KEY_ENCRYPTION_KEY
   openssl rand -hex 32   # SUPER_API_KEY (optional)
   ```

   Also set `SUPERUSER_PASSWORD` (at least 12 characters) and your SMTP server.
3. Start it:

   ```bash
   docker compose up -d
   docker compose logs -f mailservice
   ```

The service listens on `127.0.0.1:8080`. Put a reverse proxy with HTTPS (nginx, Caddy) in front of it,
then open `https://<your-domain>/admin/` and sign in as the super user.

To test without a proxy or HTTPS, set `BIND_ADDR=0.0.0.0`, `TRUST_PROXY=false` and `COOKIE_SECURE=false`,
then open `http://<server-ip>:8080/admin/`.

## Choosing a version

`MAILSERVICE_VERSION` in `.env` selects the image tag:

| Value | Runs |
|---|---|
| `latest` | The newest release or master build |
| `1.2.3` | Exactly that release (recommended in production) |
| `1.2` | The newest 1.2.x patch release |

## Updating

```bash
# optionally change MAILSERVICE_VERSION in .env first
docker compose pull
docker compose up -d
```

To roll back, set `MAILSERVICE_VERSION` to the previous version and run the same commands.

## Data and backups

The database is stored in `./Dockerdata/postgres` next to `docker-compose.yml`. Back it up with:

```bash
docker compose exec postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' > backup.sql
```

Keep `.env` safe as well: without `API_KEY_ENCRYPTION_KEY`, stored API keys can no longer be revealed.

## Private image

If the package is private, log in once on the server with a GitHub personal access token that has the
`read:packages` scope:

```bash
echo <token> | docker login ghcr.io -u <github-username> --password-stdin
```
