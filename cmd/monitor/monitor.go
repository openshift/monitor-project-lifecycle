package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()

	http.HandleFunc("/healthz", handleHealthz)
	http.Handle("/metrics", prometheus.Handler())

	appCreateLatency := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "monitor_project_lifecycle",
			Name:      "app_create_latency",
			Help:      "App creation step latency (milliseconds)",
		},
		[]string{
			"step",
		},
	)
	prometheus.MustRegister(appCreateLatency)

	go http.ListenAndServe(*addr, nil)

	go runAppCreateSim(appCreateLatency, 100*time.Millisecond)

	select {}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func runAppCreateSim(gauge *prometheus.GaugeVec, interval time.Duration) {
	steps := map[string]struct {
		min time.Duration
		max time.Duration
	}{
		"new-app": {min: 1 * time.Second, max: 5 * time.Second},
		"build":   {min: 1 * time.Minute, max: 4 * time.Minute},
		"deploy":  {min: 1 * time.Minute, max: 5 * time.Minute},
		"expose":  {min: 10 * time.Second, max: 1 * time.Minute},
	}
	for {
		for step, r := range steps {
			latency := rand.Int63n(int64(r.max)-int64(r.min)) + int64(r.min)
			gauge.With(prometheus.Labels{"step": step}).Set(float64(latency / int64(time.Millisecond)))
		}
		time.Sleep(interval)
	}
}
