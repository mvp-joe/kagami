# Kagami - Initial Architecture

## Summary

Kagami ("mirror" in Japanese) is a local-proxy tool that mirrors local APIs to the internet without exposing the local machine. A lightweight Go agent (`kagami`) connects outbound via WebSocket to a Cloudflare Durable Object that acts as a "digital twin" of the local machine. External HTTP requests hit the DO, which relays them to the agent over the persistent WebSocket. The agent proxies to configured local services and returns the response. This lets developers write normal HTTP APIs locally and expose them to the internet through a secure, always-on tunnel — no custom WebSocket code, no open ports.

The Cloudflare-side components (Worker routes, DO class) are distributed as an NPM package that users integrate into their own Worker application. Each project deployment has its own shared secret and machine registry — multiple machines connect to the same Worker, each with a unique per-machine secret stored in D1.

## Goals

- NPM package (`kagami`) exporting Hono routes, proxy middleware, and a DO class for integration into any Cloudflare Worker
- Example project showing how to wire up the package
- One DO class, one instance per machine (digital twin model) using WebSocket Hibernation for cost efficiency
- Single Go binary: `kagami run` (daemon), `kagami install` (systemd), `kagami project-secret/init/tunnel/status` (management)
- TOML config at `/etc/kagami/kagami.toml` supporting multiple local services per machine
- Binary-framed request/response multiplexing over a single WebSocket connection (JSON metadata + raw body bytes) with chunking for large bodies
- Two-tier authentication: project secret (admin) + per-machine secrets (generated during registration, stored in D1)
- Machine registration via `kagami init` — agent registers with the Worker, receives a unique secret
- Management APIs protected by project secret (list machines, revoke machines)
- Subdomain-based routing: host matching `*.BASE_DOMAIN` is proxied, tunnel ID is the rightmost subdomain label before `BASE_DOMAIN`, full Host forwarded to agent
- Body size limits enforced at the DO before framing onto WebSocket
- systemd integration for boot-persistent operation on Linux
- HTTP proxying end-to-end in v1

## Non-Goals

- WebSocket passthrough (external WS clients tunneled to local WS services) — deferred post-v1
- gRPC / ConnectRPC protocol support — deferred post-v1
- Web dashboard / UI — CLI-only for now
- Multi-agent per DO (load balancing) — strictly 1:1 digital twin model
- Custom domain provisioning automation — users deploy and configure DNS themselves
- Pure binary protocol / msgpack — hybrid JSON header + raw body is sufficient
- macOS / launchd support — planned for v2 fast-follow

## Current Status

Planning

## Key Files

- [system-components.md](./system-components.md) — Architecture: Worker, DO, Go agent, wire protocol
- [interface.md](./interface.md) — Type definitions for wire protocol and config
- [apis.md](./apis.md) — Worker HTTP endpoints and DO WebSocket protocol
- [schema.md](./schema.md) — D1 database schema for machine registry
- [implementation.md](./implementation.md) — Phased build plan
- [tests.md](./tests.md) — Test specifications
- [decisions.md](./decisions.md) — Key architectural decisions
- [dependencies.md](./dependencies.md) — New dependencies
- [documentation.md](./documentation.md) — README and docs to create
