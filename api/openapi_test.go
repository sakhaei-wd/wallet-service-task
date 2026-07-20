package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIDocumentIsValidAndGeneratedOutputIsCurrent(t *testing.T) {
	t.Parallel()
	document := loadDocument(t, openAPIYAML)
	if document.JSONSchemaDialect != "" {
		t.Fatalf("non-default jsonSchemaDialect is not supported by the embedded Swagger UI: %q", document.JSONSchemaDialect)
	}
	generated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	generated = append(generated, '\n')
	if !bytes.Equal(generated, openAPIJSON) {
		t.Fatal("api/openapi.json is stale; run `go generate ./api`")
	}
	loadDocument(t, openAPIJSON)
}

func TestOpenAPIDocumentCoversEveryRouteAndStatus(t *testing.T) {
	t.Parallel()
	document := loadDocument(t, openAPIYAML)
	tests := []struct {
		method   string
		path     string
		statuses []string
	}{
		{method: http.MethodGet, path: "/health/live", statuses: []string{"200", "405"}},
		{method: http.MethodGet, path: "/health/ready", statuses: []string{"200", "405", "503"}},
		{method: http.MethodPost, path: "/v1/wallets", statuses: mutationStatuses()},
		{method: http.MethodGet, path: "/v1/wallets/{walletId}", statuses: readStatuses()},
		{method: http.MethodPost, path: "/v1/wallets/{walletId}/deposits", statuses: walletMutationStatuses()},
		{method: http.MethodPost, path: "/v1/wallets/{walletId}/withdrawals", statuses: walletMutationStatuses()},
		{method: http.MethodPost, path: "/v1/transfers", statuses: walletMutationStatuses()},
		{method: http.MethodGet, path: "/v1/wallets/{walletId}/transactions", statuses: readStatuses()},
		{method: http.MethodGet, path: "/v1/transactions/{transactionId}", statuses: readStatuses()},
		{method: http.MethodGet, path: "/openapi.yaml", statuses: []string{"200", "404", "405"}},
		{method: http.MethodGet, path: "/openapi.json", statuses: []string{"200", "404", "405"}},
		{method: http.MethodGet, path: "/docs", statuses: []string{"307", "404", "405"}},
		{method: http.MethodGet, path: "/docs/", statuses: []string{"200", "404", "405"}},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			pathItem := document.Paths.Find(test.path)
			if pathItem == nil {
				t.Fatalf("route is missing from OpenAPI document")
			}
			operation := pathItem.GetOperation(test.method)
			if operation == nil {
				t.Fatalf("method is missing from OpenAPI document")
			}
			if operation.Summary == "" || operation.Description == "" || operation.OperationID == "" {
				t.Fatalf("operation metadata is incomplete: %+v", operation)
			}
			for _, status := range test.statuses {
				if operation.Responses.Value(status) == nil {
					t.Errorf("status %s is undocumented", status)
				}
			}
		})
	}
}

func loadDocument(t *testing.T, data []byte) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatalf("load OpenAPI document: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI document: %v", err)
	}
	return document
}

func mutationStatuses() []string {
	return []string{"200", "201", "400", "405", "409", "413", "415", "422", "500", "503"}
}

func walletMutationStatuses() []string {
	return append(mutationStatuses(), "403", "404")
}

func readStatuses() []string {
	return []string{"200", "400", "403", "404", "405", "500", "503"}
}
