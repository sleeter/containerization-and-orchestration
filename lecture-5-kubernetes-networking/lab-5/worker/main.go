package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

var (
	processedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worker_orders_processed_total", Help: "Orders marked as processed",
	})
	errorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worker_errors_total", Help: "Failed processing iterations",
	})
	pending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "worker_orders_pending", Help: "Orders waiting for processing",
	})

	db atomic.Pointer[sql.DB]
)

// подключаемся к БД в фоне с ретраями: /health отвечает и без postgres
func connect(dsn string) {
	for {
		d, err := sql.Open("postgres", dsn)
		if err == nil {
			if err = d.Ping(); err == nil {
				if _, err = d.Exec(schema); err == nil {
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

// берёт пачку необработанных заказов и помечает их обработанными.
// FOR UPDATE SKIP LOCKED: две реплики воркера не хватают один и тот же заказ
func processBatch(ctx context.Context, d *sql.DB) (int, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM orders WHERE NOT processed ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	for _, id := range ids {
		time.Sleep(200 * time.Millisecond) // имитация работы над заказом
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET processed = TRUE WHERE id = $1`, id); err != nil {
			return 0, err
		}
		slog.Info("order processed", "id", id)
	}
	return len(ids), tx.Commit()
}

func loop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		d := db.Load()
		if d == nil {
			continue
		}
		n, err := processBatch(ctx, d)
		if err != nil {
			errorsTotal.Inc()
			slog.Error("process batch", "err", err)
			continue
		}
		processedTotal.Add(float64(n))

		var left int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE NOT processed`).Scan(&left); err == nil {
			pending.Set(float64(left))
		}
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	prometheus.MustRegister(processedTotal, errorsTotal, pending)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is not set")
		os.Exit(1)
	}
	go connect(dsn)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go loop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	slog.Info("listening", "addr", ":8080")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
