package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerFallsBackToAppForDashboardRoutes(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/services", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatal("dashboard route did not return the application shell")
	}
}
