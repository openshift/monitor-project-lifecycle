package stepwatcher

import (
	"fmt"

	routev1 "github.com/openshift/api/route/v1"
	routev1client "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type RouteStepWatcher BasicStepWatcher

func NewRouteStepWatcher(namespace, name string, routeclient *routev1client.RouteV1Client) (*RouteStepWatcher, error) {
	retval := RouteStepWatcher{name: name}
	var err error
	retval.Watcher, err = routeclient.Routes(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch service %v: %v", name, err)
	}
	return &retval, nil
}

func (a *RouteStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		route := event.Object.(*routev1.Route)
		//eventYaml, _ := yaml.Marshal(route)
		//fmt.Printf("Got event for route/%v:\n%v\n", a.name, string(eventYaml))
		admittedCount := 0
		for _, ingress := range route.Status.Ingress {
			for _, cond := range ingress.Conditions {
				if cond.Type == routev1.RouteAdmitted && cond.Status == corev1.ConditionTrue {
					admittedCount++
				}
			}
		}
		if admittedCount == len(route.Status.Ingress) {
			return true, nil
		}

	default:
		fmt.Printf("unexpected event type while watching route %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
