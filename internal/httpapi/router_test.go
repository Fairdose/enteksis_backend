package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Fairdose/enteksis_backend/internal/service"
)

type stubStore struct {
	created service.CreatedRequest
	err     error
	request service.Request
}

func (store *stubStore) CreateRequest(_ context.Context, request service.Request) (service.CreatedRequest, error) {
	store.request = request
	return store.created, store.err
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	NewRouter(Dependencies{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if body := recorder.Body.String(); body != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected response body %q", body)
	}
}

func TestCreateRequestStoresValidatedPayload(t *testing.T) {
	createdAt := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	store := &stubStore{created: service.CreatedRequest{ID: "request-id", CreatedAt: createdAt}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/requests", strings.NewReader(`{
		"name":"  Ada Lovelace  ",
		"email":"ADA@EXAMPLE.COM",
		"serviceType":"web-design",
		"description":"  Erişilebilir bir web sitesi istiyorum.  "
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	NewRouter(Dependencies{RequestStore: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}
	if store.request.Name != "Ada Lovelace" || store.request.Email != "ada@example.com" {
		t.Fatalf("payload was not normalized: %#v", store.request)
	}
}

func TestCreateRequestRejectsInvalidFields(t *testing.T) {
	store := &stubStore{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/requests", strings.NewReader(`{
		"name":"A",
		"email":"invalid",
		"serviceType":"unknown",
		"description":"short"
	}`))
	recorder := httptest.NewRecorder()

	NewRouter(Dependencies{RequestStore: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status %d, got %d", http.StatusUnprocessableEntity, recorder.Code)
	}
	for _, field := range []string{"name", "email", "serviceType", "description"} {
		if !strings.Contains(recorder.Body.String(), `"`+field+`"`) {
			t.Errorf("response does not contain %s validation error: %s", field, recorder.Body.String())
		}
	}
}

func TestCreateRequestDoesNotReportSuccessWhenStorageFails(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/requests", strings.NewReader(`{
		"name":"Ada Lovelace",
		"email":"ada@example.com",
		"serviceType":"software-development",
		"description":"Yeni bir ürün geliştirmek istiyorum."
	}`))
	recorder := httptest.NewRecorder()

	NewRouter(Dependencies{RequestStore: &stubStore{err: errors.New("database unavailable")}}).
		ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, recorder.Code)
	}
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/requests", nil)
	request.Header.Set("Origin", "https://fairdose.github.io")
	recorder := httptest.NewRecorder()

	NewRouter(Dependencies{AllowedOrigins: []string{"https://fairdose.github.io"}}).
		ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "https://fairdose.github.io" {
		t.Fatalf("unexpected allowed origin %q", origin)
	}
}
