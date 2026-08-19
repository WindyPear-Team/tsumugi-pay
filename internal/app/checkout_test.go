package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicCheckoutDisablesCaching(t *testing.T) {
	service := &Service{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/payments/invalid", nil)
	request.SetPathValue("orderNo", strings.Repeat("x", 65))

	service.publicCheckout(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("publicCheckout status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if response.Header().Get("Cache-Control") != "no-store, no-cache, max-age=0, must-revalidate" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("Pragma = %q", response.Header().Get("Pragma"))
	}
}
