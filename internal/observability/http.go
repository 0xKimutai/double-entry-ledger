package observability

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func HTTPMiddleware(logger *zap.Logger, metrics *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			writer := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(writer, r)

			status := writer.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := "unmatched"
			if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
				if pattern := routeContext.RoutePattern(); pattern != "" {
					route = pattern
				}
			}
			if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
				span.SetName(r.Method + " " + route)
				span.SetAttributes(attribute.String("http.route", route))
			}
			duration := time.Since(startedAt)
			metrics.Observe(r.Method, route, status, writer.BytesWritten(), duration.Seconds())

			fields := []zap.Field{
				zap.String("method", r.Method),
				zap.String("route", route),
				zap.Int("status", status),
				zap.Int("response_bytes", writer.BytesWritten()),
				zap.Duration("duration", duration),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			}
			if spanContext := trace.SpanFromContext(r.Context()).SpanContext(); spanContext.IsValid() {
				fields = append(fields,
					zap.String("trace_id", spanContext.TraceID().String()),
					zap.String("span_id", spanContext.SpanID().String()),
				)
			}
			message := "HTTP request completed"
			switch {
			case status >= http.StatusInternalServerError:
				logger.Error(message, fields...)
			case status >= http.StatusBadRequest:
				logger.Warn(message, fields...)
			default:
				logger.Info(message, fields...)
			}
		})
	}
}
