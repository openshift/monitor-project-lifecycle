/*
Copyright (c) 2018 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	glog "github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"

	projectv1 "github.com/openshift/api/project/v1"
	routev1 "github.com/openshift/api/route/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/monitor-project-lifecycle/client"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	lastTestDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_duration_seconds",
		Help: "Duration of the last completed test (seconds)",
	})

	testsRun = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_run_total",
		Help: "Number of tests run.",
	})

	testsCompleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_completed_total",
		Help: "Number of tests completed.",
	})

	testsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_failed_total",
			Help: "Number of tests failed.",
		},
		[]string{"stage"},
	)
)

// AppCreateAvailabilityMonitor continuously measures availability of the app
// creation workflow using a predefined template.
type AppCreateAvailabilityMonitor struct {
	Client *client.RESTClients
	Config *Config
	Stop   chan struct{}
}

// Run starts up the availability testing loop.
func (m *AppCreateAvailabilityMonitor) Run() error {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})
	http.Handle("/metrics", prometheus.Handler())

	prometheus.MustRegister(lastTestDuration)
	prometheus.MustRegister(testsRun)
	prometheus.MustRegister(testsCompleted)
	prometheus.MustRegister(testsFailed)

	go http.ListenAndServe(m.Config.ListenAddress, nil)
	glog.V(1).Infof("http server listening on %v", m.Config.ListenAddress)

	// Start testing.
	// TODO: timing/metrics
	wait.Until(func() {
		defer func() {
			if err := m.cleanupWorkspace(); err != nil {
				glog.Errorf("error cleaning up workspace: %v", err)
			}
			glog.V(2).Infof("cleaned up workspace")
		}()

		glog.V(2).Infof("starting a test")

		// Set up the project.
		err := m.setupWorkspace()
		if err != nil {
			glog.Errorf("error setting up workspace: %v", err)
			// try again next interval
			return
		}
		glog.V(2).Infof("created workspace")

		// Run the test.
		start := time.Now()
		err = m.runTest()

		// Measure the results.
		duration := time.Since(start)
		testsRun.Inc()
		glog.V(2).Infof("finished test")

		if err != nil {
			glog.Errorf("error running test: %v", err)
			testsFailed.With(prometheus.Labels{"stage": "test"}).Inc()
		} else {
			testsCompleted.Inc()
			// TODO: switch to prometheus.Timer in client_golang v0.9
			lastTestDuration.Set(float64(int64(duration) / int64(time.Second)))
		}
	}, m.Config.RunInterval.Duration, m.Stop)

	return nil
}

// runTest executes a single test:
//
//   1. Instantiate a template.
//   2. Wait for the route to return HTTP 200.
//
// TODO: track metrics.
func (m *AppCreateAvailabilityMonitor) runTest() error {
	template, err := m.Client.TemplateClient.Templates(m.Config.Template.Namespace).Get(m.Config.Template.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting template %q: %v", m.Config.Template.Name, err)
	}

	// The template parameters for a TemplateInstance come from a secret, but
	// we'll create an empty one since we're fine with all the defaults
	secret, err := m.Client.CoreClient.Secrets(m.Config.Check.Namespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "template-parameters",
		},
		//StringData: templateParams,
		StringData: m.Config.Template.Parameters,
	})
	if err != nil {
		return fmt.Errorf("error creating template parameter secret: %v", err)
	}

	// Create a TemplateInstance object, linking the Template and a reference to
	// the Secret object created above.
	_, err = m.Client.TemplateClient.TemplateInstances(m.Config.Check.Namespace).Create(
		&templatev1.TemplateInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: m.Config.Template.Name,
			},
			Spec: templatev1.TemplateInstanceSpec{
				Template: *template,
				Secret: &corev1.LocalObjectReference{
					Name: secret.Name,
				},
			},
		})
	if err != nil {
		return fmt.Errorf("error instantiating template %q: %v", m.Config.Template.Name, err)
	}

	// Wait for the route to exist.
	glog.V(2).Infof("waiting for route %q to exist", m.Config.Check.Route)
	var route *routev1.Route
	err = wait.Poll(50*time.Millisecond, m.Config.Timeout.TemplateCreation.Duration, wait.ConditionFunc(func() (done bool, err error) {
		r, err := m.Client.RouteClient.Routes(m.Config.Check.Namespace).Get(m.Config.Check.Route, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				return false, nil
			}
			return true, fmt.Errorf("error getting route %q: %v", m.Config.Check.Route, err)
		}
		route = r
		return true, nil
	}))
	if err != nil {
		return fmt.Errorf("timed out waiting for route %q to exist", m.Config.Check.Route)
	}

	// Wait for the route to become responsive.
	glog.V(2).Infof("waiting for good response from %q", m.Config.Check.Route)
	err = wait.Poll(1*time.Second, m.Config.Timeout.TemplateCreation.Duration, wait.ConditionFunc(func() (done bool, err error) {
		client := http.Client{Timeout: 1 * time.Second}
		url := &url.URL{
			Host:   route.Spec.Host,
			Scheme: "http",
		}
		resp, err := client.Get(url.String())
		if err != nil {
			glog.V(3).Infof("received error response from %q: %v", url.String(), err)
			return false, nil
		}
		if e, a := 200, resp.StatusCode; e != a {
			glog.V(3).Infof("expected status code %d from %q, got %d", e, url.String(), a)
			return false, nil
		}
		return true, nil
	}))
	if err != nil {
		return fmt.Errorf("timed out waiting for app to become availabile at %q: %v", m.Config.Check.Route, err)
	}
	glog.V(2).Infof("got success response from route %q", m.Config.Check.Route)
	return nil
}

// setupWorkspace creates a project to hold an individual test's resources.
// TODO: Add prometheus metrics for timing and errors.
func (m *AppCreateAvailabilityMonitor) setupWorkspace() error {
	_, err := m.Client.ProjectClient.ProjectRequests().Create(
		&projectv1.ProjectRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name: m.Config.Check.Namespace,
			},
			DisplayName: m.Config.Check.DisplayName,
		},
	)

	if err != nil {
		return fmt.Errorf("error creating project %q: %v", m.Config.Check.Namespace, err)
	}

	return nil
}

// cleanupWorkspace deletes the project for a test.
// TODO: Add prometheus metrics for timing and errors.
func (m *AppCreateAvailabilityMonitor) cleanupWorkspace() error {
	// Delete the project that contains the kube resources we've created
	err := m.Client.ProjectClient.Projects().Delete(m.Config.Check.Namespace, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("Failed to remove project %v: %v", m.Config.Check.Namespace, err)
	}
	return nil
}
