package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	//projectv1client "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
	"github.com/prometheus/client_golang/prometheus"

	templatev1 "github.com/openshift/api/template/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"

	corev1 "k8s.io/api/core/v1"
	//v1beta1 "k8s.io/api/extensions/v1beta1"
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
	ch <- jc.stat
}

func (jc *jenkinsSmokeTestCollector) successMetric() {
	lv := []string{jc.namespace, "success", ""}
	jc.stat = prometheus.MustNewConstMetric(jc.desc, prometheus.GaugeValue, float64(time.Now().Unix()), lv...)
}

func (jc *jenkinsSmokeTestCollector) failureMetric(errStr string) {
	lv := []string{jc.namespace, "failure", errStr}
	jc.stat = prometheus.MustNewConstMetric(jc.desc, prometheus.GaugeValue, float64(time.Now().Unix()), lv...)
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

	go runAppCreateSim(collector, 10*time.Minute)

	select {}
}

func instantiateJenkins(templateclient *templatev1client.TemplateV1Client, coreclient *corev1client.CoreV1Client, namespace string) error {
	// Get the "jenkins-ephemeral" Template from the "openshift" Namespace.
	template, err := templateclient.Templates("openshift").Get(
		"jenkins-ephemeral", metav1.GetOptions{})
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
					cond.Status == corev1.ConditionTrue {
					return fmt.Errorf("templateinstance instantiation failed")
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

func getRestConfig() (*restclient.Config, error) {
	// Build a rest.Config from configuration injected into the Pod by
	// Kubernetes.  Clients will use the Pod's ServiceAccount principal.
	return restclient.InClusterConfig()
}

func getNamespace() string {
	return os.Getenv("NAMESPACE")
}

func listPods(coreclient *corev1client.CoreV1Client, namespace string) error {
	pods, err := coreclient.Pods(namespace).List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	fmt.Printf("Pods in namespace %s:\n", namespace)
	for _, pod := range pods.Items {
		fmt.Printf("  %s\n", pod.Name)
	}

	return nil
}

func scaleJenkins(coreclient *corev1client.CoreV1Client, namespace string, up bool) error {
	scale, err := coreclient.ReplicationControllers(namespace).GetScale("jenkins",
		metav1.GetOptions{})
	if err != nil {
		return err
	}

	if up {
		scale.Spec.Replicas = 0
	} else {
		scale.Spec.Replicas = 1
	}

	_, err = coreclient.ReplicationControllers(namespace).UpdateScale("jenkins", scale)
	return err
}

// Cleans up all objects that this monitoring command creates
/*func cleanupWorkspace(project string, restconfig *restclient.Config) error {
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
}*/

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func runAppCreateSim(collector jenkinsSmokeTestCollector, interval time.Duration) {

	var templateclient *templatev1client.TemplateV1Client
	var coreclient *corev1client.CoreV1Client
	var namespace string
	restconfig, err := getRestConfig()
	if err == nil {
		collector.namespace = getNamespace()
		fmt.Printf("namesapce is %s\n", collector.namespace)
		templateclient, err = getTemplateClient(restconfig)
		if err == nil {
			coreclient, err = getCoreClient(restconfig)
			err = instantiateJenkins(templateclient, coreclient, namespace)
			if err == nil {
				listPods(coreclient, namespace)
			}
		}
	}
	instantiated := true
	if err != nil {
		instantiated = false
		errStr := fmt.Sprintf("setup error %#v", err)
		fmt.Printf("%s\n", errStr)
		collector.failureMetric(errStr)
	} else {
		collector.successMetric()
	}

	for {
		time.Sleep(interval)
		if !instantiated {
			if restconfig == nil {
				restconfig, err = getRestConfig()
				if err != nil {
					errStr := fmt.Sprintf("setup error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
				collector.namespace = getNamespace()
				fmt.Printf("namesapce is %s\n", collector.namespace)
			}

			if templateclient == nil {
				templateclient, err = getTemplateClient(restconfig)
				if err != nil {
					errStr := fmt.Sprintf("setup error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
			}

			if coreclient == nil {
				coreclient, err = getCoreClient(restconfig)
				if err != nil {
					errStr := fmt.Sprintf("setup error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
			}

			if !instantiated {
				err = instantiateJenkins(templateclient, coreclient, namespace)
				if err != nil {
					errStr := fmt.Sprintf("setup error %#v", err)
					fmt.Printf("%s\n", errStr)
					collector.failureMetric(errStr)
					continue
				}
				listPods(coreclient, namespace)
			}
		}

		err = scaleJenkins(coreclient, namespace, false)
		if err != nil {
			errStr := fmt.Sprintf("setup error %#v", err)
			fmt.Printf("%s\n", errStr)
			collector.failureMetric(errStr)
			continue
		}

		err = scaleJenkins(coreclient, namespace, true)
		if err != nil {
			errStr := fmt.Sprintf("setup error %#v", err)
			fmt.Printf("%s\n", errStr)
			collector.failureMetric(errStr)
			continue
		}

		collector.successMetric()
	}
}
