# Hosted Relay

## Railway

Railway is the easiest first hosted target for this repo.

```sh
./outpost relay hosted prepare \
  --platform railway \
  --relay https://your-service.up.railway.app \
  --slug demo \
  --dir deploy/relay/generated \
  --force
```

The generated `railway.env` contains the service variables to paste into
Railway. The repo-root `railway.json` configures Railway to build
`Dockerfile.relay` and health-check `/healthz`; the relay automatically listens
on Railway's injected `PORT`.

## Docker Compose

Create a deploy bundle from the repo root:

```sh
./outpost relay hosted prepare \
  --relay https://relay.example.com \
  --slug demo \
  --dir deploy/relay/generated \
  --force
```

Run it locally with Docker:

```sh
cd deploy/relay/generated
docker compose up --build
```

The generated `relay.env` contains the agent token and an encoded endpoint
registry. The generated `README.md` contains the matching desktop hosted
settings and client relay header.
