package stepwatcher

import (
	"fmt"
	"strings"

	imagev1 "github.com/openshift/api/image/v1"
	imagev1client "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type ImageStreamTagStepWatcher BasicStepWatcher

func NewImageStreamTagStepWatcher(namespace, name string, imageclient *imagev1client.ImageV1Client) (*ImageStreamTagStepWatcher, error) {
	retval := ImageStreamTagStepWatcher{name: name}
	var err error
	retval.Interface, err = imageclient.ImageStreams(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: strings.Split(name, ":")[0]}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch service %v: %v", name, err)
	}
	return &retval, nil
}

func (a *ImageStreamTagStepWatcher) Event(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		is := event.Object.(*imagev1.ImageStream)
		//eventYaml, _ := yaml.Marshal(is)
		//fmt.Printf("Got event for ist/%v:\n%v\n", a.name, string(eventYaml))
		tagName := strings.Split(a.name, ":")[1]
		for _, tag := range is.Status.Tags {
			if tag.Tag == tagName {
				return true, nil
			}
		}
	default:
		fmt.Printf("unexpected event type while watching ImageStreamTag %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
