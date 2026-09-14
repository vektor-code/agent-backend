// Package centralauth attaches the shared agent→API ingest token to outbound
// HTTP requests. Token is read from CRNET_INGEST_TOKEN, INGEST_TOKEN, or CENTRAL_TOKEN.
package centralauth

import (
	"net/http"
	"os"
	"strings"
)

const Header = "X-CRNET-Ingest-Token"

// Token returns the configured ingest/control token, or empty.
func Token() string {
	for _, key := range []string{"CRNET_INGEST_TOKEN", "INGEST_TOKEN", "CENTRAL_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// Apply sets Authorization Bearer and X-CRNET-Ingest-Token when a token is configured.
func Apply(req *http.Request) {
	if req == nil {
		return
	}
	tok := Token()
	if tok == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(Header, tok)
}
