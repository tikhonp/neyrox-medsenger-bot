APP_VERSION := $(shell git rev-parse HEAD)

ENVS := COMPOSE_BAKE=true

.PHONY: run dev build-dev fdev prod fprod logs-prod go-to-server-container \
	db-status db-up db-down db-reset templ worker build-prod-image update-deps

run: dev

dev:
	${ENVS} docker compose -f compose.yaml up

build-dev: fdev
	${ENVS} docker compose -f compose.yaml up --build

fdev:
	${ENVS} docker compose -f compose.yaml down

prod:
	docker compose -f compose.prod.yaml up --build -d

fprod:
	docker compose -f compose.prod.yaml down

logs-prod:
	docker compose -f compose.prod.yaml logs -f -n 100

go-to-server-container:
	docker exec -it --tty neyrox-agent /bin/sh

db-status:
	docker exec -it --tty neyrox-agent /bin/manage -c migrate-status

db-up:
	docker exec -it --tty neyrox-agent /bin/manage -c migrate-up

db-down:
	docker exec -it --tty neyrox-agent /bin/manage -c migrate-down

db-reset:
	docker exec -it --tty neyrox-agent /bin/manage -c migrate-reset

templ:
	docker exec -it --tty neyrox-agent templ generate

# Run one-off worker cycle/loop inside the dev container.
worker:
	docker exec -it --tty neyrox-agent /bin/worker

build-prod-image:
	docker buildx build --build-arg APP_VERSION="${APP_VERSION}" --target server -t docker.telepat.online/agents-neyrox-image:server-latest .
	docker buildx build --build-arg APP_VERSION="${APP_VERSION}" --target worker -t docker.telepat.online/agents-neyrox-image:worker-latest .

update-deps:
	docker exec -it --tty neyrox-agent go get -u ./...
