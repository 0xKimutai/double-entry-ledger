package api

import (
	"net/http"
	"time"

	"github.com/0xKimutai/double-entry-ledger/internal/health"
	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
	"github.com/0xKimutai/double-entry-ledger/internal/observability"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
)

type handler struct {
	ledger    *ledger.Service
	logger    *zap.Logger
	readiness health.Checker
	metrics   *observability.Metrics
	startedAt time.Time
}

type Route struct {
	Method  string
	Pattern string
}

func NewRouter(service *ledger.Service, logger *zap.Logger, readiness health.Checker, metrics *observability.Metrics, startedAt time.Time) http.Handler {
	h := &handler{
		ledger:    service,
		logger:    logger,
		readiness: readiness,
		metrics:   metrics,
		startedAt: startedAt,
	}
	router := newChiRouter(h, logger, metrics, metrics.Handler())
	return otelhttp.NewHandler(router, "ledger-api")
}

func RegisteredRoutes() ([]Route, error) {
	metrics := observability.NewMetrics(nil)
	router := newChiRouter(&handler{}, zap.NewNop(), metrics, http.NotFoundHandler())
	routes := make([]Route, 0)
	err := chi.Walk(router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, Route{Method: method, Pattern: pattern})
		return nil
	})
	return routes, err
}

func newChiRouter(h *handler, logger *zap.Logger, metrics *observability.Metrics, metricsHandler http.Handler) *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(observability.HTTPMiddleware(logger, metrics))
	router.Use(middleware.Recoverer)
	router.Method(http.MethodGet, "/metrics", metricsHandler)
	router.Get("/healthz", h.health)
	router.Get("/readyz", h.ready)
	router.Route("/v1", func(router chi.Router) {
		router.Post("/accounts", h.createAccount)
		router.Get("/accounts/{accountID}", h.getAccount)
		router.Get("/accounts/{accountID}/entries", h.listEntries)
		router.Post("/accounts/{accountID}/deposits", h.deposit)
		router.Post("/payments", h.transfer)
	})
	return router
}
