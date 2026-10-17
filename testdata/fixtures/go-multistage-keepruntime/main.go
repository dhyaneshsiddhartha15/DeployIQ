// DeployIQ fixture modelled on a real service: multi-stage build with
// private-module setup, a slim Debian runtime with OS packages, the binary
// copied into /, and a config file the app reads from /opt at startup.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	cfg, err := os.ReadFile("/opt/config.yml")
	if err != nil {
		log.Fatalf("reading config: %v", err)
	}
	if _, err := os.Stat("/usr/bin/ssh"); err != nil {
		log.Fatalf("openssh-client missing from the runtime image: %v", err)
	}
	msg := strings.TrimSpace(strings.TrimPrefix(string(cfg), "message:"))
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(msg)) })
	log.Fatal(http.ListenAndServe(":8080", nil))
}
