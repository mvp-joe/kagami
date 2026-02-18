# Kagami Basic Worker Example

A minimal Cloudflare Worker showing how to integrate the `kagami` package.

## Setup

1. Install dependencies:

```bash
npm install
```

2. Create the D1 database:

```bash
wrangler d1 create kagami
```

3. Update `wrangler.toml` with the database ID from the previous command.

4. Apply the migration:

```bash
wrangler d1 migrations apply kagami
```

5. Generate and set the project secret:

```bash
kagami project-secret
wrangler secret put KAGAMI_PROJECT_SECRET
```

6. Update `KAGAMI_BASE_DOMAIN` in `wrangler.toml` to match your Worker's domain.

## Development

```bash
wrangler dev
```

## Deploy

```bash
wrangler deploy
```

## What's Included

- `src/index.ts` — Hono app wired with kagami proxy middleware and management routes
- `wrangler.toml` — Worker config with DO, D1, and variable bindings
- `migrations/0001_create_machines.sql` — D1 schema for machine registry

## Next Steps

After deploying, use the kagami CLI to register a machine and start tunneling:

```bash
kagami init          # Register this machine with the Worker
kagami tunnel add --name api --local-addr localhost:8080 --hostname api.<tunnel-id>.<base-domain>
kagami run           # Start the agent (foreground)
```

See the [main README](../../README.md) for full documentation.
