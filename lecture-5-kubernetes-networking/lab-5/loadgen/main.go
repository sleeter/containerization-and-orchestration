package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "loadgen_requests_total", Help: "Client requests by result",
	}, []string{"result"})
	latency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "loadgen_request_duration_seconds", Help: "Client-side latency",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2},
	})
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	prometheus.MustRegister(requests, latency)

	target := os.Getenv("TARGET")
	if target == "" {
		target = "http://api:8080/orders"
	}
	rate, _ := strconv.Atoi(os.Getenv("RATE"))
	if rate <= 0 {
		rate = 20
	}

	// без keep-alive: каждый запрос открывает новое соединение, как отдельный клиент,
	// иначе он прилипнет к одному поду и не будет видно ни балансировки, ни обрыва на смерти пода
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	go func() {
		t := time.NewTicker(time.Second / time.Duration(rate))
		for range t.C {
			go func() {
				start := time.Now()
				resp, err := client.Get(target)
				if err != nil {
					requests.WithLabelValues("fail").Inc()
					slog.Warn("request failed", "target", target, "err", err.Error())
					return
				}
				resp.Body.Close()
				latency.Observe(time.Since(start).Seconds())
				if resp.StatusCode >= 500 {
					requests.WithLabelValues("http_5xx").Inc()
					slog.Warn("bad status", "status", resp.StatusCode)
					return
				}
				requests.WithLabelValues("ok").Inc()
			}()
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	slog.Info("loadgen started", "target", target, "rate", rate)
	if err := http.ListenAndServe(":8080", nil); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
