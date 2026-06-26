# syntax=docker/dockerfile:1

ARG GOVERSION=1.26.4


FROM golang:${GOVERSION}-alpine AS dev
RUN go install github.com/air-verse/air@latest && \
    go install github.com/a-h/templ/cmd/templ@latest && \
    go install github.com/pressly/goose/v3/cmd/goose@latest
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
CMD ["air", "-c", ".air.toml"]


FROM --platform=$BUILDPLATFORM golang:${GOVERSION}-alpine AS app-builder
ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
ENV LDFLAGS="-X github.com/tikhonp/medsenger-neyrox-bot/internal/util.AppVersion=${APP_VERSION}"
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "$LDFLAGS" -o /bin/server ./cmd/server
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "$LDFLAGS" -o /bin/manage ./cmd/manage
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "$LDFLAGS" -o /bin/worker ./cmd/worker


FROM alpine AS pre-prod
WORKDIR /src
COPY --from=app-builder /usr/local/go/lib/time/zoneinfo.zip /
ENV ZONEINFO=/zoneinfo.zip
ENV DEBUG=false
ENV SERVER_PORT=80


FROM pre-prod AS server
COPY --from=app-builder /bin/server /bin/manage /bin/
EXPOSE 80
# Migrations are embedded in the manage binary (internal/db/migrate.go); apply them, then serve.
ENTRYPOINT ["/bin/sh", "-c", "manage -c migrate-up && server"]


FROM pre-prod AS worker
COPY --from=app-builder /bin/worker /bin/
ENTRYPOINT ["worker"]
