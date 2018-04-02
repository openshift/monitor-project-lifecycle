package openshift

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/homedir"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"

	clientconfig "github.com/openshift/origin/pkg/client/config"
	configapi "github.com/openshift/origin/pkg/cmd/server/apis/config"
	_ "github.com/openshift/origin/pkg/cmd/server/apis/config/install"
	configapilatest "github.com/openshift/origin/pkg/cmd/server/apis/config/latest"
	cmdutil "github.com/openshift/origin/pkg/cmd/util"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/dockerhelper"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/errors"
	dockerexec "github.com/openshift/origin/pkg/oc/bootstrap/docker/exec"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/host"
	"github.com/openshift/origin/pkg/oc/bootstrap/docker/run"
)

const (
	defaultNodeName      = "localhost"
	DefaultDNSPort       = 8053
	DefaultSvcCIDR       = "172.30.0.0/16"
	cmdDetermineNodeHost = "for name in %s; do ls /var/lib/origin/openshift.local.config/node-$name &> /dev/null && echo $name && break; done"

	// TODO: Figure out why cluster up relies on this name
	ContainerName  = "origin"
	Namespace      = "openshift"
	InfraNamespace = "openshift-infra"
)

var (
	BasePorts    = []int{4001, 7001, 8443, 10250, DefaultDNSPort}
	RouterPorts  = []int{80, 443}
	AllPorts     = append(RouterPorts, BasePorts...)
	SocatPidFile = filepath.Join(homedir.HomeDir(), clientconfig.OpenShiftConfigHomeDir, "socat-8443.pid")
)

// Helper contains methods and utilities to help with OpenShift startup
type Helper struct {
	hostHelper        *host.HostHelper
	dockerHelper      *dockerhelper.Helper
	execHelper        *dockerexec.ExecHelper
	runHelper         *run.RunHelper
	image             string
	containerName     string
	routingSuffix     string
	serverIP          string
	version           *semver.Version
	prereleaseVersion *semver.Version
}

// NewHelper creates a new OpenShift helper
func NewHelper(dockerHelper *dockerhelper.Helper, hostHelper *host.HostHelper, image, containerName, routingSuffix string) *Helper {
	return &Helper{
		dockerHelper:  dockerHelper,
		execHelper:    dockerexec.NewExecHelper(dockerHelper.Client(), containerName),
		hostHelper:    hostHelper,
		runHelper:     run.NewRunHelper(dockerHelper),
		image:         image,
		containerName: containerName,
		routingSuffix: routingSuffix,
	}
}

func (h *Helper) TestPorts(ports []int) error {
	_, portData, _, _, err := h.runHelper.New().Image(h.image).
		DiscardContainer().
		Privileged().
		HostNetwork().
		HostPid().
		Entrypoint("/bin/bash").
		Command("-c", "cat /proc/net/tcp && ( [ -e /proc/net/tcp6 ] && cat /proc/net/tcp6 || true)").
		Output()
	if err != nil {
		return errors.NewError("Cannot get TCP port information from Kubernetes host").WithCause(err)
	}
	return checkPortsInUse(portData, ports)
}

func testIPDial(ip string) error {
	// Attempt to connect to test container
	testHost := fmt.Sprintf("%s:8443", ip)
	glog.V(4).Infof("Attempting to dial %s", testHost)
	if err := cmdutil.WaitForSuccessfulDial(false, "tcp", testHost, 200*time.Millisecond, 1*time.Second, 10); err != nil {
		glog.V(2).Infof("Dial error: %v", err)
		return err
	}
	glog.V(4).Infof("Successfully dialed %s", testHost)
	return nil
}

func (h *Helper) TestIP(ip string) error {

	// Start test server on host
	id, err := h.runHelper.New().Image(h.image).
		Privileged().
		HostNetwork().
		Entrypoint("socat").
		Command("TCP-LISTEN:8443,crlf,reuseaddr,fork", "SYSTEM:\"echo 'hello world'\"").Start()
	if err != nil {
		return errors.NewError("cannot start simple server on Docker host").WithCause(err)
	}
	defer func() {
		errors.LogError(h.dockerHelper.StopAndRemoveContainer(id))
	}()
	return testIPDial(ip)
}

func (h *Helper) TestForwardedIP(ip string) error {
	// Start test server on host
	id, err := h.runHelper.New().Image(h.image).
		PortForward(8443, 8443).
		Entrypoint("socat").
		Command("TCP-LISTEN:8443,crlf,reuseaddr,fork", "SYSTEM:\"echo 'hello world'\"").Start()
	if err != nil {
		return errors.NewError("cannot start simple server on Docker host").WithCause(err)
	}
	defer func() {
		errors.LogError(h.dockerHelper.StopAndRemoveContainer(id))
	}()
	return testIPDial(ip)
}

func (h *Helper) DetermineNodeHost(hostConfigDir string, names ...string) (string, error) {
	_, result, _, _, err := h.runHelper.New().Image(h.image).
		DiscardContainer().
		Privileged().
		HostNetwork().
		Entrypoint("/bin/bash").
		Bind(fmt.Sprintf("%s:/var/lib/origin/openshift.local.config", hostConfigDir)).
		Command("-c", fmt.Sprintf(cmdDetermineNodeHost, strings.Join(names, " "))).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

// ServerIP retrieves the Server ip through the openshift start command
func (h *Helper) ServerIP() (string, error) {
	if len(h.serverIP) > 0 {
		return h.serverIP, nil
	}
	_, result, _, _, err := h.runHelper.New().Image(h.image).
		DiscardContainer().
		Privileged().
		HostNetwork().
		Command("start", "--print-ip").Output()
	if err != nil {
		return "", err
	}
	h.serverIP = strings.TrimSpace(result)
	return h.serverIP, nil
}

// OtherIPs tries to find other IPs besides the argument IP for the Docker host
func (h *Helper) OtherIPs(excludeIP string) ([]string, error) {
	_, result, _, _, err := h.runHelper.New().Image(h.image).
		DiscardContainer().
		Privileged().
		HostNetwork().
		Entrypoint("hostname").
		Command("-I").Output()
	if err != nil {
		return nil, err
	}

	candidates := strings.Split(result, " ")
	resultIPs := []string{}
	for _, ip := range candidates {
		if len(strings.TrimSpace(ip)) == 0 {
			continue
		}
		if ip != excludeIP && !strings.Contains(ip, ":") { // for now, ignore IPv6
			resultIPs = append(resultIPs, ip)
		}
	}
	return resultIPs, nil
}

// CheckNodes determines if there is more than one node that corresponds to the
// current machine and removes the one that doesn't match the default node name
func (h *Helper) CheckNodes(kclient kclientset.Interface) error {
	nodes, err := kclient.Core().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return errors.NewError("cannot retrieve nodes").WithCause(err)
	}
	if len(nodes.Items) > 1 {
		glog.V(2).Infof("Found more than one node, will attempt to remove duplicate nodes")
		nodesToRemove := []string{}

		// First, find default node
		defaultNodeMachineId := ""
		for i := 0; i < len(nodes.Items); i++ {
			if nodes.Items[i].Name == defaultNodeName {
				defaultNodeMachineId = nodes.Items[i].Status.NodeInfo.MachineID
				glog.V(5).Infof("machine id for default node is: %s", defaultNodeMachineId)
				break
			}
		}

		for i := 0; i < len(nodes.Items); i++ {
			if nodes.Items[i].Name != defaultNodeName &&
				nodes.Items[i].Status.NodeInfo.MachineID == defaultNodeMachineId {
				glog.V(5).Infof("Found non-default node with duplicate machine id: %s", nodes.Items[i].Name)
				nodesToRemove = append(nodesToRemove, nodes.Items[i].Name)
			}
		}

		for i := 0; i < len(nodesToRemove); i++ {
			glog.V(2).Infof("Deleting extra node %s", nodesToRemove[i])
			err = kclient.Core().Nodes().Delete(nodesToRemove[i], nil)
			if err != nil {
				return errors.NewError("cannot delete duplicate node %s", nodesToRemove[i]).WithCause(err)
			}
		}
	}
	return nil
}

func (h *Helper) OriginLog() string {
	log := h.dockerHelper.ContainerLog(h.containerName, 10)
	if len(log) > 0 {
		return fmt.Sprintf("Last 10 lines of %q container log:\n%s\n", h.containerName, log)
	}
	return fmt.Sprintf("No log available from %q container\n", h.containerName)
}

func (h *Helper) healthzReadyURL(ip string) string {
	return fmt.Sprintf("%s/healthz/ready", h.Master(ip))
}

func (h *Helper) Master(ip string) string {
	return fmt.Sprintf("https://%s:8443", ip)
}

func (h *Helper) GetNodeConfigFromLocalDir(configDir string) (*configapi.NodeConfig, string, error) {
	configPath := filepath.Join(configDir, fmt.Sprintf("node-%s", defaultNodeName), "node-config.yaml")
	glog.V(1).Infof("Reading node config from %s", configPath)
	cfg, err := configapilatest.ReadNodeConfig(configPath)
	if err != nil {
		glog.V(2).Infof("Could not read node config: %v", err)
		return nil, "", err
	}
	return cfg, configPath, nil
}

func (h *Helper) GetConfigFromLocalDir(configDir string) (*configapi.MasterConfig, string, error) {
	configPath := filepath.Join(configDir, "master", "master-config.yaml")
	glog.V(1).Infof("Reading master config from %s", configPath)
	cfg, err := configapilatest.ReadMasterConfig(configPath)
	if err != nil {
		glog.V(2).Infof("Could not read master config: %v", err)
		return nil, "", err
	}
	return cfg, configPath, nil
}

func (h *Helper) ServerVersion() (semver.Version, error) {
	if h.version != nil {
		return *h.version, nil
	}
	version, err := h.ServerPrereleaseVersion()
	if err == nil {
		// ignore pre-release portion
		version.Pre = []semver.PRVersion{}
		h.version = &version
	}
	return version, err
}

func (h *Helper) ServerPrereleaseVersion() (semver.Version, error) {
	if h.prereleaseVersion != nil {
		return *h.prereleaseVersion, nil
	}

	_, versionText, _, _, err := h.runHelper.New().Image(h.image).
		Command("version").
		DiscardContainer().
		Output()
	if err != nil {
		return semver.Version{}, err
	}
	lines := strings.Split(versionText, "\n")
	versionStr := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "openshift") {
			parts := strings.SplitN(line, " ", 2)
			versionStr = strings.TrimLeft(parts[1], "v")
			break
		}
	}
	if len(versionStr) == 0 {
		return semver.Version{}, fmt.Errorf("did not find version in command output: %s", versionText)
	}
	return parseOpenshiftVersion(versionStr)
}

func parseOpenshiftVersion(versionStr string) (semver.Version, error) {
	// The OCP version may have > 4 parts to the version string,
	// e.g. 3.5.1.1-prerelease, whereas Origin will be 3.5.1-prerelease,
	// drop the 4th digit for OCP.
	re := regexp.MustCompile("([0-9]+)\\.([0-9]+)\\.([0-9]+)((?:\\.[0-9]+)+)(.*)")
	versionStr = re.ReplaceAllString(versionStr, "${1}.${2}.${3}${5}")

	return semver.Parse(versionStr)
}

func checkPortsInUse(data string, ports []int) error {
	used := getUsedPorts(data)
	conflicts := []int{}
	for _, port := range ports {
		if _, inUse := used[port]; inUse {
			conflicts = append(conflicts, port)
		}
	}
	if len(conflicts) > 0 {
		return ErrPortsNotAvailable(conflicts)
	}
	return nil
}

func getUsedPorts(data string) map[int]struct{} {
	ports := map[int]struct{}{}
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		// discard lines that don't contain connection data
		if !strings.Contains(parts[0], ":") {
			continue
		}
		glog.V(5).Infof("Determining port in use from: %s", line)
		localAddress := strings.Split(parts[1], ":")
		if len(localAddress) < 2 {
			continue
		}
		state := parts[3]
		if state != "0A" { // only look at connections that are listening
			continue
		}
		port, err := strconv.ParseInt(localAddress[1], 16, 0)
		if err == nil {
			ports[int(port)] = struct{}{}
		}
	}
	glog.V(2).Infof("Used ports in container: %#v", ports)
	return ports
}
