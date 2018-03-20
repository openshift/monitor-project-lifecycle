package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	projectv1client "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()

	http.HandleFunc("/healthz", handleHealthz)
	http.Handle("/metrics", prometheus.Handler())

	appCreateLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "app_create_latency_seconds",
			Help:    "The latency of various app creation steps.",
			Buckets: []float64{1, 10, 60, 3 * 60, 5 * 60},
		},
		[]string{"step"},
	)
	prometheus.MustRegister(appCreateLatency)

	go http.ListenAndServe(*addr, nil)

	go runAppCreateSim(appCreateLatency, 1*time.Second)

	select {}
}

// Creates a rest config object that is used for other client calls.
// TODO: we should probably not panic, instead expose this as an error in prometheus.
func getRestConfig() *restclient.Config {
	// Instantiate loader for kubeconfig file.
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	// Get a rest.Config from the kubeconfig file.  This will be passed into all
	// the client objects we create.
	restconfig, err := kubeconfig.ClientConfig()
	if err != nil {
		panic(err)
	}

	return restconfig
}

// Cleans up all objects that this monitoring command creates
// TODO: Add prometheus metrics for timing and errors.
// TODO: Don't panic as that would kill the prometheus end point as well.
func cleanupWorkspace(project string, restconfig *restclient.Config) {
	// Create an OpenShift project/v1 client.
	projectclient, err := projectv1client.NewForConfig(restconfig)
	if err != nil {
		panic(err)
	}

	// Delete the project that contains the kube resources we've created
	err = projectclient.Projects().Delete(project, &metav1.DeleteOptions{})
	if err != nil {
		panic(err)
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func runAppCreateSim(metric *prometheus.HistogramVec, interval time.Duration) {
	steps := map[string]struct {
		min time.Duration
		max time.Duration
	}{
		"new-app": {min: 1 * time.Second, max: 5 * time.Second},
		"build":   {min: 1 * time.Minute, max: 5 * time.Minute},
		"deploy":  {min: 1 * time.Minute, max: 5 * time.Minute},
		"expose":  {min: 10 * time.Second, max: 1 * time.Minute},
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		for step, r := range steps {
			latency := rng.Int63n(int64(r.max)-int64(r.min)) + int64(r.min)
			metric.With(prometheus.Labels{"step": step}).Observe(float64(latency / int64(time.Second)))
		}
		time.Sleep(interval)
	}
}
