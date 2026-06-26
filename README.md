# Medsenger Neyrox Bot

Medsenger agent that syncs health data from the Neyrox smart band into Medsenger.

It polls the [Neyrox API](https://adm.neyrox.com/) for each connected patient and pushes
their measurements (pulse, etc.) into the patient's Medsenger contract as records.

## Development

1. Install **docker** and **make**.
2. Create a `.env` from `.env.example`.

```sh
make            # or: make dev — runs server (air hot-reload) + postgres
make build-dev  # rebuild images (after dependency/Dockerfile changes)
make db-up      # apply goose migrations (run once after first start)
make worker     # run the polling worker inside the dev container
```

### Migrations (goose)

```sh
make db-status                                            # status
make db-up                                                # apply all
make db-down                                              # roll back one
goose -dir=internal/db/migrations create <name> sql       # new migration
```

### templ

HTML is templated with [templ](https://github.com/a-h/templ). After editing `*.templ`:

```sh
make templ      # dev container must be running
```

## Deploying

```sh
make prod       # build + run server and worker containers (compose.prod.yaml)
make fprod      # stop
make logs-prod  # tail logs
```

The server image applies migrations on start; the worker image runs the polling loop.
Production images are built per-arch by CI and pushed to `docker.telepat.online/agents-neyrox-image`.

## License

Created by Tikhon Petrishchev. Copyright © 2026 OOO Telepat. All rights reserved.
