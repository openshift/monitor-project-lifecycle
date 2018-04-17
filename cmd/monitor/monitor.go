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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/ghodss/yaml"
	glog "github.com/golang/glog"
	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/monitor-project-lifecycle/client"
	"github.com/openshift/monitor-project-lifecycle/stepwatcher"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// Config This is the structure for the config file we read in.
type Config struct {
	ListenAddress string `json:"listenAddress"`
	Check         struct {
		Namespace   string `json:"namespace"`
		DisplayName string `json:"displayName"`
		URL         string `json:"url"`
	} `json:"check"`
	RunInterval Duration `json:"runInterval"`
	Timeout     struct {
		TemplateCreation Duration `json:"templateCreation"`
		TemplateDeletion Duration `json:"templateDeletion"`
	} `json:"timeout"`
	Template struct {
		Name       string            `json:"name"`
		Namespace  string            `json:"namespace"`
		Parameters map[string]string `json:"parameters"`
	} `json:"template"`
}

// RunOptions represent command line flags.
type RunOptions struct {
	ConfigFile string
}

func NewRunCommand() *cobra.Command {
	options := &RunOptions{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Starts the monitor app.",
		Long:  "Starts the monitor app.",
		Run: func(c *cobra.Command, args []string) {
			run(options)
		},
	}

	flags := cmd.Flags()
	// This command only supports reading from config
	flags.StringVar(&options.ConfigFile, "config", "", "Location of the configuration file to run from.")
	cmd.MarkFlagFilename("config", "yaml", "yml")
	cmd.MarkFlagRequired("config")

	return cmd
}

func run(options *RunOptions) {
	config, err := readConfig(options.ConfigFile)
	if err != nil {
		glog.Fatalf("Fatal error: %v\n", err)
	}

	configStr, _ := json.Marshal(config)
	glog.Infof("starting monitor app with config: %s", configStr)

	// before we bring everything up, we need to make sure that we have
	// a working configuration to connect to the API server
	restconfig, err := client.GetRestConfig()
	if err != nil {
		glog.Fatalf("Fatal error: %v\n", err)
	}

	clients, err := client.MakeRESTClients(restconfig)
	if err != nil {
		glog.Fatalf("Fatal error: %v\n", err)
	}

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

	go http.ListenAndServe(config.ListenAddress, nil)

	go runAppCreateSim(appCreateLatency, 1*time.Second)

	cleanupWorkspace(config, clients)

	interval := config.RunInterval.Duration
	for {
		timeoutTime := time.Now().Add(config.Timeout.TemplateCreation.Duration)
		doneCh := make(chan stepwatcher.CompleteStatus, 1)
		go func() {
			// TODO: Decide if we should keep this URL a configuration option, derive it from the template or fetch it from the created route object?
			// TODO: Decide if config.Check.URL should be a list and if so, how to handle checking multiple URLs at the same time (multiple go routines,
			//       hand multiple URLs to pollURL, etc).
			// pollURL will block until success or timeout
			if pollURL(config.Check.URL, timeoutTime) {
				doneCh <- stepwatcher.CompletedSuccess
			} else {
				doneCh <- stepwatcher.CompletedTimeout
			}
		}()

		startTime := time.Now()
		_, err = setupWorkspace(config, clients)
		if err != nil {
			glog.Errorf("Failed to create project: %v\n", err)
		} else {
			if err = newApp(config, clients, doneCh); err != nil {
				glog.Errorf("Failed to create app: %v\n", err)
			}
			if err = delApp(config, clients); err != nil {
				glog.Errorf("Failed to remove app: %v\n", err)
			}
		}
		if err = cleanupWorkspace(config, clients); err != nil {
			glog.Errorf("Failed to remove project: %v\n", err)
		}
		// sleep a minimum of 30 seconds between runs
		toSleep := 30 * time.Second
		if elapsed := time.Since(startTime); interval-elapsed > toSleep {
			toSleep = interval - elapsed
		}
		glog.Infof("Sleeping for %v before next iteration\n", toSleep)
		time.Sleep(toSleep)
	}
}

// Sets up the workspace that this monitoring command will use.
// TODO: Add prometheus metrics for timing and errors.
func setupWorkspace(config *Config, clients client.RESTClients) (*projectv1.Project, error) {
	// Delete the project that contains the kube resources we've created
	//err = projectclient.Projects().Create(project, &metav1.DeleteOptions{})

	prj, err := clients.ProjectClient.ProjectRequests().Create(
		&projectv1.ProjectRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name: config.Check.Namespace,
			},
			DisplayName: config.Check.DisplayName,
		},
	)

	if err != nil {
		return nil, fmt.Errorf("Failed to create project %v: %v", config.Check.Namespace, err)
	}

	return prj, nil
}

// Cleans up all objects that this monitoring command creates
// TODO: Add prometheus metrics for timing and errors.
func cleanupWorkspace(config *Config, clients client.RESTClients) error {
	// Delete the project that contains the kube resources we've created
	err := clients.ProjectClient.Projects().Delete(config.Check.Namespace, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("Failed to remove project %v: %v", config.Check.Namespace, err)
	}
	return nil
}

// pollURL will continually make HTTP GET requests of url until it returns status code 200 or times out
// pollURL returns true if the request (eventually) succeeded with status code 200, and false if it timed out
func pollURL(url string, timeoutTime time.Time) bool {
	for timeout := timeoutTime.Sub(time.Now()); timeout > 0; timeout = timeoutTime.Sub(time.Now()) {
		client := http.Client{Timeout: timeout}
		resp, err := client.Get(url)
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return false
		}
		if err == nil && resp.StatusCode == 200 {
			glog.Infof("Route check [%v] got %v. Success!\n", url, resp.StatusCode)
			return true
		}

		if resp == nil {
			glog.Infof("Route check [%v] got %v. Still waiting %v before timeout\n", url, err, timeoutTime.Sub(time.Now()))
		} else {
			glog.Infof("Route check [%v] got %v and %v. Still waiting %v before timeout\n", url, resp.StatusCode, err, timeoutTime.Sub(time.Now()))
		}

		time.Sleep(time.Second)
	}
	return false
}

// newApp will create an application based upon the template configuration in config.
// newApp will monitor created objects until it receives any value from doneCh
// newApp will return a non-nil error when an unrecoverable error prevents it
//        from proceeding (and it will not read from doneCh in such cases) and nil
//        when it has read from doneCh
func newApp(config *Config, clients client.RESTClients, doneCh <-chan stepwatcher.CompleteStatus) error {
	glog.Infof("Step 2: %v\n", clients.TemplateClient)
	template, err := clients.TemplateClient.Templates(config.Template.Namespace).Get(config.Template.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("Unable to get template %v: %v", config.Template.Name, err)
	}

	glog.Infof("Step 3\n")
	// The template parameters for a TemplateInstance come from a secret, but
	// we'll create an empty one since we're fine with all the defaults
	secret, err := clients.CoreClient.Secrets(config.Check.Namespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "template-parameters",
		},
		//StringData: templateParams,
		StringData: config.Template.Parameters,
	})
	if err != nil {
		return fmt.Errorf("Error while creating template parameter secret: %v", err)
	}
	glog.Infof("Step 4\n")

	// Create a TemplateInstance object, linking the Template and a reference to
	// the Secret object created above.
	ti, err := clients.TemplateClient.TemplateInstances(config.Check.Namespace).Create(
		&templatev1.TemplateInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: config.Template.Name,
			},
			Spec: templatev1.TemplateInstanceSpec{
				Template: *template,
				Secret: &corev1.LocalObjectReference{
					Name: secret.Name,
				},
			},
		})
	if err != nil {
		return fmt.Errorf("Error creating %v application from TemplateInstance: %v", config.Template.Name, err)
	}
	glog.Infof("Step 5\n")
	watchers, err := stepwatcher.StepsFromTemplateInstance(ti, config.Check.Namespace, clients, doneCh)
	if err != nil {
		for _, v := range watchers {
			v.Stop()
		}
		glog.Errorf("Failed to create watches: %v\n", err)
	}

	wg := sync.WaitGroup{}

	for k, v := range watchers {
		glog.Infof("Adding watcher %v\n", k)
		wg.Add(1)
		go func(name string, step *stepwatcher.Step) {
			defer wg.Done()
			for {
				event, ok := <-step.ResultChan()
				if !ok {
					// cancelled by completion or timeout
					return
				}
				ok, err = step.Event(event)
				if err != nil {
					glog.Errorf("StepWatcher returned error %v\n", err)
					step.Done(false, err, watchers)
				} else if ok {
					glog.Infof("StepWatcher says we're done %v\n", name)
					step.Done(false, nil, watchers)
				} else {
					glog.Infof("StepWatcher says we're not done %v\n", name)
				}
			}
		}(k, v)
	}

	glog.Infof("Step 6\n")
	// wait for app's route to become available or timeout
	completed := <-doneCh
	// stop all of the watchers, figure out which (if any) may have prevented a successful deployment
	for _, watcher := range watchers {
		watcher.Done(completed == stepwatcher.CompletedTimeout, nil, watchers)
		watcher.Stop()
	}
	wg.Wait()
	return nil
}

func delApp(config *Config, clients client.RESTClients) error {
	glog.Infof("Step 7\n")

	// Delete everything

	// We use the foreground propagation policy to ensure that the garbage
	// collector removes all instantiated objects before the TemplateInstance
	// itself disappears.
	foreground := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &foreground}
	err := clients.TemplateClient.TemplateInstances(config.Check.Namespace).Delete(config.Template.Name, &deleteOptions)
	if err != nil {
		return fmt.Errorf("Error deleting application %v: %v", config.Template.Name, err)
	}

	tiStepWatcher, err := stepwatcher.NewTemplateInstanceStepWatcher(config.Check.Namespace, metav1.ObjectMeta{Name: config.Template.Name}, clients.TemplateClient)
	if err != nil {
		return fmt.Errorf("Unable to watch TemplateInstance %v for deletion: %v", config.Template.Name, err)
	}

	glog.Infof("Step 8\n")
	// TODO: setting for how long we should wait for everything to be deleted
	timeout := time.NewTimer(config.Timeout.TemplateDeletion.Duration)
WaitDeleteLoop:
	for {
		select {
		case <-timeout.C:
			return fmt.Errorf("Timeout while waiting for application deletion")
		case event, ok := <-tiStepWatcher.ResultChan():
			if !ok {
				break WaitDeleteLoop // channel is closed, we've read all the events that arrived before we .Stop()ped below
			}
			switch event.Type {
			case watch.Added:
				// ignore
			case watch.Modified:
				// ignore
			case watch.Deleted:
				tiStepWatcher.Stop()
			default:
				glog.Errorf("unexpected event type: %v\n", event.Type)
			}
		}
	}

	glog.Infof("Step 9\n")
	// Finally delete the "template-parameters" Secret.
	err = clients.CoreClient.Secrets(config.Check.Namespace).Delete("template-parameters", &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("Error while trying to delete secret: %v", err)
	}
	glog.Infof("Step 10\n")
	return nil
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

func readConfig(filename string) (*Config, error) {
	// Ensure the entire path is there for reading.
	absFilename, _ := filepath.Abs(filename)

	// Actually read in the file as a byte array
	yamlFile, err := ioutil.ReadFile(absFilename)
	if err != nil {
		return nil, err
	}

	// Convert the yaml byte array into the config structure object.
	var cfg Config
	err = yaml.Unmarshal(yamlFile, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	tmp, err := time.ParseDuration(s)
	if err != nil {
		return err
	}

	d.Duration = tmp

	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}
