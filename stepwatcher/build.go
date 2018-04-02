package stepwatcher

import (
	"fmt"

	buildv1 "github.com/openshift/api/build/v1"
	buildv1client "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	//"github.com/ghodss/yaml"
)

type BuildStepWatcher BasicStepWatcher

func NewBuildStepWatcher(namespace, name string, buildclient *buildv1client.BuildV1Client) (*BuildStepWatcher, error) {
	retval := BuildStepWatcher{name: name}
	var err error
	retval.Watcher, err = buildclient.Builds(namespace).Watch(metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return nil, fmt.Errorf("Unable to watch build %v: %v", name, err)
	}
	return &retval, nil
}

func (a *BuildStepWatcher) Event(event watch.Event) (bool, error) {

	switch event.Type {
	case watch.Added:
		fallthrough
	case watch.Modified:
		build := event.Object.(*buildv1.Build)
		//eventYaml, _ := yaml.Marshal(build)
		//fmt.Printf("Got event for build/%v:\n%v\n", a.name, string(eventYaml))
		switch build.Status.Phase {
		case buildv1.BuildPhaseComplete:
			return true, nil
		case buildv1.BuildPhaseFailed:
			return false, fmt.Errorf("build/%v marked as failed", a.name)
		case buildv1.BuildPhaseError:
			return false, fmt.Errorf("build/%v has an error", a.name)
		case buildv1.BuildPhaseCancelled:
			return false, fmt.Errorf("build/%v marked as cancelled", a.name)
		}

	default:
		fmt.Printf("unexpected event type while watching build %v: %v\n", a.name, event.Type)
	}
	return false, nil
}
