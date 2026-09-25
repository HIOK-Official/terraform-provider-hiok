// Command mockhiok serves the in-memory HIOK API mock for local demos:
//
//	go run ./internal/mockapi/cmd/mockhiok -addr 127.0.0.1:18080
//	export HIOK_ENDPOINT=http://127.0.0.1:18080 HIOK_TOKEN=dev
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/mockapi"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	hidden := flag.Int("hidden-reads", 2, "list calls a new/deleted resource stays invisible/visible (simulates async provisioning)")
	failCreate := flag.String("fail-create", "", "answer every create with 200 {\"success\":false,\"message\":<this>}")
	flag.Parse()

	srv := mockapi.New(mockapi.Options{HiddenReads: *hidden, FailCreate: *failCreate})
	h := srv.Handler()
	log.Printf("mock HIOK API on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/token") {
			log.Printf("%s %s", r.Method, r.URL.RequestURI())
		}
		h.ServeHTTP(w, r)
	})))
}
