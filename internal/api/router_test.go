package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xKimutai/double-entry-ledger/internal/health"
	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
	"github.com/0xKimutai/double-entry-ledger/internal/observability"
	"go.uber.org/zap"
)

type serviceStub struct {
	ledger.Repository
	account ledger.Account
}

func (s *serviceStub) CreateAccount(_ context.Context, name, currency string) (ledger.Account, error) {
	s.account = ledger.Account{Name: name, Currency: currency, BalanceMinor: 0}
	return s.account, nil
}

type readinessStub struct {
	components map[string]health.Component
}

func uintPointer(value uint) *uint {
	return &value
}

func (s readinessStub) Check(context.Context) map[string]health.Component {
	return s.components
}

func newTestRouter(checker readinessStub) http.Handler {
	return NewRouter(
		ledger.NewService(&serviceStub{}),
		zap.NewNop(),
		checker,
		observability.NewMetrics(nil),
		time.Now(),
	)
}

func TestCreateAccount(t *testing.T) {
	router := newTestRouter(readinessStub{})
	request := httptest.NewRequest(http.MethodPost, "/v1/accounts",
		strings.NewReader(`{"name":"wallet","currency":"kes"}`))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}
	var account ledger.Account
	if err := json.Unmarshal(response.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.Currency != "KES" || account.BalanceMinor != 0 {
		t.Fatalf("account = %+v, want normalized zero-balance account", account)
	}
}

func TestCreateAccountRejectsUnknownFields(t *testing.T) {
	router := newTestRouter(readinessStub{})
	request := httptest.NewRequest(http.MethodPost, "/v1/accounts",
		strings.NewReader(`{"name":"wallet","currency":"KES","owner_id":"user"}`))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestHealthEndpointsReportReadiness(t *testing.T) {
	router := newTestRouter(readinessStub{components: map[string]health.Component{
		"database":   {Status: "healthy", LatencyMS: int64Pointer(2)},
		"migrations": {Status: "unhealthy", Version: uintPointer(1), ExpectedVersion: uintPointer(2), Error: "database schema is not up to date"},
	}})

	liveness := httptest.NewRecorder()
	router.ServeHTTP(liveness, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if liveness.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d", liveness.Code, http.StatusOK)
	}

	readiness := httptest.NewRecorder()
	router.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", readiness.Code, http.StatusServiceUnavailable)
	}
	var report health.Report
	if err := json.Unmarshal(readiness.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "unhealthy" || report.Components["migrations"].ExpectedVersion == nil || *report.Components["migrations"].ExpectedVersion != 2 {
		t.Fatalf("readiness report = %+v", report)
	}
	if report.Components["api"].Status != "healthy" || report.Components["database"].Status != "healthy" {
		t.Fatalf("readiness components = %+v", report.Components)
	}

}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestMetricsEndpointExposesHTTPMetrics(t *testing.T) {
	router := newTestRouter(readinessStub{})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `ledger_http_requests_total{method="GET",route="/healthz",status="OK"} 1`) {
		t.Fatalf("metrics response missing health request counter:\n%s", response.Body.String())
	}
}

func TestRegisteredRoutes(t *testing.T) {
	routes, err := RegisteredRoutes()
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		got[fmt.Sprintf("%s %s", route.Method, route.Pattern)] = struct{}{}
	}
	want := []string{
		"GET /healthz",
		"GET /metrics",
		"GET /readyz",
		"GET /v1/accounts/{accountID}",
		"GET /v1/accounts/{accountID}/entries",
		"POST /v1/accounts",
		"POST /v1/accounts/{accountID}/deposits",
		"POST /v1/payments",
	}
	if len(routes) != len(want) {
		t.Fatalf("registered route count = %d, want %d: %+v", len(routes), len(want), routes)
	}
	for _, route := range want {
		if _, ok := got[route]; !ok {
			t.Errorf("missing route %q", route)
		}
	}
}
