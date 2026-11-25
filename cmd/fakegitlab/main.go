// Command fakegitlab serves demo GitLab API data for local UI development.
//
//	go run ./cmd/fakegitlab                     # listens on :8929, token "dev-token"
//	GITLAB_URL=http://localhost:8929 GITLAB_TOKEN=dev-token go run ./cmd/gitlab-dashboard
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"gitlab-dashboard/internal/fakegitlab"
)

func main() {
	addr := flag.String("addr", "localhost:8929", "listen address")
	token := flag.String("token", "dev-token", "token clients must send")
	flag.Parse()
	log.Printf("fake GitLab on http://%s (token %q)", *addr, *token)
	log.Fatal(http.ListenAndServe(*addr, fakegitlab.Demo(*token, time.Now())))
}
