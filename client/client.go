package client

import (
	"fmt"

	appsv1client "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	buildv1client "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imagev1client "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	projectv1client "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
	routev1client "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type RESTClients struct {
	AppsClient     *appsv1client.AppsV1Client
	BuildClient    *buildv1client.BuildV1Client
	CoreClient     *corev1client.CoreV1Client
	ImageClient    *imagev1client.ImageV1Client
	RouteClient    *routev1client.RouteV1Client
	TemplateClient *templatev1client.TemplateV1Client
	ProjectClient  *projectv1client.ProjectV1Client
}

func MakeRESTClients(restconfig *restclient.Config) (RESTClients, error) {
	retval := RESTClients{}
	var err error

	retval.AppsClient, err = appsv1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create AppsV1Client: %v", err)
	}

	retval.BuildClient, err = buildv1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create BuildV1Client: %v", err)
	}

	retval.CoreClient, err = corev1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create CoreV1Client: %v", err)
	}

	retval.ImageClient, err = imagev1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create ImageV1Client: %v", err)
	}

	retval.RouteClient, err = routev1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create RouteV1Client: %v", err)
	}

	retval.TemplateClient, err = templatev1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create TemplateV1Client: %v", err)
	}

	retval.ProjectClient, err = projectv1client.NewForConfig(restconfig)
	if err != nil {
		return retval, fmt.Errorf("Unable to create ProjectV1Client: %v", err)
	}
	return retval, nil
}

// Creates a rest config object that is used for other client calls.
func GetRestConfig() (*restclient.Config, error) {
	// Instantiate loader for kubeconfig file.
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	// Get a rest.Config from the kubeconfig file.  This will be passed into all
	// the client objects we create.
	restconfig, err := kubeconfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("Unable to create API server client configuration: %v", err)
	}

	return restconfig, nil
}
