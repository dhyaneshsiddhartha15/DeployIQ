// DeployIQ fixture: a module with one dependency, a main package under cmd/,
// and a Dockerfile that runs `go run` inside a full golang image.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := uuid.NewRandom(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, "ok")
	})
	log.Fatal(http.ListenAndServe(*addr, nil))
}
