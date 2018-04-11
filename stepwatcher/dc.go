package stepwatcher

import (
	"fmt"

	appsv1 "github.com/openshift/api/apps/v1"
	appsv1client "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type DeploymentConfigStepWatcher BasicStepWatcher

func NewDeploymentConfigStepWatcher(namespace, name string, appsclient *appsv1client.AppsV1Client) (*DeploymentConfigStepWatcher, error) {
	retval := DeploymentConfigStepWatcher{name: name}
	var err error
	retval.Interface, err = appsclient.DeploymentConfigs(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch service %v: %v", name, err)
	}
	return &retval, nil
}

func (a *DeploymentConfigStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		dc := event.Object.(*appsv1.DeploymentConfig)
		// eventYaml, _ := yaml.Marshal(dc)
		//fmt.Printf("Got event for dc/%v:\n%v\n", a.name, string(eventYaml))
		for _, cond := range dc.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
			if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse {
				return false, fmt.Errorf("dc/%v marked as not progressing", a.name)
			}
		}

	default:
		fmt.Printf("unexpected event type while watching dc %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
