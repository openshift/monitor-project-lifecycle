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
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	prefix = "monitor_app_create_"

	nsAnnotationKey   = "openshift.io/monitoring-availability-test"
	nsAnnotationValue = "monitor-app-create"
	nsLabelKey        = "openshift-monitoring-test"
	nsLabelValue      = "monitor-app-create"

	lastTestDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "test_duration_seconds",
		Help: "Duration of the last completed test (seconds)",
	})

	testsRun = prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "test_run_total",
		Help: "Number of tests run.",
	})

	testsCompleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "test_completed_total",
		Help: "Number of tests completed.",
	})

	testsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "test_failed_total",
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
	// TODO: Add a backoff instead of using wait.Until.
	wait.Until(func() {
		glog.V(2).Infof("starting a test")

		errs := m.deleteMonitorProjects()
		if errs != nil {
			glog.Errorf("failed to clean up projects prior to test: %v", errs)
			return
		}

		// Set up the project.
		namespace, err := m.createProject()
		if err != nil {
			glog.Errorf("error creating project %q: %v", namespace, err)
			// try again next interval
			return
		}
		glog.V(2).Infof("created project %q", namespace)

		defer func(project string) {
			errs := m.deleteMonitorProjects()
			if errs != nil {
				glog.Errorf("Failed to clean up project %q following test: %v", project, errs)
			} else {
				glog.V(2).Infof("Project %q cleaned up", project)
			}
		}(namespace)

		// Run the test.
		start := time.Now()
		err = m.runTest(namespace)

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
func (m *AppCreateAvailabilityMonitor) runTest(namespace string) error {
	template, err := m.Client.TemplateClient.Templates(m.Config.Template.Namespace).Get(m.Config.Template.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting template %q: %v", m.Config.Template.Name, err)
	}

	// The template parameters for a TemplateInstance come from a secret, but
	// we'll create an empty one since we're fine with all the defaults
	secret, err := m.Client.CoreClient.Secrets(namespace).Create(&corev1.Secret{
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
	_, err = m.Client.TemplateClient.TemplateInstances(namespace).Create(
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
	routeName := m.Config.Template.AvailabilityRoute
	glog.V(2).Infof("waiting for route %q to exist", routeName)
	var route *routev1.Route
	err = wait.Poll(50*time.Millisecond, m.Config.AvailabilityTimeout.Duration, wait.ConditionFunc(func() (done bool, err error) {
		r, err := m.Client.RouteClient.Routes(namespace).Get(routeName, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				return false, nil
			}
			return true, fmt.Errorf("error getting route %q: %v", routeName, err)
		}
		route = r
		return true, nil
	}))
	if err != nil {
		return fmt.Errorf("timed out waiting for route %q to be created", routeName)
	}

	// Wait for the route to become responsive.
	url := &url.URL{
		Host:   route.Spec.Host,
		Scheme: "http",
	}
	glog.V(2).Infof("waiting for good response from %s", url.String())
	err = wait.Poll(1*time.Second, m.Config.AvailabilityTimeout.Duration, wait.ConditionFunc(func() (done bool, err error) {
		client := http.Client{Timeout: 1 * time.Second}
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
		return fmt.Errorf("timed out waiting for app route %q to become availabile at %q: %v", routeName, url.String(), err)
	}
	glog.V(2).Infof("got success response from %s", url.String())
	return nil
}

// createProject creates a project to hold an individual test's resources.
// Returns the generated name of the project.
// TODO: Add prometheus metrics for timing and errors.
func (m *AppCreateAvailabilityMonitor) createProject() (string, error) {
	name := "monitor-app-create-" + utilrand.String(4)
	_, err := m.Client.ProjectClient.ProjectRequests().Create(
		&projectv1.ProjectRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			DisplayName: "A temporary test project created by monitor-project-lifecycle app.",
		},
	)
	if err != nil {
		return "", fmt.Errorf("error creating project: %v", err)
	}

	// Add monitoring annotation and label to the namespace
	ns, err := m.Client.CoreClient.Namespaces().Get(name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("error getting namespace: %v", err)
	}

	// TODO: Get decision from deads or jliggit what pattern we should use for the annotation name.
	//       More info:  https://github.com/openshift/monitor-project-lifecycle/issues/24
	ns.Annotations[nsAnnotationKey] = nsAnnotationValue

	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}

	ns.Labels[nsLabelKey] = nsLabelValue

	ns, err = m.Client.CoreClient.Namespaces().Update(ns)
	if err != nil {
		return "", fmt.Errorf("error updating namespace: %v", err)
	}

	return name, nil
}

// deleteMonitorProjects deletes all test apps projects created by this app.
// TODO: Add prometheus metrics for timing and errors.
func (m *AppCreateAvailabilityMonitor) deleteMonitorProjects() error {
	errors := make([]error, 0)

	projects, err := m.Client.ProjectClient.Projects().List(metav1.ListOptions{
		LabelSelector: nsLabelKey + "=" + nsLabelValue,
	})
	if err != nil {
		return utilerrors.NewAggregate(append(errors, fmt.Errorf("error getting monitoring projects: %v", err)))
	}

	for _, project := range projects.Items {
		// Before deleting the project, check for our annotation which ensures we created this project.
		if val, ok := project.Annotations[nsAnnotationKey]; ok && val == nsAnnotationValue {
			glog.V(2).Infof("Deleting monitoring project: %v", project.Name)
			err := m.Client.ProjectClient.Projects().Delete(project.Name, &metav1.DeleteOptions{})
			if err != nil {
				errors = append(errors, fmt.Errorf("error deleting monitoring project %q: %v", project.Name, err))
			}
		}
	}

	return utilerrors.NewAggregate(errors)
}
