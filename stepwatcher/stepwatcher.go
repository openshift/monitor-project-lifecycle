package stepwatcher

import (
	"fmt"
	"strings"
	"sync"
	"time"

	appsv1 "github.com/openshift/api/apps/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/monitor-project-lifecycle/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
)

type Watcher watch.Interface

// StepWatcher is just like watch.Interface, except that its Event() can tell when a step is complete
type StepWatcher interface {
	Watcher // also known as watch.Interface
	// Event examines an event
	// Event returns false,nil when the event doesn't indicate that the task has completed
	// Event returns true,nil when the event indicates that the task completed
	// Event returns false,err on an error that indicates that the task will never complete
	Event(event watch.Event) (bool, error)
}

type BasicStepWatcher struct {
	Watcher
	name string
}

type Step struct {
	StepWatcher
	Err        error
	EndTime    time.Time
	Dependents []string
	Prereqs    []string
	Mutex      sync.Mutex
}

func makePrereqs(retval map[string]*Step) map[string]*Step {
	for k, v := range retval {
		for _, depkey := range v.Dependents {
			if dep, ok := retval[depkey]; ok {
				dep.Prereqs = append(dep.Prereqs, k)
			}
		}
	}
	return retval
}

func StepsFromTemplateInstance(ti *templatev1.TemplateInstance, namespace string, clients client.RESTClients, timeoutChan <-chan time.Time) (map[string]*Step, error) {
	retval := map[string]*Step{}
	deps := map[string][]string{}
	// a map of service name to label selectors - used to create endpoints dependencies
	svcLabels := map[string]map[string]string{}
	// a list of all found deploymentconfigs - used to create endpoints dependencies
	var dcs []*appsv1.DeploymentConfig

	// The final watched item to get to a ready state should be this one, the TemplateInstance
	// but we watch it first so that we can wait for it and get a version of it that has .Status.Objects populated
	tistepwatcher, err := NewTemplateInstanceStepWatcher(namespace, ti.ObjectMeta, clients.TemplateClient)
	if err != nil {
		return makePrereqs(retval), fmt.Errorf("Unable to watch TemplateInstance %v: %v", ti.Name, err)
	}
	tiStep := Step{
		StepWatcher: tistepwatcher,
		Dependents:  deps["templateinstance/"+ti.Name],
	}
	retval["templateinstance/"+ti.Name] = &tiStep
GetTILoop:
	for {
		select {
		case event := <-tiStep.ResultChan():
			switch event.Type {
			case watch.Added:
				fallthrough
			case watch.Modified:
				ti = event.Object.(*templatev1.TemplateInstance)
				if len(ti.Status.Objects) > 0 {
					break GetTILoop
				}
			case watch.Deleted:
				return makePrereqs(retval), fmt.Errorf("TemplateInstance %v was deleted before deploy finished", ti.Name)
			}
		case <-timeoutChan:
			return makePrereqs(retval), fmt.Errorf("Timed out before getting TemplateInstance update for %v", ti.Name)
		}
	}

	// make one pass to find prereq->dep relationships
	for _, obj := range ti.Status.Objects {
		switch obj.Ref.Kind {
		case "DeploymentConfig":
			dc, err := clients.AppsClient.DeploymentConfigs(namespace).Get(obj.Ref.Name, metav1.GetOptions{})
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to get dc %v: %v", obj.Ref.Name, err)
			}
			dcs = append(dcs, dc)
			// find out which ISTs it needs
			for _, trigger := range dc.Spec.Triggers {
				if trigger.Type != "ImageChange" {
					continue
				}
				if trigger.ImageChangeParams.From.Kind == "ImageStreamTag" && trigger.ImageChangeParams.From.Namespace == namespace {
					key := "imagestreamtag/" + trigger.ImageChangeParams.From.Name
					deps[key] = append(deps[key], "dc/"+obj.Ref.Name)
				}
			}
			// find out which PVCs it needs
			for _, vol := range dc.Spec.Template.Spec.Volumes {
				if vol.PersistentVolumeClaim != nil {
					key := "pvc/" + vol.PersistentVolumeClaim.ClaimName
					deps[key] = append(deps[key], "dc/"+obj.Ref.Name)
				}
			}
		case "Route":
			route, err := clients.RouteClient.Routes(namespace).Get(obj.Ref.Name, metav1.GetOptions{})
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to get route %v: %v", obj.Ref.Name, err)
			}
			if route.Spec.To.Kind == "Service" {
				key := "svc/" + route.Spec.To.Name
				deps[key] = append(deps[key], "route/"+obj.Ref.Name)
			}
		case "Service":
			service, err := clients.CoreClient.Services(namespace).Get(obj.Ref.Name, metav1.GetOptions{})
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to get service %v: %v", obj.Ref.Name, err)
			}
			svcLabels[service.Name] = service.Spec.Selector
		}
	}
	// we need to wait for endpoints, but they aren't among the ti.Status.Objects
	for svc, sel := range svcLabels {
		selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: sel})
		if err != nil {
			return makePrereqs(retval), fmt.Errorf("Unable to create label selector to find endpoints: %v", err)
		}
		needEP := false
		for _, dc := range dcs {
			if selector.Matches(labels.Set(dc.Spec.Template.Labels)) {
				needEP = true
				deps["dc/"+dc.Name] = append(deps["dc/"+dc.Name], "ep/"+svc)
			}
		}
		if needEP {
			deps["svc/"+svc] = append(deps["svc/"+svc], "ep/"+svc)
			sw, err := NewEndpointsStepWatcher(namespace, svc, clients.CoreClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch endpoints %v: %v", svc, err)
			}
			retval["ep/"+svc] = &Step{
				StepWatcher: sw,
				Dependents:  append(deps["ep/"+svc], "templateinstance/"+ti.Name),
			}
		}
	}
	// second pass: look for objects that we can watch and wait for
	for _, obj := range ti.Status.Objects {
		switch obj.Ref.Kind {
		case "BuildConfig":
			bc, err := clients.BuildClient.BuildConfigs(namespace).Get(obj.Ref.Name, metav1.GetOptions{})
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to get bc %v: %v", obj.Ref.Name, err)
			}
			buildName := fmt.Sprintf("%s-%d", bc.Name, bc.Status.LastVersion)
			istdeps := []string{}
			if bc.Spec.Output.To.Kind == "ImageStreamTag" {
				istdeps = append(istdeps, "imagestreamtag/"+bc.Spec.Output.To.Name)
			}
			sw, err := NewBuildStepWatcher(namespace, buildName, clients.BuildClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch build %v: %v", buildName, err)
			}
			retval["build/"+buildName] = &Step{
				StepWatcher: sw,
				Dependents:  append(deps["bc/"+obj.Ref.Name], istdeps...),
			}
		case "Service":
			sw, err := NewServiceStepWatcher(namespace, obj.Ref.Name, clients.CoreClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch service %v: %v", obj.Ref.Name, err)
			}
			retval["svc/"+obj.Ref.Name] = &Step{
				StepWatcher: sw,
				Dependents:  deps["svc/"+obj.Ref.Name],
			}
		case "Route":
			sw, err := NewRouteStepWatcher(namespace, obj.Ref.Name, clients.RouteClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch route %v: %v", obj.Ref.Name, err)
			}
			retval["route/"+obj.Ref.Name] = &Step{
				StepWatcher: sw,
				Dependents:  deps["route/"+obj.Ref.Name],
			}
		case "DeploymentConfig":
			sw, err := NewDeploymentConfigStepWatcher(namespace, obj.Ref.Name, clients.AppsClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch dc %v: %v", obj.Ref.Name, err)
			}
			retval["dc/"+obj.Ref.Name] = &Step{
				StepWatcher: sw,
				Dependents:  append(deps["dc/"+obj.Ref.Name], "templateinstance/"+ti.Name),
			}
		case "PersistentVolumeClaim":
			sw, err := NewPersistentVolumeClaimStepWatcher(namespace, obj.Ref.Name, clients.CoreClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch pvc %v: %v", obj.Ref.Name, err)
			}
			retval["pvc/"+obj.Ref.Name] = &Step{
				StepWatcher: sw,
				Dependents:  deps["pvc/"+obj.Ref.Name],
			}
		}
	}
	// we need to wait for istags too, but they aren't among the ti.Status.Objects
	for k, v := range deps {
		if strings.HasPrefix(k, "imagestreamtag/") {
			ist := k[len("imagestreamtag/"):]
			sw, err := NewImageStreamTagStepWatcher(namespace, ist, clients.ImageClient)
			if err != nil {
				return makePrereqs(retval), fmt.Errorf("Unable to watch imagestreamtag %v: %v", ist, err)
			}
			retval["imagestreamtag/"+ist] = &Step{
				StepWatcher: sw,
				Dependents:  v,
			}
		}
	}
	return makePrereqs(retval), nil
}
