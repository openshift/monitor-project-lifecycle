package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	templatev1 "github.com/openshift/api/template/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"

	appsv1 "github.com/openshift/api/apps/v1"
	appsv1client "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	//glide update of the dependencies for this failed with
	/*
		[ERROR]	Error scanning github.com/prometheus/procfs/nfs: cannot find package "." in:
			/home/gmontero/.glide/cache/src/https-github.com-prometheus-procfs/nfs
	*/
	//oapps "github.com/openshift/origin/pkg/apps/apis/apps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
)

type jenkinsSmokeTestCollector struct {
	desc      *prometheus.Desc
	stat      prometheus.Metric
	namespace string
}

// Describe implements the prometheus.Collector interface.
func (jc *jenkinsSmokeTestCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- jc.desc
}

// Collect implements the prometheus.Collector interface.
func (jc *jenkinsSmokeTestCollector) Collect(ch chan<- prometheus.Metric) {
	fmt.Printf("Prometheus metric collection has called, returning %#v\n", jc.stat)
	ch <- jc.stat
}

func (jc *jenkinsSmokeTestCollector) successMetric() {
	lv := []string{jc.namespace, "success", ""}
	jc.stat = prometheus.MustNewConstMetric(jc.desc, prometheus.GaugeValue, float64(time.Now().Unix()), lv...)
	fmt.Printf("Registering successful deployment metric %#v\n", jc.stat)
}

func (jc *jenkinsSmokeTestCollector) failureMetric(errStr string) {
	lv := []string{jc.namespace, "failure", errStr}
	jc.stat = prometheus.MustNewConstMetric(jc.desc, prometheus.GaugeValue, float64(time.Now().Unix()), lv...)
	fmt.Printf("Registering failed deployment metric %#v\n", jc.stat)
}

func main() {
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()

	http.HandleFunc("/healthz", handleHealthz)
	http.Handle("/metrics", prometheus.Handler())

	collector := jenkinsSmokeTestCollector{}
	collector.desc = prometheus.NewDesc(
		"jenkins_smoke_test_seconds",
		"Shows the time in unix epoch the jenkins smoke test was run by namespace, status, and reason",
		[]string{"namespace", "status", "reason"},
		nil,
	)

	prometheus.MustRegister(&collector)

	go http.ListenAndServe(*addr, nil)

	go runAppCreateSim(&collector, 1*time.Minute)

	select {}
}

func instantiateJenkins(templateclient *templatev1client.TemplateV1Client, coreclient *corev1client.CoreV1Client, namespace string) error {
	// Get the "jenkins-persistent" Template from the "openshift" Namespace.
	template, err := templateclient.Templates("openshift").Get(
		"jenkins-persistent", metav1.GetOptions{})
	if err != nil {
		return err
	}

	// INSTANTIATE THE TEMPLATE.

	// To set Template parameters, create a Secret holding overridden parameters
	// and their values.
	secret, err := coreclient.Secrets(namespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "parameters",
		},
		StringData: map[string]string{
			"MEMORY_LIMIT": "1024Mi",
		},
	})
	if err != nil {
		return err
	}

	// Create a TemplateInstance object, linking the Template and a reference to
	// the Secret object created above.
	ti, err := templateclient.TemplateInstances(namespace).Create(
		&templatev1.TemplateInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: "templateinstance",
			},
			Spec: templatev1.TemplateInstanceSpec{
				Template: *template,
				Secret: &corev1.LocalObjectReference{
					Name: secret.Name,
				},
			},
		})
	if err != nil {
		return err
	}

	// Watch the TemplateInstance object until it indicates the Ready or
	// InstantiateFailure status condition.
	watcher, err := templateclient.TemplateInstances(namespace).Watch(
		metav1.SingleObject(ti.ObjectMeta),
	)
	if err != nil {
		return err
	}

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Modified:
			ti = event.Object.(*templatev1.TemplateInstance)

			for _, cond := range ti.Status.Conditions {
				// If the TemplateInstance contains a status condition
				// Ready == True, stop watching.
				if cond.Type == templatev1.TemplateInstanceReady &&
					cond.Status == corev1.ConditionTrue {
					watcher.Stop()
				}

				// If the TemplateInstance contains a status condition
				// InstantiateFailure == True, indicate failure.
				if cond.Type ==
					templatev1.TemplateInstanceInstantiateFailure &&
					cond.Status == corev1.ConditionTrue &&
					cond.Reason != "AlreadyExists" {
					return fmt.Errorf("templateinstance instantiation failed reason %s message %s", cond.Reason, cond.Message)
				}
			}

		default:
			return fmt.Errorf("unexpected event type %s", string(event.Type))
		}
	}

	return nil
}

func getTemplateClient(restconfig *restclient.Config) (*templatev1client.TemplateV1Client, error) {
	return templatev1client.NewForConfig(restconfig)
}

func getCoreClient(restconfig *restclient.Config) (*corev1client.CoreV1Client, error) {
	return corev1client.NewForConfig(restconfig)
}

func getAppsClient(restconfig *restclient.Config) (*appsv1client.AppsV1Client, error) {
	return appsv1client.NewForConfig(restconfig)
}

func getRestConfig() (*restclient.Config, error) {
	// Build a rest.Config from configuration injected into the Pod by
	// Kubernetes.  Clients will use the Pod's ServiceAccount principal.
	return restclient.InClusterConfig()
}

func getNamespace() string {
	b, _ := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/" + corev1.ServiceAccountNamespaceKey)
	return string(b)
}

func listPods(coreclient *corev1client.CoreV1Client, namespace string) error {
	pods, err := coreclient.Pods(namespace).List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	fmt.Printf("Pods in namespace %s:\n", namespace)
	for _, pod := range pods.Items {
		fmt.Printf("  %s\t%s\n", pod.Name, string(pod.Status.Phase))
	}

	return nil
}

func deployJenkins(appsclient *appsv1client.AppsV1Client, namespace string) error {
	dc, err := appsclient.DeploymentConfigs(namespace).Get("jenkins",
		metav1.GetOptions{})
	if err != nil {
		return err
	}

	req := appsv1.DeploymentRequest{
		Name:   "jenkins",
		Latest: true,
		Force:  true,
	}
	dc, err = appsclient.DeploymentConfigs(namespace).Instantiate("jenkins",
		&req)

  if err != nil {
		return err
	}

	watcher, err := appsclient.DeploymentConfigs(namespace).Watch(
		metav1.SingleObject(dc.ObjectMeta))
	if err != nil {
		return err
	}

	// look for progressing to true with rcupdated reasone, then avail true
	// cannot look for avail true at outset, as that will exists for prior deployment
	rcUpdated := false
	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Modified:
			dc = event.Object.(*appsv1.DeploymentConfig)

			for _, cond := range dc.Status.Conditions {
				fmt.Printf("dc watch condition %#v\n", cond)

				if rcUpdated {
					if cond.Type == appsv1.DeploymentAvailable &&
						cond.Status == corev1.ConditionTrue {
						fmt.Printf("new deployment available\n")
						return nil
					} else {
						continue
					}
				}

				if cond.Type == appsv1.DeploymentProgressing {
					if cond.Status == corev1.ConditionTrue || cond.Status == corev1.ConditionUnknown {
						// see problem with glide import comment above as to why we do not use apps type.go constant
						if string(cond.Reason) == "ReplicationControllerUpdated" {
							fmt.Printf("dc rc udpated phase 1 complete \n")
							rcUpdated = true
							continue
						}

						fmt.Printf("dc deploy has not failed so continue watching\n")
						continue
					}

					return fmt.Errorf("dc rollout progessing failed reason %s message %s", cond.Reason, cond.Message)
				}

			}
		default:
			fmt.Printf("got event type %#v\n", string(event.Type))
		}
	}

	return nil
}

func pingJenkins(coreclient *corev1client.CoreV1Client, namespace string) error {
	svc, err := coreclient.Services(namespace).Get("jenkins",
		metav1.GetOptions{})
	if err != nil {
		return err
	}

	uri := fmt.Sprintf("http://%s:80/login", svc.Spec.ClusterIP)
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return err
	}
	req.Close = true
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ping of jenkins service had an rc of %d", resp.StatusCode)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("ping of jenkins service returned an empty body")
	}
	return nil
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func runAppCreateSim(collector *jenkinsSmokeTestCollector, interval time.Duration) {
	fmt.Printf("Jenkins smoke test loop start\n")
	var templateclient *templatev1client.TemplateV1Client
	var coreclient *corev1client.CoreV1Client
	restconfig, err := getRestConfig()
	if err == nil {
		collector.namespace = getNamespace()
		templateclient, err = getTemplateClient(restconfig)
		if err == nil {
			coreclient, err = getCoreClient(restconfig)
			err = instantiateJenkins(templateclient, coreclient, collector.namespace)
		}
	}
	instantiated := true
	if err != nil {
		instantiated = false
		errStr := fmt.Sprintf("instantiate error %#v", err)
		fmt.Printf("%s\n", errStr)
		collector.failureMetric(errStr)
		fmt.Printf("\nInitial Jenkins deployment failed $#v\n", err)
	} else {
		collector.successMetric()
		fmt.Printf("\nInitial Jenkins deployment succeeded\n")
	}

	listPods(coreclient, collector.namespace)

	for {
		time.Sleep(interval)
		if !instantiated {
			if restconfig == nil {
				restconfig, err = getRestConfig()
				if err != nil {
					errStr := fmt.Sprintf("restclient error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
				if len(collector.namespace) == 0 {
					collector.namespace = getNamespace()
					if len(collector.namespace) == 0 {
						continue
					}
				}
			}

			if templateclient == nil {
				templateclient, err = getTemplateClient(restconfig)
				if err != nil {
					errStr := fmt.Sprintf("templateclient error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
			}

			if coreclient == nil {
				coreclient, err = getCoreClient(restconfig)
				if err != nil {
					errStr := fmt.Sprintf("coreclient error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
			}

			if !instantiated {
				err = instantiateJenkins(templateclient, coreclient, collector.namespace)
				listPods(coreclient, collector.namespace)
				if err != nil {
					errStr := fmt.Sprintf("instantiate error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
			}
		}

		appsclient, err := getAppsClient(restconfig)
		if err != nil {
			errStr := fmt.Sprintf("appclient error %#v", err)
			fmt.Printf("%s\n", errStr)
			collector.failureMetric(errStr)
			continue
		}

		err = deployJenkins(appsclient, collector.namespace)
		listPods(coreclient, collector.namespace)
		if err != nil {
			errStr := fmt.Sprintf("deploy error %#v", err)
			fmt.Printf("%s\n", errStr)
			collector.failureMetric(errStr)
			continue
		}

		for i := 0; i < 3; i++ {
			err = pingJenkins(coreclient, collector.namespace)
			if err == nil {
				break
			}
			time.Sleep(10 * time.Second)
		}
		if err != nil {
			errStr := fmt.Sprintf("ping error %#v", err)
			fmt.Printf("%s\n", errStr)
			collector.failureMetric(errStr)
			continue
		}

		collector.successMetric()
	}
}
