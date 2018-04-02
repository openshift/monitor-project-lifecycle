package stepwatcher

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

type ServiceStepWatcher BasicStepWatcher

func NewServiceStepWatcher(namespace, name string, coreclient *corev1client.CoreV1Client) (*ServiceStepWatcher, error) {
	retval := ServiceStepWatcher{name: name}
	var err error
	retval.Watcher, err = coreclient.Services(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch service %v: %v", name, err)
	}
	return &retval, nil
}

func (a *ServiceStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		service := event.Object.(*corev1.Service)
		_ = service
		//eventYaml, _ := yaml.Marshal(service)
		//fmt.Printf("Got event for svc/%v:\n%v\n", a.name, string(eventYaml))
		return true, nil
	default:
		fmt.Printf("unexpected event type while watching service %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
