# Outpost

Outpost turns a local AI runtime into a private, OpenAI-compatible endpoint.
Run a model in Ollama, LM Studio, or another local `/v1` server; Outpost adds
API keys, model aliases, request logs, and an optional relay so another machine
can call it without installing a VPN client.

The project is early, but the core loop works:

- local OpenAI-compatible proxy on `127.0.0.1:7341`
- bearer-token API keys with local config storage
- `/v1/models`, `/v1/chat/completions`, `/v1/completions`, and `/v1/embeddings`
- streaming responses without buffering
- model aliases such as `outpost-default -> llama3.2:1b`
- local JSONL request logs
- relay publishing for a public slug URL
- Electron control surface for setup, health checks, and copyable client config
- hosted relay bundle generation for Docker deployment
- Railway-ready relay deployment config

## How It Works

```text
OpenAI client
  |
  | Authorization: Bearer op_...
  v
Outpost local API :7341
  |
  v
Ollama / LM Studio / OpenAI-compatible local backend
```

For remote access, Outpost can connect to a relay:

```text
OpenAI client
  |
  | HTTPS relay URL + relay request token + Outpost API key
  v
Hosted or local Outpost relay
  |
  | WebSocket agent tunnel
  v
Outpost publish agent on your machine
  |
  v
Outpost local API -> local model backend
```

The relay is not a VPN. It forwards only the reserved Outpost slug path, and
the local machine initiates the outbound agent connection.

## Requirements

- Go 1.22+
- Node.js 20+ for the desktop app
- A local model backend:
  - Ollama: `http://127.0.0.1:11434`
  - LM Studio: usually `http://127.0.0.1:1234/v1`
  - any OpenAI-compatible local server

Docker is optional, and only needed for the hosted relay bundle or Railway's
Dockerfile build.

## Build

```sh
go build -o outpost ./cmd/outpost
```

Run tests:

```sh
go test ./...
```

## Quick Start

Start your local model backend first, then run:

```sh
./outpost start
```

On first run, Outpost creates a config file and prints the initial API key once.
Use that key from any OpenAI-compatible client:

```sh
curl http://127.0.0.1:7341/v1/models \
  -H "Authorization: Bearer $OUTPOST_API_KEY"
```

Client base URL:

```text
http://127.0.0.1:7341/v1
```

## Desktop App

The Electron app is the easiest way to use Outpost while it is still young.
It discovers local models, saves the default alias, starts/stops relay and
publish processes, runs diagnostics, and shows copyable client settings.

```sh
cd desktop
npm install
npm start
```

Package the macOS app:

```sh
npm --prefix desktop run pack
```

The desktop app wraps the same `outpost` binary, so the CLI and UI share the
same config and behavior.

## CLI

```sh
outpost start
outpost status
outpost keys list
outpost keys create <name>
outpost keys revoke <id-or-prefix>
outpost logs tail
outpost logs search <query>
outpost expose local
outpost expose lan
outpost publish
outpost relay identity
outpost relay endpoint create <slug>
outpost relay endpoint list
outpost relay endpoint revoke <slug>
outpost relay hosted prepare
outpost relay serve
outpost relay connect
outpost config path
outpost config print
outpost config edit
```

## Configuration

Outpost stores config in the OS user config directory by default. Override it
with:

```sh
OUTPOST_CONFIG=/path/to/config.json ./outpost start
```

Example config shape:

```json
{
  "listen_addr": "127.0.0.1:7341",
  "backend": {
    "type": "ollama",
    "base_url": "http://127.0.0.1:11434"
  },
  "model_aliases": {
    "outpost-default": "llama3.2:1b"
  }
}
```

For LM Studio, use:

```json
{
  "backend": {
    "type": "lmstudio",
    "base_url": "http://127.0.0.1:1234/v1"
  }
}
```

## Model Aliases

Aliases let clients use stable model names while the local backend keeps its
native model IDs:

```json
{
  "model_aliases": {
    "gpt-local": "qwen2.5-coder:7b",
    "outpost-default": "llama3.2:1b"
  }
}
```

When a client requests `gpt-local`, Outpost forwards the request to
`qwen2.5-coder:7b`.

## Local Relay

For development, run a local relay and connect this machine as the publish
agent:

```sh
./outpost relay serve --listen 127.0.0.1:8787 --token dev
./outpost relay connect \
  --relay http://127.0.0.1:8787 \
  --slug demo \
  --target http://127.0.0.1:7341 \
  --token dev
```

Clients can then use:

```text
http://127.0.0.1:8787/demo/v1
```

For relay-side request auth, reserve the slug and generate a public relay
token:

```sh
DEVICE_ID=$(./outpost relay identity)

./outpost relay endpoint create demo \
  --device "$DEVICE_ID" \
  --public-token auto

./outpost relay serve --listen 127.0.0.1:8787 --token dev
```

Clients send both headers:

```sh
curl http://127.0.0.1:8787/demo/v1/models \
  -H "X-Outpost-Relay-Token: Bearer $OUTPOST_RELAY_PUBLIC_TOKEN" \
  -H "Authorization: Bearer $OUTPOST_API_KEY"
```

## Publish

`outpost publish` is the one-command local publisher flow. It ensures the local
Outpost API is running, creates or reuses an API key, prints the client config,
and connects the relay agent with reconnect/backoff:

```sh
./outpost publish \
  --relay http://127.0.0.1:8787 \
  --slug demo \
  --relay-token dev
```

Use `--once` for old-school one-shot development behavior.

## Hosted Relay

### Railway

Railway is the first recommended hosted target. The repo includes
`railway.json`, which tells Railway to build `Dockerfile.relay`, run the relay
container, and use `/healthz` as the deploy health check. The relay listens on
Railway's injected `PORT` automatically.

Generate Railway variables:

```sh
./outpost relay hosted prepare \
  --platform railway \
  --relay https://your-service.up.railway.app \
  --slug demo \
  --dir deploy/relay/generated \
  --force
```

Then:

1. Create a Railway service from this GitHub repo.
2. Paste `deploy/relay/generated/railway.env` into the Railway service
   Variables tab.
3. Deploy the service.
4. Copy the Railway public domain into the desktop app's Hosted Relay URL.
5. Press Start all in the desktop app to publish your local model to that relay.

Railway gives the public URL only after the service is created, so it is fine
to generate the bundle with a placeholder URL first and update the desktop Relay
URL after deploy.

### Docker

Generate a Docker-ready hosted relay bundle:

```sh
./outpost relay hosted prepare \
  --relay https://relay.example.com \
  --slug demo \
  --dir deploy/relay/generated \
  --force
```

The bundle contains:

- `relay.env`
- `relay_endpoints.json`
- `docker-compose.yml`
- a generated README with desktop and client settings

Run the hosted relay:

```sh
cd deploy/relay/generated
docker compose up --build
```

The root `Dockerfile.relay` builds a small relay-only container. The relay can
load endpoint reservations from `OUTPOST_RELAY_ENDPOINTS_B64`, so platforms that
prefer environment variables do not need writable registry storage.

## Security Model

Outpost has two separate auth layers:

- Outpost API key: protects the OpenAI-compatible `/v1/*` API.
- Relay token: protects relay agent registration and, when configured, public
  relay requests before traffic reaches the local machine.

Generated API keys and relay tokens are shown once. Do not commit generated
`relay.env`, config files, or desktop state.

## Project Status

Outpost is an alpha prototype. It is good enough for local experiments and
private testing, but it is not yet a hardened hosted service. The next useful
pieces are hosted relay verification, TLS/domain setup helpers, clearer
packaging, and a tighter first-run onboarding flow.
