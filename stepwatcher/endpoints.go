package stepwatcher

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	//"github.com/ghodss/yaml"
)

type EndpointsStepWatcher BasicStepWatcher

func NewEndpointsStepWatcher(namespace, name string, coreclient *corev1client.CoreV1Client) (*EndpointsStepWatcher, error) {
	retval := EndpointsStepWatcher{name: name}
	var err error
	retval.Interface, err = coreclient.Endpoints(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch endpoints %v: %v", name, err)
	}
	return &retval, nil
}

func (a *EndpointsStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		endpoints := event.Object.(*corev1.Endpoints)
		//eventYaml, _ := yaml.Marshal(endpoints)
		//fmt.Printf("Got event for ep/%v:\n%v\n", a.name, string(eventYaml))
		for _, subset := range endpoints.Subsets {
			if len(subset.NotReadyAddresses) == 0 || len(subset.Addresses) > 0 {
				return true, nil
			}
		}
	default:
		fmt.Printf("unexpected event type while watching endpoints %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
