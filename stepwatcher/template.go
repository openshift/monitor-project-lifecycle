package stepwatcher

import (
	"fmt"

	templatev1 "github.com/openshift/api/template/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type TemplateInstanceStepWatcher BasicStepWatcher

func NewTemplateInstanceStepWatcher(namespace string, metadata metav1.ObjectMeta, templateclient *templatev1client.TemplateV1Client) (*TemplateInstanceStepWatcher, error) {
	retval := TemplateInstanceStepWatcher{name: metadata.Name}
	var err error
	retval.Watcher, err = templateclient.TemplateInstances(namespace).Watch(metav1.SingleObject(metadata))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch TemplateInstance %v: %v", metadata.Name, err)
	}
	return &retval, nil
}

func (a *TemplateInstanceStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		ti := event.Object.(*templatev1.TemplateInstance)
		//eventYaml, _ := yaml.Marshal(ti)
		//fmt.Printf("Got event for ti/%v:\n%v\n", a.name, string(eventYaml))
		for _, cond := range ti.Status.Conditions {
			// If the TemplateInstance contains a status condition
			// Ready == True, stop watching.
			if cond.Type == templatev1.TemplateInstanceReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}

			// If the TemplateInstance contains a status condition
			// InstantiateFailure == True, indicate failure.
			if cond.Type == templatev1.TemplateInstanceInstantiateFailure && cond.Status == corev1.ConditionTrue {
				return false, fmt.Errorf("Application creatation failed")
			}
		}
	default:
		fmt.Printf("unexpected event type while watching TemplateInstance %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
