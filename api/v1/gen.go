// Package v1 holds the versioned API contract and the code generated from it.
//
// Why protobuf at all, when Phase 5.2 says "REST over HTTPS, JSON bodies - no
// GraphQL or gRPC needed"?
//
// The wire contract Phase 5.2 asks for is exactly what ships: the CLI and the
// dashboard talk REST/JSON to /api/v1/... . What changes is how that surface is
// *defined*. Each service is written once as a proto with google.api.http
// annotations, and protoc-gen-grpc-gateway generates the REST/JSON layer from
// it. That yields:
//
//   - one definition instead of three (route table, request structs, docs),
//   - an OpenAPI document that cannot drift from the handlers, and
//   - the same generation pipeline the reference orbiter-storage-plugin uses,
//     so the toolchain and conventions carry over unchanged.
//
// gRPC is therefore an internal transport — the gateway dials it over loopback
// — and never a public interface. Phase 5.2's intent (no second public API
// dialect for clients to implement) holds.
//
// Layout:
//
//	api/v1/*.proto          contract, hand-written
//	api/v1/*.pb.go          messages          (protoc-gen-go)
//	api/v1/*_grpc.pb.go     service stubs     (protoc-gen-go-grpc)
//	api/v1/*.pb.gw.go       REST/JSON gateway (protoc-gen-grpc-gateway)
//	api/swagger/OpenAPI/    OpenAPI bundle    (protoc-gen-openapiv2)
//	third_party/google/api/ annotation protos, vendored so protoc needs no
//	                        network access
//
// Generated files are committed, matching the reference project: a clone builds
// with `go build` alone, no protoc required.
//
// Adding a service (a later phase, not now):
//
//  1. Write api/v1/<resource>.proto — plural resource nouns per Phase 4.4,
//     with an `option (google.api.http)` on every rpc so the REST route is part
//     of the contract rather than a separate routing table.
//
//     Note that path parameters are camelCased on the way out: a proto field
//     repo_name declared as {repo_name} in the http option is published as
//     /api/v1/probes/{repoName} in both the gateway and the OpenAPI document.
//     That is protoc-gen-openapiv2 following proto3 JSON naming, and it is the
//     name clients actually see — verified against a generated bundle.
//
//  2. Append its filename to the generate directive below.
//
//  3. Run `make proto`.
//
//  4. Register the server in internal/backend/register.go.
//
// Requires protoc plus protoc-gen-go, protoc-gen-go-grpc,
// protoc-gen-grpc-gateway and protoc-gen-openapiv2 on PATH.
package v1

//go:generate protoc -I . -I ../../third_party --go_out=. --go_opt=paths=source_relative common.proto

// Service generation. Add each new .proto to the file list at the end of the
// command; everything before it stays untouched.
//
//go:generate sh -c "test -z \"$(ls *.proto | grep -v '^common.proto$')\" || protoc -I . -I ../../third_party --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative --openapiv2_out=../swagger/OpenAPI --openapiv2_opt=logtostderr=true --openapiv2_opt=allow_merge=true --openapiv2_opt=merge_file_name=apidocs $(ls *.proto | grep -v '^common.proto$')"
