# Hosted Relay

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
