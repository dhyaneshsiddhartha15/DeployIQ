# Generated OpenAPI bundle

`apidocs.swagger.json` is written here by `protoc-gen-openapiv2` when you run
`make proto`. Do not edit anything in this directory by hand — the protos in
`api/v1/` are the source of truth (Phase 5.2: the REST/JSON contract is
generated from them, so the two cannot drift).

The bundle is committed so a clone builds without protoc, and is embedded into
the API server binary by `../embed.go`. The server serves it at `/swagger/`.

No file exists yet because no service protos exist yet — the Phase 4.5 API
surface (`/api/v1/optimizations`, `/api/v1/auth/github/callback`, `/api/v1/me`)
lands with Phase 4 of the build plan.
