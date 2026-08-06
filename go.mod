module github.com/coredgeio/deployiq-optimizer

go 1.24.0

toolchain go1.24.5

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0
	google.golang.org/grpc v1.68.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/kr/text v0.2.0 // indirect
	// Test-only dependency of yaml.v3; never compiled into either binary.
	// Held at v1.14.1 because v1.16+ declares go 1.25, which would drag the
	// whole module's language version up for code we do not build.
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/protobuf v1.35.1
)
