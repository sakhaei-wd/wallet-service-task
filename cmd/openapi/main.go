// Command openapi validates the authored OpenAPI YAML and generates canonical JSON.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	input := flag.String("input", "api/openapi.yaml", "path to the authored OpenAPI document")
	output := flag.String("output", "api/openapi.json", "path for the generated JSON document")
	flag.Parse()

	if err := generate(*input, *output); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "generate OpenAPI document: %v\n", err)
		os.Exit(1)
	}
}

func generate(input, output string) error {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(input)
	if err != nil {
		return fmt.Errorf("load %s: %w", input, err)
	}
	if err := document.Validate(context.Background()); err != nil {
		return fmt.Errorf("validate %s: %w", input, err)
	}

	generated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal generated document: %w", err)
	}
	generated = append(generated, '\n')
	if err := os.WriteFile(output, generated, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}
	return nil
}
