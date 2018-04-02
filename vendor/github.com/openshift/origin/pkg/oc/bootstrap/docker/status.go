package docker

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/go-units"
	"github.com/spf13/cobra"
	"k8s.io/kubernetes/pkg/kubectl/cmd/templates"

	"github.com/openshift/origin/pkg/cmd/server/apis/config"
	configapilatest "github.com/openshift/origin/pkg/cmd/server/apis/config/latest"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/dockerhelper"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/errors"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/exec"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/openshift"
	"github.com/openshift/origin/pkg/oc/cli/util/clientcmd"
)

// CmdStatusRecommendedName is the recommended command name
const CmdStatusRecommendedName = "status"

var (
	cmdStatusLong = templates.LongDesc(`
		Show the status of the local OpenShift cluster.

		If you started your OpenShift with a specific docker-machine, you need to specify the
		same machine using the --docker-machine argument.`)

	cmdStatusExample = templates.Examples(`
		# See status of local OpenShift cluster
		%[1]s

		# See status of OpenShift cluster running on Docker machine 'mymachine'
		%[1]s --docker-machine=mymachine`)
)

// NewCmdStatus implements the OpenShift cluster status command.
func NewCmdStatus(name, fullName string, f *clientcmd.Factory, out io.Writer) *cobra.Command {
	clientStatusConfig := &ClientStatusConfig{}
	cmd := &cobra.Command{
		Use:     name,
		Short:   "Show OpenShift on Docker status",
		Long:    cmdStatusLong,
		Example: fmt.Sprintf(cmdStatusExample, fullName),
		Run: func(c *cobra.Command, args []string) {
			err := clientStatusConfig.Status(f, out)
			if err != nil {
				if err.Error() != "" {
					PrintError(err, out)
				}
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVar(&clientStatusConfig.DockerMachine, "docker-machine", "", "Specify the Docker machine to use")
	return cmd
}

// ClientStatusConfig is the configuration for the client status command
type ClientStatusConfig struct {
	DockerMachine string
}

func getConfigFromContainer(client dockerhelper.Interface) (*config.MasterConfig, error) {
	serverConfigPath := "/var/lib/origin/openshift.local.config"
	serverMasterConfig := serverConfigPath + "/master/master-config.yaml"
	r, err := dockerhelper.StreamFileFromContainer(client, openshift.ContainerName, serverMasterConfig)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	masterConfig := &config.MasterConfig{}
	err = configapilatest.ReadYAMLInto(data, masterConfig)
	if err != nil {
		return nil, err
	}
	return masterConfig, nil
}

// Status prints the OpenShift cluster status
func (c *ClientStatusConfig) Status(f *clientcmd.Factory, out io.Writer) error {
	dockerClient, err := getDockerClient(out, c.DockerMachine, false)
	if err != nil {
		return errors.ErrNoDockerClient(err)
	}
	helper := dockerhelper.NewHelper(dockerClient)

	container, running, err := helper.GetContainerState(openshift.ContainerName)
	if err != nil {
		return errors.NewError("cannot get state of OpenShift container %s", openshift.ContainerName).WithCause(err)
	}

	if !running {
		return errors.NewError("OpenShift cluster is not running")
	}

	healthy, err := isHealthy(f)
	if err != nil {
		return err
	}
	if !healthy {
		return errors.NewError("OpenShift cluster health check failed")
	}

	masterConfig, err := getConfigFromContainer(dockerClient)
	if err != nil {
		return err
	}

	fmt.Fprint(out, status(container, masterConfig))

	notReady := 0

	eh := exec.NewExecHelper(dockerClient, openshift.ContainerName)

	stdout, _, _ := eh.Command("oc", "get", "dc", "docker-registry", "-n", "default", "-o", "template", "--template", "{{.status.availableReplicas}}").Output()
	if stdout != "1" {
		fmt.Fprintln(out, "Notice: Docker registry is not yet ready")
		notReady++
	}

	stdout, _, _ = eh.Command("oc", "get", "dc", "router", "-n", "default", "-o", "template", "--template", "{{.status.availableReplicas}}").Output()
	if stdout != "1" {
		fmt.Fprintln(out, "Notice: Router is not yet ready")
		notReady++
	}

	stdout, _, _ = eh.Command("oc", "get", "job", "persistent-volume-setup", "-n", "default", "-o", "template", "--template", "{{.status.succeeded}}").Output()
	if stdout != "1" {
		fmt.Fprintln(out, "Notice: Persistent volumes are not yet ready")
		notReady++
	}

	stdout, _, _ = eh.Command("oc", "get", "is", "-n", "openshift", "-o", "template", "--template", `{{range .items}}{{if not .status.tags}}notready{{end}}{{end}}`).Output()
	if len(stdout) > 0 {
		fmt.Fprintln(out, "Notice: Imagestreams are not yet ready")
		notReady++
	}

	if notReady > 0 {
		fmt.Fprintf(out, "\nNotice: %d OpenShift component(s) are not yet ready (see above)\n", notReady)
		return fmt.Errorf("")
	}

	return nil
}

func isHealthy(f *clientcmd.Factory) (bool, error) {
	client, err := f.RESTClient()
	if err != nil {
		return false, err
	}

	var statusCode int
	client.Client.Timeout = 10 * time.Second
	client.Get().AbsPath("/healthz").Do().StatusCode(&statusCode)
	return statusCode == 200, nil
}

func status(container *types.ContainerJSON, config *config.MasterConfig) string {
	mountMap := make(map[string]string)
	for _, mount := range container.Mounts {
		mountMap[mount.Destination] = mount.Source
	}

	pvDir := ""
	for _, env := range container.Config.Env {
		if strings.HasPrefix(env, "OPENSHIFT_PV_DIR=") {
			pvDir = strings.TrimPrefix(env, "OPENSHIFT_PV_DIR=")
		}
	}

	status := ""
	startedAt, err := time.Parse(time.RFC3339, container.State.StartedAt)
	if err != nil {
		duration := strings.ToLower(units.HumanDuration(time.Since(startedAt)))
		status += fmt.Sprintf("The OpenShift cluster was started %s ago\n\n", duration)
	}

	status = status + fmt.Sprintf("Web console URL: %s\n", config.OAuthConfig.AssetPublicURL)
	status = status + fmt.Sprintf("\n")

	status = status + fmt.Sprintf("Config is at host directory %s\n", mountMap["/var/lib/origin/openshift.local.config"])
	status = status + fmt.Sprintf("Volumes are at host directory %s\n", mountMap["/var/lib/origin/openshift.local.volumes"])
	if len(pvDir) > 0 {
		status = status + fmt.Sprintf("Persistent volumes are at host directory %s\n", pvDir)
	}
	if _, hasKey := mountMap["/var/lib/origin/openshift.local.etcd"]; hasKey {
		status = status + fmt.Sprintf("Data is at host directory %s\n", mountMap["/var/lib/origin/openshift.local.etcd"])
	} else {
		status = status + fmt.Sprintf("Data will be discarded when cluster is destroyed\n")
	}
	status = status + fmt.Sprintf("\n")

	return status
}
