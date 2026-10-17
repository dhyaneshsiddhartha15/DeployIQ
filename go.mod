module github.com/dhyaneshsiddhartha15/DeployIQ

go 1.24.0

toolchain go1.24.5

require (
	github.com/BurntSushi/toml v1.4.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0
	go.mongodb.org/mongo-driver/v2 v2.2.3
	google.golang.org/grpc v1.68.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/golang/snappy v1.0.0 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.33.0 // indirect
	golang.org/x/sync v0.11.0 // indirect
)

require (
	github.com/kr/text v0.2.0 // indirect
	// Test-only dependency of yaml.v3; never compiled into either binary.
	// Held at v1.14.1 because v1.16+ declares go 1.25, which would drag the
	// whole module's language version up for code we do not build.
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240903143218-8af14fe29dc1
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1
	google.golang.org/protobuf v1.35.1
)
