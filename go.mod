module github.com/dhyaneshsiddhartha15/DeployIQ

go 1.25.0

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0
	google.golang.org/grpc v1.80.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/kr/text v0.2.0 // indirect
	// Test-only dependency of yaml.v3; never compiled into either binary.
	// Held at v1.14.1 because v1.16+ declares go 1.25, which would drag the
	// whole module's language version up for code we do not build.
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/protobuf v1.36.11
)
