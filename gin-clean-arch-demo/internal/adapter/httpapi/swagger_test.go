package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// swaggerPath is the generated specification. Regenerate with:
//
//	go tool swag init -g cmd/server/main.go -o internal/adapter/httpapi/docs --parseInternal
const swaggerPath = "docs/swagger.json"

type swaggerDoc struct {
	Swagger     string                    `json:"swagger"`
	Info        map[string]any            `json:"info"`
	Paths       map[string]map[string]any `json:"paths"`
	Definitions map[string]map[string]any `json:"definitions"`
}

func readSwagger(t *testing.T) swaggerDoc {
	t.Helper()
	raw, err := os.ReadFile(swaggerPath)
	if err != nil {
		t.Fatalf("read %s (run `go tool swag init ...`): %v", swaggerPath, err)
	}
	var doc swaggerDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", swaggerPath, err)
	}
	return doc
}

// ginPath converts the router's parameter syntax to the documented form.
func ginPath(p string) string {
	return strings.NewReplacer(":id", "{id}", ":any", "{any}", "*any", "{any}").Replace(p)
}

// Documentation is generated from annotations, so a handler can be added
// without annotations and silently vanish from the docs. Every served route
// must be documented, and every documented operation must exist.
func TestSwaggerCoversEveryRoute(t *testing.T) {
	doc := readSwagger(t)

	served := routesOf(t, newRouter(t, io.Discard))
	documented := make(map[string]bool, len(doc.Paths))
	for path, operations := range doc.Paths {
		for method := range operations {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}

	for _, op := range served {
		// The documentation endpoints document the API rather than being part
		// of it, so they are deliberately absent from the specification.
		if strings.HasPrefix(op, "GET /docs/") {
			continue
		}
		if !documented[op] {
			t.Errorf("route %s is served but not documented in %s", op, swaggerPath)
		}
	}
	for op := range documented {
		if !contains(served, op) {
			t.Errorf("operation %s is documented but not served", op)
		}
	}
}

// routesOf lists the router's routes in "METHOD /path" form.
func routesOf(t *testing.T, r *gin.Engine) []string {
	t.Helper()
	out := make([]string, 0, len(r.Routes()))
	for _, route := range r.Routes() {
		out = append(out, route.Method+" "+ginPath(route.Path))
	}
	return out
}

// Swagger UI and the documents it loads must be reachable; a broken path here
// produces a blank page that no handler test would notice.
func TestSwaggerUIAndDocumentsAreServed(t *testing.T) {
	h := newRouter(t, io.Discard)

	cases := []struct {
		path        string
		contentType string
	}{
		{"/docs/index.html", "text/html"},
		{"/docs/doc.json", "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
				t.Errorf("Content-Type = %q, want %s", got, tc.contentType)
			}
		})
	}
}

// The schema a client code-generator sees must describe the JSON the handlers
// actually emit: field names, required properties and enums.
func TestSwaggerSchemasMatchResponses(t *testing.T) {
	doc := readSwagger(t)

	user, ok := doc.Definitions["handlers.UserResponse"]
	if !ok {
		t.Fatalf("UserResponse schema missing; definitions: %v", keysOfDefs(doc.Definitions))
	}
	props := user["properties"].(map[string]any)
	for _, field := range []string{"id", "email", "display_name", "status", "created_at", "updated_at"} {
		if _, ok := props[field]; !ok {
			t.Errorf("UserResponse schema missing property %q", field)
		}
	}

	status := props["status"].(map[string]any)
	enums, _ := status["enum"].([]any)
	if !containsAny(enums, "active") || !containsAny(enums, "suspended") {
		t.Errorf("status enum = %v, want active and suspended", enums)
	}

	// A live response must contain exactly the documented properties, so a
	// renamed JSON tag cannot slip past the spec.
	created := createUser(t, newRouter(t, io.Discard))
	if got := keysOf(created); len(got) != len(props) {
		t.Errorf("response has fields %v, schema documents %v", got, keysOf(props))
	}
	for field := range created {
		if _, ok := props[field]; !ok {
			t.Errorf("response field %q is not documented", field)
		}
	}
}

// The order schema must describe the JSON the handlers emit, and the nested
// line schema must carry the computed totals clients rely on.
func TestSwaggerOrderSchemaMatchesResponses(t *testing.T) {
	doc := readSwagger(t)

	order, ok := doc.Definitions["handlers.OrderResponse"]
	if !ok {
		t.Fatalf("OrderResponse schema missing; definitions: %v", keysOfDefs(doc.Definitions))
	}
	props := order["properties"].(map[string]any)
	for _, field := range []string{"id", "number", "customer_id", "currency", "status", "items", "total_cents", "version", "created_at", "updated_at"} {
		if _, ok := props[field]; !ok {
			t.Errorf("OrderResponse schema missing property %q", field)
		}
	}
	enums, _ := props["status"].(map[string]any)["enum"].([]any)
	for _, want := range []string{"draft", "placed", "cancelled"} {
		if !containsAny(enums, want) {
			t.Errorf("order status enum = %v, missing %q", enums, want)
		}
	}

	line, ok := doc.Definitions["handlers.OrderItemResponse"]
	if !ok {
		t.Fatalf("OrderItemResponse schema missing")
	}
	lineProps := line["properties"].(map[string]any)
	for _, field := range []string{"sku", "quantity", "unit_price_cents", "line_total_cents"} {
		if _, ok := lineProps[field]; !ok {
			t.Errorf("OrderItemResponse schema missing property %q", field)
		}
	}

	// A live response must not carry undocumented fields.
	h := newRouter(t, io.Discard)
	customerID := createCustomer(t, h, "schema-order@example.com")
	created, _ := createOrder(t, h, customerID)
	for field := range created {
		if _, ok := props[field]; !ok {
			t.Errorf("order response field %q is not documented", field)
		}
	}
	if got := keysOf(created); len(got) != len(props) {
		t.Errorf("order response has fields %v, schema documents %v", got, keysOf(props))
	}
}

// createUser POSTs a user and returns the decoded response.
func createUser(t *testing.T, h *gin.Engine) map[string]any {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/v1/users",
		`{"email":"schema@example.com","display_name":"Schema Check"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

// Every documented response reference must resolve; a typo yields a spec that
// generators reject or render as an empty schema.
func TestSwaggerReferencesResolve(t *testing.T) {
	doc := readSwagger(t)

	for path, operations := range doc.Paths {
		for method, raw := range operations {
			op, ok := raw.(map[string]any)
			if !ok {
				t.Errorf("%s %s is not an operation object", method, path)
				continue
			}
			responses, ok := op["responses"].(map[string]any)
			if !ok || len(responses) == 0 {
				t.Errorf("%s %s documents no responses", method, path)
				continue
			}
			walkRefs(t, method+" "+path, responses, doc.Definitions)
		}
	}
}

// walkRefs checks every $ref under node against the definitions table.
func walkRefs(t *testing.T, where string, node any, definitions map[string]map[string]any) {
	t.Helper()
	switch typed := node.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			const prefix = "#/definitions/"
			if !strings.HasPrefix(ref, prefix) {
				t.Errorf("%s: unexpected $ref %q", where, ref)
				return
			}
			if _, ok := definitions[strings.TrimPrefix(ref, prefix)]; !ok {
				t.Errorf("%s: $ref %q does not resolve", where, ref)
			}
		}
		for _, value := range typed {
			walkRefs(t, where, value, definitions)
		}
	case []any:
		for _, value := range typed {
			walkRefs(t, where, value, definitions)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// keysOfDefs lists definition names, for failure messages.
func keysOfDefs(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

func containsAny(items []any, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
