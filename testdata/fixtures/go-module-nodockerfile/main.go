// DeployIQ fixture: a Go module with no Dockerfile that reads a template from
// disk at runtime — the case a binary-only image silently breaks.
package main

import (
	"html/template"
	"log"
	"net/http"
	"os"
)

func main() {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = tmpl.Execute(w, "ok")
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
