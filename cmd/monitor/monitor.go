package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ghodss/yaml"
	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/monitor-project-lifecycle/client"
	"github.com/openshift/monitor-project-lifecycle/stepwatcher"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// Config: This is the structure for the config file we read in.
type Config struct {
	Namespace         string `yaml:"namespace"`
	TemplateName      string `yaml:"templateName"`
	TemplateNamespace string `yaml:"templateNamespace"`
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

func main() {
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()

	// before we bring everything up, we need to make sure that we have
	// a working configuration to connect to the API server
	restconfig, err := client.GetRestConfig()
	if err != nil {
		fmt.Printf("Fatal error: %v\n", err)
		os.Exit(1)
	}

	clients, err := client.MakeRESTClients(restconfig)
	if err != nil {
		fmt.Printf("Fatal error: %v\n", err)
		os.Exit(1)
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

	go http.ListenAndServe(*addr, nil)

	go runAppCreateSim(appCreateLatency, 1*time.Second)

	// TODO: the namespace where we should do everything should be configurable
	namespace := "example"
	// TODO: the name of the template (and resulting app) should be configurable
	template := "django-psql-persistent"
	// TODO: the name of the namespace where the template lives should be configurable
	templateNS := "openshift"
	// TODO: the template parameters should be configurable
	var templateParams map[string]string
	// TODO: the places where we call time.NewTimer() need configurable values passed down to them

	cleanupWorkspace(namespace, clients)

	// TODO: setting for how often we should do the app-create loop
	interval := 5 * time.Minute
	for {
		startTime := time.Now()
		_, err = setupWorkspace(namespace, namespace, clients)
		if err != nil {
			fmt.Printf("Failed to create project: %v\n", err)
		} else {
			if err = newApp(namespace, template, templateNS, templateParams, clients); err != nil {
				fmt.Printf("Failed to create app: %v\n", err)
			}
			if err = delApp(namespace, template, clients); err != nil {
				fmt.Printf("Failed to remove app: %v\n", err)
			}
		}
		if err = cleanupWorkspace(namespace, clients); err != nil {
			fmt.Printf("Failed to remove project: %v\n", err)
		}
		if elapsed := time.Since(startTime); elapsed < interval {
			fmt.Printf("Sleeping for %v before next iteration\n", interval-elapsed)
			time.Sleep(interval - elapsed)
		}
	}
}

// Sets up the workspace that this monitoring command will use.
// TODO: Add prometheus metrics for timing and errors.
func setupWorkspace(projectName, displayName string, clients client.RESTClients) (*projectv1.Project, error) {
	// Delete the project that contains the kube resources we've created
	//err = projectclient.Projects().Create(project, &metav1.DeleteOptions{})

	prj, err := clients.ProjectClient.ProjectRequests().Create(
		&projectv1.ProjectRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name: projectName,
			},
			DisplayName: displayName,
		},
	)

	if err != nil {
		return nil, fmt.Errorf("Failed to create project %v: %v", projectName, err)
	}

	return prj, nil
}

// Cleans up all objects that this monitoring command creates
// TODO: Add prometheus metrics for timing and errors.
func cleanupWorkspace(projectName string, clients client.RESTClients) error {
	// Delete the project that contains the kube resources we've created
	err := clients.ProjectClient.Projects().Delete(projectName, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("Failed to remove project %v: %v", projectName, err)
	}
	return nil
}

func newApp(namespace, templateName, templateNS string, templateParams map[string]string, clients client.RESTClients) error {
	fmt.Printf("Step 2: %v\n", clients.TemplateClient)
	template, err := clients.TemplateClient.Templates(templateNS).Get(templateName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("Unable to get template %v: %v", templateName, err)
	}

	fmt.Printf("Step 3\n")
	// The template parameters for a TemplateInstance come from a secret, but
	// we'll create an empty one since we're fine with all the defaults
	secret, err := clients.CoreClient.Secrets(namespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "template-parameters",
		},
		StringData: templateParams,
	})
	if err != nil {
		return fmt.Errorf("Error while creating template parameter secret: %v", err)
	}
	fmt.Printf("Step 4\n")

	// Create a TemplateInstance object, linking the Template and a reference to
	// the Secret object created above.
	ti, err := clients.TemplateClient.TemplateInstances(namespace).Create(
		&templatev1.TemplateInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: templateName,
			},
			Spec: templatev1.TemplateInstanceSpec{
				Template: *template,
				Secret: &corev1.LocalObjectReference{
					Name: secret.Name,
				},
			},
		})
	if err != nil {
		return fmt.Errorf("Error creating %v application from TemplateInstance: %v", templateName, err)
	}
	fmt.Printf("Step 5\n")
	// TODO: this should be configurable, not hard-coded
	timeout := time.NewTimer(10 * time.Minute)
	watchers, err := stepwatcher.StepsFromTemplateInstance(ti, namespace, clients, timeout.C)
	if err != nil {
		for _, v := range watchers {
			v.Stop()
		}
		fmt.Printf("Failed to create watches: %v\n", err)
	}

	wg := sync.WaitGroup{}
	for k, v := range watchers {
		fmt.Printf("Adding watcher %v\n", k)
		wg.Add(1)
		go func(name string, step *stepwatcher.Step) {
			defer wg.Done()
			for {
				event, ok := <-step.ResultChan()
				if !ok {
					// cancelled by a prereq or timeout
					return
				}
				ok, err := step.Event(event)
				if err != nil {
					fmt.Printf("StepWatcher returned error %v\n", err)
					seen := map[string]bool{}
					now := time.Now()
					var cancel func([]string)
					cancel = func(keys []string) {
						for _, depkey := range keys {
							if seen[depkey] { // prevent infinite recursion in the case of cycles
								continue
							}
							seen[depkey] = true
							if dep, ok := watchers[depkey]; ok && dep.EndTime.IsZero() {
								dep.Mutex.Lock()
								if dep.EndTime.IsZero() { // double check now that we hold the mutex
									dep.EndTime = now
									dep.Err = fmt.Errorf("Unable to complete due to failed prerequisite: %v", name)
								}
								dep.Mutex.Unlock()
								dep.Stop()
							}
						}
					}
					cancel(step.Dependents)
					break
				} else if ok {
					fmt.Printf("StepWatcher says we're done %v\n", name)
					break
				} else {
					fmt.Printf("StepWatcher says we're not done %v\n", name)
				}
			}
			step.Mutex.Lock()
			step.EndTime = time.Now()
			step.Err = err
			step.Mutex.Unlock()
		}(k, v)
	}
	donech := make(chan bool)
	go func() {
		wg.Wait()
		donech <- true
	}()
	fmt.Printf("Step 6\n")

	select {
	case <-timeout.C:
		for k, v := range watchers {
			now := time.Now()
			if v.EndTime.IsZero() {
				v.Mutex.Lock()
				if v.EndTime.IsZero() { // double check now that we hold the mutex
					v.EndTime = now
					v.Err = fmt.Errorf("Timed out before application deployment succeeded: %v", k)
				}
				v.Mutex.Unlock()
				v.Stop()
			}
		}
	case <-donech:
	}
	return nil
}

func delApp(namespace, templateName string, clients client.RESTClients) error {
	fmt.Printf("Step 7\n")

	// Delete everything

	// We use the foreground propagation policy to ensure that the garbage
	// collector removes all instantiated objects before the TemplateInstance
	// itself disappears.
	foreground := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &foreground}
	err := clients.TemplateClient.TemplateInstances(namespace).Delete(templateName, &deleteOptions)
	if err != nil {
		return fmt.Errorf("Error deleting application %v: %v\n", templateName, err)
	}

	tiStepWatcher, err := stepwatcher.NewTemplateInstanceStepWatcher(namespace, metav1.ObjectMeta{Name: templateName}, clients.TemplateClient)
	if err != nil {
		return fmt.Errorf("Unable to watch TemplateInstance %v for deletion: %v", templateName, err)
	}

	fmt.Printf("Step 8\n")
	// TODO: this should be configurable, not hard-coded
	// TODO: setting for how long we should wait for everything to be deleted
	timeout := time.NewTimer(5 * time.Minute)
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
				fmt.Printf("unexpected event type: %v\n", event.Type)
			}
		}
	}

	fmt.Printf("Step 9\n")
	// Finally delete the "template-parameters" Secret.
	err = clients.CoreClient.Secrets(namespace).Delete("template-parameters", &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("Error while trying to delete secret: %v", err)
	}
	fmt.Printf("Step 10\n")
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
