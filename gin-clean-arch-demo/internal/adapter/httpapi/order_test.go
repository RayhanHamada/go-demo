package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createCustomer posts a user and returns its id.
func createCustomer(t *testing.T, h http.Handler, email string) string {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/v1/users", `{"email":"`+email+`","display_name":"Customer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create customer: status = %d, body = %s", rec.Code, rec.Body)
	}
	return createdID(t, rec)
}

// orderBody builds a one-line order body for customer.
func orderBody(customerID string) string {
	return `{"customer_id":"` + customerID + `","currency":"USD",
		"items":[{"sku":"WIDGET-01","quantity":2,"unit_price_cents":1999}]}`
}

// createOrder posts an order and returns its decoded body plus the ETag.
func createOrder(t *testing.T, h http.Handler, customerID string) (map[string]any, string) {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/v1/orders", orderBody(customerID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order: status = %d, body = %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag not set on create")
	}
	return body, etag
}

func TestOrderLifecycleOverHTTP(t *testing.T) {
	h := newRouter(t, io.Discard)
	customerID := createCustomer(t, h, "order-flow@example.com")
	order, etag := createOrder(t, h, customerID)

	id, _ := order["id"].(string)
	if loc := do(t, h, http.MethodGet, "/v1/orders/"+id, "").Header().Get("Location"); loc != "" {
		t.Errorf("unexpected Location on GET: %q", loc)
	}
	if total, _ := order["total_cents"].(float64); total != 3998 {
		t.Errorf("total_cents = %v, want 3998", order["total_cents"])
	}
	items, _ := order["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	line := items[0].(map[string]any)
	if got, _ := line["line_total_cents"].(float64); got != 3998 {
		t.Errorf("line_total_cents = %v, want 3998", line["line_total_cents"])
	}

	// A GET must expose the same version as an ETag so a client can chain a
	// conditional write.
	rec := do(t, h, http.MethodGet, "/v1/orders/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got != etag {
		t.Errorf("GET ETag = %q, want %q", got, etag)
	}

	// Correct If-Match succeeds.
	rec = doWithHeaders(t, h, http.MethodPost, "/v1/orders/"+id+"/place", "", map[string]string{"If-Match": etag})
	if rec.Code != http.StatusOK {
		t.Fatalf("place status = %d, body = %s", rec.Code, rec.Body)
	}
	var placed struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &placed); err != nil {
		t.Fatalf("decode place: %v", err)
	}
	if placed.Status != "placed" || placed.Version != 2 {
		t.Errorf("placed = %+v, want placed at version 2", placed)
	}
	if next := rec.Header().Get("ETag"); next != `"v2"` {
		t.Errorf("ETag after place = %q, want \"v2\"", next)
	}

	// The stale ETag from before the placement must now be refused.
	rec = doWithHeaders(t, h, http.MethodPost, "/v1/orders/"+id+"/cancel", "", map[string]string{"If-Match": etag})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale cancel status = %d, want 412 (body %s)", rec.Code, rec.Body)
	}

	// Placing again is a state conflict, not a silent success.
	rec = doWithHeaders(t, h, http.MethodPost, "/v1/orders/"+id+"/place", "", map[string]string{"If-Match": `"v2"`})
	if rec.Code != http.StatusConflict {
		t.Errorf("place twice status = %d, want 409", rec.Code)
	}

	// A placed order is a record: it cannot be edited or deleted.
	rec = doWithHeaders(t, h, http.MethodPut, "/v1/orders/"+id+"/items",
		`{"items":[{"sku":"B2","quantity":1,"unit_price_cents":5}]}`, map[string]string{"If-Match": `"v2"`})
	if rec.Code != http.StatusConflict {
		t.Errorf("edit placed status = %d, want 409", rec.Code)
	}
	rec = doWithHeaders(t, h, http.MethodDelete, "/v1/orders/"+id, "", map[string]string{"If-Match": `"v2"`})
	if rec.Code != http.StatusConflict {
		t.Errorf("delete placed status = %d, want 409", rec.Code)
	}

	// Cancelling with the current version works and is terminal.
	rec = doWithHeaders(t, h, http.MethodPost, "/v1/orders/"+id+"/cancel", "", map[string]string{"If-Match": `"v2"`})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", rec.Code, rec.Body)
	}
	rec = doWithHeaders(t, h, http.MethodPost, "/v1/orders/"+id+"/cancel", "", map[string]string{"If-Match": `"v3"`})
	if rec.Code != http.StatusConflict {
		t.Errorf("cancel twice status = %d, want 409", rec.Code)
	}
}

func TestDraftOrderDeleteAndItemReplacement(t *testing.T) {
	h := newRouter(t, io.Discard)
	customerID := createCustomer(t, h, "draft-http@example.com")
	order, etag := createOrder(t, h, customerID)
	id := order["id"].(string)

	rec := doWithHeaders(t, h, http.MethodPut, "/v1/orders/"+id+"/items",
		`{"items":[{"sku":"NEW-1","quantity":4,"unit_price_cents":250}]}`,
		map[string]string{"If-Match": etag})
	if rec.Code != http.StatusOK {
		t.Fatalf("replace items status = %d, body = %s", rec.Code, rec.Body)
	}
	var replaced struct {
		TotalCents int64 `json:"total_cents"`
		Version    int64 `json:"version"`
		Items      []struct {
			SKU string `json:"sku"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &replaced); err != nil {
		t.Fatalf("decode replace: %v", err)
	}
	if replaced.TotalCents != 1000 || replaced.Version != 2 || len(replaced.Items) != 1 || replaced.Items[0].SKU != "NEW-1" {
		t.Errorf("replaced = %+v, want one NEW-1 line totalling 1000 at version 2", replaced)
	}

	if rec := doWithHeaders(t, h, http.MethodDelete, "/v1/orders/"+id, "", map[string]string{"If-Match": `"v2"`}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete draft status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodGet, "/v1/orders/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", rec.Code)
	}
}

func TestOrderErrorMapping(t *testing.T) {
	h := newRouter(t, io.Discard)
	customerID := createCustomer(t, h, "order-errors@example.com")
	order, _ := createOrder(t, h, customerID)
	id := order["id"].(string)

	cases := []struct {
		name    string
		method  string
		path    string
		body    string
		headers map[string]string
		want    int
	}{
		{"malformed json", http.MethodPost, "/v1/orders", `{"items":`, nil, http.StatusBadRequest},
		{"unknown customer", http.MethodPost, "/v1/orders",
			`{"customer_id":"0193f3a0-0000-7000-8000-000000000000","currency":"USD","items":[{"sku":"A1","quantity":1,"unit_price_cents":1}]}`, nil, http.StatusNotFound},
		{"no items", http.MethodPost, "/v1/orders",
			`{"customer_id":"` + customerID + `","currency":"USD","items":[]}`, nil, http.StatusBadRequest},
		{"bad currency", http.MethodPost, "/v1/orders",
			`{"customer_id":"` + customerID + `","currency":"dollars","items":[{"sku":"A1","quantity":1,"unit_price_cents":1}]}`, nil, http.StatusBadRequest},
		{"bad sku", http.MethodPost, "/v1/orders",
			`{"customer_id":"` + customerID + `","currency":"USD","items":[{"sku":" ","quantity":1,"unit_price_cents":1}]}`, nil, http.StatusBadRequest},
		{"quantity out of range", http.MethodPost, "/v1/orders",
			`{"customer_id":"` + customerID + `","currency":"USD","items":[{"sku":"A1","quantity":0,"unit_price_cents":1}]}`, nil, http.StatusBadRequest},
		{"malformed etag", http.MethodPost, "/v1/orders/" + id + "/place", "", map[string]string{"If-Match": "not-a-tag"}, http.StatusBadRequest},
		{"wildcard etag", http.MethodPost, "/v1/orders/" + id + "/place", "", map[string]string{"If-Match": "*"}, http.StatusBadRequest},
		{"bad uuid", http.MethodGet, "/v1/orders/abc", "", nil, http.StatusBadRequest},
		{"unknown order", http.MethodGet, "/v1/orders/0193f3a0-0000-7000-8000-000000000000", "", nil, http.StatusNotFound},
		{"bad status filter", http.MethodGet, "/v1/orders?status=zombie", "", nil, http.StatusBadRequest},
		{"bad customer filter", http.MethodGet, "/v1/orders?customer_id=nope", "", nil, http.StatusBadRequest},
		{"bad limit", http.MethodGet, "/v1/orders?limit=abc", "", nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doWithHeaders(t, h, tc.method, tc.path, tc.body, tc.headers)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// A customer with orders cannot be deleted: the restriction would orphan the
// order, so the API answers 409 instead of a database error.
func TestDeleteCustomerWithOrdersConflicts(t *testing.T) {
	h := newRouter(t, io.Discard)
	customerID := createCustomer(t, h, "has-orders@example.com")
	createOrder(t, h, customerID)

	rec := do(t, h, http.MethodDelete, "/v1/users/"+customerID, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete customer with orders status = %d, want 409 (body %s)", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodGet, "/v1/users/"+customerID, ""); rec.Code != http.StatusOK {
		t.Errorf("customer gone after refused delete: status = %d", rec.Code)
	}
}

// doWithHeaders is do() plus request headers, for precondition tests.
func doWithHeaders(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
