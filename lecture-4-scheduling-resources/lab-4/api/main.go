package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const schema = `CREATE TABLE IF NOT EXISTS orders (
	id SERIAL PRIMARY KEY,
	item TEXT NOT NULL,
	processed BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

type order struct {
	ID        int       `json:"id"`
	Item      string    `json:"item"`
	Processed bool      `json:"processed"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "Requests",
	}, []string{"method", "path", "status"})
	latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Request latency",
	}, []string{"path"})

	db atomic.Pointer[sql.DB] // nil, пока не подключились к БД
)

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

func instrument(path string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		h(sw, r)
		requests.WithLabelValues(r.Method, path, strconv.Itoa(sw.code)).Inc()
		latency.WithLabelValues(path).Observe(time.Since(start).Seconds())
		slog.Info("request", "method", r.Method, "path", path, "status", sw.code)
	})
}

// подключаемся к БД в фоне с ретраями: сервис стартует и отвечает на /health,
// даже если postgres ещё не поднялся (или упал)
func connect(dsn string) {
	for {
		d, err := sql.Open("postgres", dsn)
		if err == nil {
			if err = d.Ping(); err == nil {
				if _, err = d.Exec(schema); err == nil {
					d.SetMaxOpenConns(10)
					db.Store(d)
					slog.Info("connected to postgres")
					return
				}
			}
			d.Close()
		}
		slog.Warn("postgres not ready", "err", err)
		time.Sleep(3 * time.Second)
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	prometheus.MustRegister(requests, latency)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is not set")
		os.Exit(1)
	}
	go connect(dsn)

	mux := http.NewServeMux()
	mux.Handle("/health", instrument("/health", func(w http.ResponseWriter, r *http.Request) {
		// специально не зависит от БД: проба не должна рестартовать сервис из-за postgres
		if os.Getenv("HEALTH_FAIL") == "true" {
			http.Error(w, "unhealthy", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	mux.Handle("/order", instrument("/order", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		d := db.Load()
		if d == nil {
			http.Error(w, "db not ready", http.StatusServiceUnavailable)
			return
		}
		var in struct {
			Item string `json:"item"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Item == "" {
			http.Error(w, `body must be {"item":"..."}`, http.StatusBadRequest)
			return
		}
		var id int
		if err := d.QueryRowContext(r.Context(), `INSERT INTO orders(item) VALUES($1) RETURNING id`, in.Item).Scan(&id); err != nil {
			slog.Error("insert order", "err", err)
			http.Error(w, "db error", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": id})
	}))
	mux.Handle("/orders", instrument("/orders", func(w http.ResponseWriter, r *http.Request) {
		d := db.Load()
		if d == nil {
			http.Error(w, "db not ready", http.StatusServiceUnavailable)
			return
		}
		rows, err := d.QueryContext(r.Context(), `SELECT id, item, processed, created_at FROM orders ORDER BY id DESC LIMIT 100`)
		if err != nil {
			slog.Error("select orders", "err", err)
			http.Error(w, "db error", http.StatusServiceUnavailable)
			return
		}
		defer rows.Close()
		out := []order{}
		for rows.Next() {
			var o order
			if err := rows.Scan(&o.ID, &o.Item, &o.Processed, &o.CreatedAt); err != nil {
				http.Error(w, "db error", http.StatusServiceUnavailable)
				return
			}
			out = append(out, o)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}))
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{Addr: ":8080", Handler: mux}

	// аккуратное завершение по SIGTERM: при rolling update запросы в полёте не обрываются
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
		<-stop
		// kubelet шлёт SIGTERM одновременно с удалением пода из endpoints, а kube-proxy
		// узнаёт об этом с задержкой: ещё пару секунд приходят новые запросы
		time.Sleep(5 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	slog.Info("listening", "addr", ":8080")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
