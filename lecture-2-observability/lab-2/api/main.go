package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var (
	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "Requests",
	}, []string{"method", "path", "status"})
	errorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total", Help: "5xx responses",
	}, []string{"path"})
	latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Request latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"path"})

	tracer = otel.Tracer("api")
)

// логгер с trace_id текущего спана
func logger(ctx context.Context) *slog.Logger {
	l := slog.Default()
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		l = l.With("trace_id", sc.TraceID().String())
	}
	return l
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

// RED-метрики и лог на каждый запрос
func instrument(path string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		h(sw, r)
		requests.WithLabelValues(r.Method, path, strconv.Itoa(sw.code)).Inc()
		latency.WithLabelValues(path).Observe(time.Since(start).Seconds())
		if sw.code >= 500 {
			errorsTotal.WithLabelValues(path).Inc()
		}
		logger(r.Context()).Info("request", "method", r.Method, "path", path, "status", sw.code)
	})
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	prometheus.MustRegister(requests, errorsTotal, latency)

	// endpoint берётся из OTEL_EXPORTER_OTLP_ENDPOINT
	exp, err := otlptracehttp.New(context.Background())
	if err != nil {
		slog.Error("otlp exporter", "err", err)
		os.Exit(1)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("api"))),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("api")

	mux := http.NewServeMux()
	mux.Handle("/health", instrument("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	mux.Handle("/fail", instrument("/fail", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetStatus(codes.Error, "intentional failure")
		logger(r.Context()).Error("intentional failure on /fail")
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	mux.Handle("/slow", instrument("/slow", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "slow-op")
		d := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
		span.SetAttributes(attribute.Int64("sleep_ms", d.Milliseconds()))
		time.Sleep(d)
		span.End()
		fmt.Fprint(w, "slow done")
	}))
	mux.Handle("/load", instrument("/load", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 {
			n = 50
		}
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if resp, err := http.Get("http://127.0.0.1:8080/health"); err == nil {
					resp.Body.Close()
				}
			}()
		}
		wg.Wait()
		fmt.Fprintf(w, "sent %d requests", n)
	}))

	root := http.NewServeMux()
	root.Handle("/metrics", promhttp.Handler())
	// трейсим всё, кроме /metrics (иначе скрейп засоряет Jaeger)
	root.Handle("/", otelhttp.NewHandler(mux, "http.request"))

	slog.Info("listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", root); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
