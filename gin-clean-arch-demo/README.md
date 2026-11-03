# gin-clean-arch-demo

Layered Go HTTP service: Gin transport, typesafe domain model, SQLite
persistence and Valkey (Redis-compatible) read-through caching.

## Layout

```
cmd/server/                 entrypoint: config, composition root, graceful shutdown
internal/domain/            entities, value types, validation, sentinel errors
internal/usecase/           application service + ports (UserRepository, Cache)
internal/logger/            zerolog construction + request-scoped logger in context
internal/adapter/sqlite/    GORM model (schema source of truth), store, repository
internal/adapter/cache/     Valkey/Redis implementation of the Cache port
internal/adapter/httpapi/
  server.go                 Server struct, router, http.Server with timeouts
  handlers.go               one handler per endpoint, request/response types
  middleware.go             request id + structured access log
internal/app/               composition root: build, run, graceful shutdown
internal/config/            caarlos0/env loader with defaults and validation
```

Dependencies point inward: `domain` imports nothing internal, `usecase`
defines the interfaces, adapters implement them, and `cmd/server` is the only
place that wires concrete types together.

## Persistence

SQLite through [GORM](https://gorm.io) with the pure-Go `glebarez/sqlite`
driver (no cgo). `internal/adapter/sqlite/model.go` is the schema's single
source of truth: column names, the `status` CHECK constraint, and both indexes
live on the struct tags, and `AutoMigrate` applies them at startup (idempotent,
so restarts are no-ops). Timestamps are application-owned
(`autoCreateTime:false`/`autoUpdateTime:false`), so the domain decides when a
write happened. `TranslateError` maps unique violations to
`gorm.ErrDuplicatedKey`, which the repository converts to
`domain.ErrEmailTaken`.

## Types

`UserID`, `Email`, `DisplayName` and `UserStatus` are distinct Go types. The
only way to obtain a valid value from untrusted input is a `Parse*` function,
so validation cannot be skipped by accident; `User` is built through
`domain.NewUser` and transitioned through `Rename` / `ChangeStatus`.

## Logging

[zerolog](https://github.com/rs/zerolog) writes one event per line. Every layer
owning a log line adds its own `layer` field, and requests carry `request_id`
(echoed in `X-Request-ID`) so handler and service lines can be joined:

```json
{"level":"info","layer":"usecase","op":"create_user","request_id":"trace-777",
 "method":"POST","path":"/v1/users","user_id":"01a1…","message":"user created"}
{"level":"info","layer":"http","request_id":"trace-777","method":"POST",
 "path":"/v1/users","status":201,"bytes":201,"duration_ms":1.378,
 "client_ip":"127.0.0.1","message":"http request"}
```

- `internal/logger` owns construction, level/format parsing and the context
  plumbing (`WithLogger`, `WithRequestID`); the usecase reads the request
  logger with `logger.FromContext`, so no `slog` globals leak across requests.
- The access log's level follows the status code (5xx error, 4xx warn).
- `LOG_FORMAT=console` gives colourised human output for local runs.

## API

| Method | Path            | Description                          |
| ------ | --------------- | ------------------------------------ |
| GET    | `/healthz`      | DB + cache readiness (503 if down)   |
| POST   | `/v1/users`     | Create a user → 201 + `Location`     |
| GET    | `/v1/users`     | List: `limit`, `offset`, `status`    |
| GET    | `/v1/users/:id` | Fetch one (cache-backed)             |
| PATCH  | `/v1/users/:id` | Update `display_name` and/or `status`|
| DELETE | `/v1/users/:id` | Delete → 204                         |

Errors are `{"error": "...", "field": "..."}` with 400 (validation),
404 (missing), 409 (duplicate email), 500 (unexpected, logged server side).

## Configuration

| Variable              | Default          | Meaning                              |
| --------------------- | ---------------- | ------------------------------------ |
| `HTTP_ADDR`           | `:8080`          | listen address                       |
| `DATABASE_PATH`       | `data/app.db`    | SQLite file (`:memory:` for scratch) |
| `DATABASE_MAX_CONNS`  | `4`              | connection pool size                 |
| `DATABASE_BUSY_TIMEOUT` | `5s`           | SQLite lock wait                     |
| `DATABASE_LOG_SQL`    | `false`          | log every statement at debug         |
| `CACHE_ADDR`          | `127.0.0.1:6379` | Valkey/Redis address                 |
| `CACHE_PASSWORD`      | –                | optional AUTH password               |
| `CACHE_DB`            | `0`              | logical database index               |
| `CACHE_TTL`           | `5m`             | cached user lifetime (≤0 disables)   |
| `CACHE_TIMEOUT`       | `2s`             | dial/read/write timeout              |
| `SHUTDOWN_TIMEOUT`    | `10s`            | graceful shutdown budget             |
| `LOG_LEVEL`           | `info`           | `trace`…`panic`                      |
| `LOG_FORMAT`          | `json`           | `json` (production) or `console`     |
| `GIN_MODE`            | –                | set `release` to silence route logs  |

## Run

```sh
docker compose up -d valkey
go run ./cmd/server
```

```sh
curl -sS localhost:8080/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","display_name":"Ada Lovelace"}'
```

## Test

```sh
go test ./...        # uses a temp SQLite file and an in-process Redis (miniredis)
go test -race ./...
```
