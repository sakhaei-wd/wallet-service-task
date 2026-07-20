// Package api embeds and serves the Wallet Service OpenAPI contract.
package api

import (
	_ "embed"
	"net/http"

	"github.com/swaggest/swgui/v5emb"
)

//go:generate go run ../cmd/openapi -input openapi.yaml -output openapi.json

//go:embed openapi.yaml
var openAPIYAML []byte

//go:embed openapi.json
var openAPIJSON []byte

// Source returns an HTTP handler for the authored OpenAPI YAML document.
func Source() http.Handler {
	return documentHandler("application/yaml; charset=utf-8", openAPIYAML)
}

// Generated returns an HTTP handler for the generated OpenAPI JSON document.
func Generated() http.Handler {
	return documentHandler("application/json; charset=utf-8", openAPIJSON)
}

// SwaggerUI returns a self-contained Swagger UI handler with embedded assets.
func SwaggerUI() http.Handler {
	return v5emb.New("Wallet Service API", "/openapi.json", "/docs/")
}

func documentHandler(contentType string, document []byte) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", contentType)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(document)
	})
}
