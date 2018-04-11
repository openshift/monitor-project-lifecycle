package stepwatcher

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

type PersistentVolumeClaimStepWatcher BasicStepWatcher

func NewPersistentVolumeClaimStepWatcher(namespace, name string, coreclient *corev1client.CoreV1Client) (*PersistentVolumeClaimStepWatcher, error) {
	retval := PersistentVolumeClaimStepWatcher{name: name}
	var err error
	retval.Interface, err = coreclient.PersistentVolumeClaims(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch service %v: %v", name, err)
	}
	return &retval, nil
}

func (a *PersistentVolumeClaimStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		pvc := event.Object.(*corev1.PersistentVolumeClaim)
		//eventYaml, _ := yaml.Marshal(pvc)
		//fmt.Printf("Got event for pvc/%v:\n%v\n", a.name, string(eventYaml))
		switch pvc.Status.Phase {
		case corev1.ClaimBound:
			return true, nil
		case corev1.ClaimLost:
			return false, fmt.Errorf("PersistentVolumeClaim pvc/%v claim lost", a.name)
		}
	default:
		fmt.Printf("unexpected event type while watching pvc %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
