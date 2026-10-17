# Generated OpenAPI bundle

`apidocs.swagger.json` is written here by `protoc-gen-openapiv2` when you run
`make proto`. Do not edit anything in this directory by hand — the protos in
`api/v1/` are the source of truth (Phase 5.2: the REST/JSON contract is
generated from them, so the two cannot drift).

The bundle is committed so a clone builds without protoc, and is embedded into
the API server binary by `../embed.go`. The server serves it at
`/swagger/apidocs.swagger.json`.

It covers the RPC routes (`/api/v1/optimizations`, `/api/v1/me`). The two OAuth
routes, `/api/v1/auth/github/login` and `/api/v1/auth/github/callback`, are
plain HTTP redirects rather than RPCs and are documented in `docs/API.md`.
