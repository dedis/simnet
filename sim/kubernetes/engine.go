package kubernetes

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.dedis.ch/simnet/daemon"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"

	"github.com/buger/goterm"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth" // Allows authentication to cloud providers
	"k8s.io/client-go/rest"
)

const (
	// LabelApp is the shared label between the simnet components.
	LabelApp = "go.dedis.ch.app"
	// AppName is the value of the shared label.
	AppName = "simnet"

	// LabelID is the label for each type of component.
	LabelID = "go.dedis.ch.id"
	// AppID is the value of the component label for the simulation application.
	AppID = "simnet-app"
	// RouterID is the value of the component label for the router.
	RouterID = "simnet-router"

	// LabelNode is attached to the node identifier in the topology.
	LabelNode = "go.dedis.ch.node.id"

	// TimeoutRouterDeployment is the amount of time in seconds that the router
	// has to progress to full availability.
	TimeoutRouterDeployment = 300
	// TimeoutAppDeployment is the amount of time in seconds that the app
	// has to progress to full availability.
	TimeoutAppDeployment = 300

	// ClientCertificateDistantPath is the location of the client certificate
	// in the router container.
	ClientCertificateDistantPath = "/etc/openvpn/pki/issued/client1.crt"
	// ClientKeyDistantPath is the location of the client key in the router
	// container.
	ClientKeyDistantPath = "/etc/openvpn/pki/private/client1.key"
	// CADistantPath is the location of the CA certificate in the router
	// container.
	CADistantPath = "/etc/openvpn/pki/ca.crt"

	// MonitorDataFilepath is the location of the monitor data file inside the
	// monitor container.
	MonitorDataFilepath = "/root/data"

	// DefaultNetworkDevice is the name of the network interface that Docker
	// containers have available.
	DefaultNetworkDevice = "eth0"
	// DefaultNetworkMask is the mask used when the router cannot determine
	// the cluster subnet.
	DefaultNetworkMask = "255.255.0.0"
)

var (
	// DeamonRequestMemory is the amount of memory for monitoring containers.
	DeamonRequestMemory = resource.MustParse("8Mi")
	// DeamonRequestCPU is the number of CPU for monitoring containers.
	DeamonRequestCPU = resource.MustParse("10m")
	// DeamonLimitMemory is the maximum amount of memory allocated to monitoring containers in
	// the simulation pods.
	DeamonLimitMemory = resource.MustParse("16Mi")
	// DeamonLimitCPU is the maxmimum number of CPU allocated to monitoring containers in
	// the simulation pods.
	DeamonLimitCPU = resource.MustParse("50m")
	// AppRequestMemory is the amount of memory allocated to app containers in the
	// simulation pods.
	AppRequestMemory = resource.MustParse("32Mi")
	// AppRequestCPU is the number of CPU allocated to app containers in the
	// simulation pods.
	AppRequestCPU = resource.MustParse("50m")
	// AppLimitMemory is the maximum amount of memory allocated to app containers in the
	// simulation pods.
	AppLimitMemory = resource.MustParse("128Mi")
	// AppLimitCPU is the maximum number of CPU allocated to app containers in the
	// simulation pods.
	AppLimitCPU = resource.MustParse("200m")
)

var newClient = kubernetes.NewForConfig

type engine interface {
	CreateDeployment() (watch.Interface, error)
	WaitDeployment(watch.Interface) error
	FetchPods() ([]apiv1.Pod, error)
	UploadConfig() error
	DeployRouter([]apiv1.Pod) (watch.Interface, error)
	WaitRouter(watch.Interface) (*apiv1.ServicePort, string, error)
	FetchCertificates() (sim.Certificates, error)
	DeleteAll() (watch.Interface, error)
	WaitDeletion(watch.Interface, time.Duration) error
	StreamLogs(close <-chan struct{}) error
	ReadStats(pod string, start, end time.Time) (metrics.NodeStats, error)
	ReadFile(pod, path string) (io.Reader, error)
}

type kubeEngine struct {
	writer    io.Writer
	options   *sim.Options
	host      string
	namespace string
	config    *rest.Config
	client    kubernetes.Interface
	fs        fs
	pods      []apiv1.Pod
	wgLogs    sync.WaitGroup
}

func newKubeEngine(config *rest.Config, ns string, options *sim.Options) (*kubeEngine, error) {
	client, err := newClient(config)
	if err != nil {
		return nil, fmt.Errorf("client: %v", err)
	}

	u, err := url.Parse(config.Host)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the host: %v", err)
	}

	return &kubeEngine{
		writer:    os.Stdout,
		options:   options,
		namespace: ns,
		host:      u.Hostname(),
		config:    config,
		client:    client,
		fs: kfs{
			restclient: client.CoreV1().RESTClient(),
			namespace:  ns,
			config:     config,
		},
	}, nil
}

func (kd *kubeEngine) makeContainer() apiv1.Container {
	pp := make([]apiv1.ContainerPort, len(kd.options.Ports))
	for i, port := range kd.options.Ports {
		if port.Protocol() == sim.TCP {
			pp[i] = apiv1.ContainerPort{
				Protocol:      apiv1.ProtocolTCP,
				ContainerPort: port.Value(),
			}
		} else if port.Protocol() == sim.UDP {
			pp[i] = apiv1.ContainerPort{
				Protocol:      apiv1.ProtocolUDP,
				ContainerPort: port.Value(),
			}
		}
	}

	return apiv1.Container{
		Name:    ContainerAppName,
		Image:   kd.options.Image,
		Command: kd.options.Cmd,
		Args:    kd.options.Args,
		Ports:   pp,
		Resources: apiv1.ResourceRequirements{
			Requests: apiv1.ResourceList{
				"memory": AppRequestMemory,
				"cpu":    AppRequestCPU,
			},
			Limits: apiv1.ResourceList{
				"memory": AppLimitMemory,
				"cpu":    AppLimitCPU,
			},
		},
	}
}

func (kd *kubeEngine) CreateDeployment() (watch.Interface, error) {
	fmt.Fprint(kd.writer, "Creating deployment...")

	intf := kd.client.AppsV1().Deployments(kd.namespace)

	opts := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, AppID),
	}

	// Watch for the deployment status so that it can wait for all the containers
	// to have started.
	w, err := intf.Watch(opts)
	if err != nil {
		return nil, err
	}

	for _, node := range kd.options.Topology.GetNodes() {
		deployment := makeDeployment(node.String(), kd.makeContainer())

		_, err = intf.Create(deployment)
		if err != nil {
			return nil, err
		}
	}

	fmt.Fprintln(kd.writer, goterm.ResetLine("Creating deployment... ok"))

	return w, nil
}

func (kd *kubeEngine) WaitDeployment(w watch.Interface) error {
	fmt.Fprint(kd.writer, "Waiting deployment...")
	readyMap := make(map[string]struct{})

	for {
		// Deployments will time out if one of them has not progressed
		// in a given amount of time.
		evt := <-w.ResultChan()
		dpl := evt.Object.(*appsv1.Deployment)

		if dpl.Status.AvailableReplicas > 0 {
			readyMap[dpl.Name] = struct{}{}
		}

		if len(readyMap) == kd.options.Topology.Len() {
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting deployment... ok"))
			return nil
		}

		hasFailure, reason := checkPodFailure(dpl)
		if hasFailure {
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting deployment... failure"))
			return errors.New(reason)
		}
	}
}

func (kd *kubeEngine) FetchPods() ([]apiv1.Pod, error) {
	fmt.Fprintf(kd.writer, "Fetching pods...")

	pods, err := kd.client.CoreV1().Pods(kd.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, AppID),
	})

	if err != nil {
		return nil, err
	}

	if len(pods.Items) != kd.options.Topology.Len() {
		return nil, fmt.Errorf("invalid number of pods: %d vs %d", len(pods.Items), kd.options.Topology.Len())
	}

	fmt.Fprintln(kd.writer, goterm.ResetLine("Fetching pods... ok"))
	kd.pods = pods.Items
	return pods.Items, nil
}

func (kd *kubeEngine) UploadConfig() error {
	mapping := make(map[network.Node]string)
	for _, pod := range kd.pods {
		node := pod.Labels[LabelNode]

		if node != "" {
			mapping[network.Node(node)] = pod.Status.PodIP
		}
	}

	for _, pod := range kd.pods {
		// Logs are written in the stdout of the main process so we get the
		// logs from the Kubernetes drivers.
		writer, _, err := kd.fs.Write(pod.Name, "monitor", []string{"sh", "-c", "./netem -log /proc/1/fd/1 -"})
		if err != nil {
			return err
		}

		node := network.Node(pod.Labels[LabelNode])

		enc := json.NewEncoder(writer)
		err = enc.Encode(kd.options.Topology.Rules(node, mapping))

		// In any case, the writer is closed.
		writer.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

func (kd *kubeEngine) DeployRouter(pods []apiv1.Pod) (watch.Interface, error) {
	fmt.Fprintf(kd.writer, "Deploying the router...")

	intf := kd.client.AppsV1().Deployments(kd.namespace)

	w, err := intf.Watch(metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", LabelID, RouterID)})
	if err != nil {
		return nil, err
	}

	_, err = intf.Create(makeRouterDeployment())
	if err != nil {
		return nil, err
	}

	fmt.Fprintln(kd.writer, goterm.ResetLine("Deploying the router... ok"))
	return w, nil
}

func (kd *kubeEngine) createVPNService() (*apiv1.ServicePort, error) {
	intf := kd.client.CoreV1().Services(kd.namespace)

	w, err := intf.Watch(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	_, err = intf.Create(kd.makeRouterService())
	if err != nil {
		return nil, err
	}

	for {
		evt := <-w.ResultChan()
		serv := evt.Object.(*apiv1.Service)

		for _, port := range serv.Spec.Ports {
			if port.NodePort > 0 {
				return &port, nil
			}
		}
	}
}

func (kd *kubeEngine) WaitRouter(w watch.Interface) (*apiv1.ServicePort, string, error) {
	fmt.Fprintf(kd.writer, "Waiting for the router...")

	for {
		// Deployment will time out after some time if it has not
		// progressed.
		evt := <-w.ResultChan()
		dpl := evt.Object.(*appsv1.Deployment)

		if dpl.Status.AvailableReplicas > 0 {
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting for the router... ok"))
			port, err := kd.createVPNService()
			if err != nil {
				return nil, "", err
			}

			return port, kd.host, nil
		}

		hasFailure, reason := checkPodFailure(dpl)
		if hasFailure {
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting for the router... failure"))
			return nil, "", errors.New(reason)
		}
	}
}

func (kd *kubeEngine) writeCertificates(pod string, certs sim.Certificates) error {
	// Those files are generated by the init container and thus they exist
	// by definition as the router container wouldn't have started if it has
	// failed.
	files := map[string]string{
		certs.Cert: ClientCertificateDistantPath,
		certs.Key:  ClientKeyDistantPath,
		certs.CA:   CADistantPath,
	}

	for out, dst := range files {
		r, err := kd.fs.Read(pod, ContainerRouterName, dst)
		if err != nil {
			return err
		}

		// Create a file that can be read only by the user.
		f, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}

		defer f.Close()

		br := bufio.NewReader(r)
		_, err = br.WriteTo(f)
		if err != nil {
			return err
		}
	}

	return nil
}

func (kd *kubeEngine) FetchCertificates() (sim.Certificates, error) {
	fmt.Fprintf(kd.writer, "Fetching the certificates...")

	// This defines where the files will be written on the client side.
	certs := sim.Certificates{
		CA:   filepath.Join(kd.options.OutputDir, "ca.crt"),
		Key:  filepath.Join(kd.options.OutputDir, "client.key"),
		Cert: filepath.Join(kd.options.OutputDir, "client.crt"),
	}

	pods, err := kd.client.CoreV1().Pods(kd.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, RouterID),
	})
	if err != nil {
		return certs, err
	}

	if len(pods.Items) != 1 {
		return certs, errors.New("missing router pod")
	}

	err = kd.writeCertificates(pods.Items[0].Name, certs)
	if err != nil {
		return certs, err
	}

	fmt.Fprintln(kd.writer, goterm.ResetLine("Fetching the certificates... ok"))
	return certs, nil
}

func (kd *kubeEngine) DeleteAll() (watch.Interface, error) {
	fmt.Fprintln(kd.writer, "Cleaning namespace...")

	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	selector := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelApp, AppName),
	}

	err := kd.deleteService(deleteOptions)
	if err != nil {
		return nil, err
	}

	intf := kd.client.AppsV1().Deployments(kd.namespace)
	w, err := intf.Watch(selector)
	if err != nil {
		return nil, err
	}

	err = intf.DeleteCollection(deleteOptions, selector)
	if err != nil {
		w.Stop()
		return nil, err
	}

	return w, nil
}

func (kd *kubeEngine) deleteService(opts *metav1.DeleteOptions) error {
	intf := kd.client.CoreV1().Services(kd.namespace)

	err := intf.Delete("simnet-router", opts)
	if err != nil {
		return err
	}

	return nil
}

func (kd *kubeEngine) WaitDeletion(w watch.Interface, timeout time.Duration) error {
	tick := time.After(timeout)
	countDeleted := 0

	for {
		select {
		case <-tick:
			return errors.New("timeout")
		case evt := <-w.ResultChan():
			dpl := evt.Object.(*appsv1.Deployment)
			if dpl.Status.AvailableReplicas == 0 && dpl.Status.UnavailableReplicas == 0 {
				countDeleted++
			}

			if countDeleted >= kd.options.Topology.Len()+1 {
				// No more replicas so the deployment is deleted.
				return nil
			}
		}
	}
}

// StreamLogs open a stream for each application container that is attached to
// the stdout and stderr. Those logs are then written in file in the output
// directory.
func (kd *kubeEngine) StreamLogs(close <-chan struct{}) error {
	dir := filepath.Join(kd.options.OutputDir, "logs")

	// Clean the folder to remove old log files.
	err := os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("couldn't clean log folder: %v", err)
	}

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("couldn't create log folder: %v", err)
	}

	kd.wgLogs = sync.WaitGroup{}

	for _, pod := range kd.pods {
		kd.wgLogs.Add(1)

		file, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s.log", pod.Name)))
		if err != nil {
			return fmt.Errorf("couldn't create log file: %v", err)
		}

		req := kd.client.CoreV1().Pods(kd.namespace).GetLogs(pod.Name, &apiv1.PodLogOptions{
			Container: ContainerAppName,
			Follow:    true, // Streaming...
		})

		reader, err := req.Stream()
		if err != nil {
			return fmt.Errorf("couldn't open the stream: %w", err)
		}

		// This Go routine will be done when the stream is closed.
		go func() {
			io.Copy(file, reader)
			kd.wgLogs.Done()
		}()

		go func() {
			<-close
			// Close the stream if the other side is still opened.
			reader.Close()
			file.Close()
		}()
	}

	return nil
}

func (kd *kubeEngine) ReadStats(pod string, start, end time.Time) (metrics.NodeStats, error) {
	reader, err := kd.fs.Read(pod, ContainerMonitorName, MonitorDataFilepath)
	if err != nil {
		return metrics.NodeStats{}, err
	}

	ns := metrics.NewNodeStats(reader, start, end)

	return ns, nil
}

func (kd *kubeEngine) ReadFile(pod, path string) (io.Reader, error) {
	return kd.fs.Read(pod, ContainerAppName, path)
}

func (kd *kubeEngine) String() string {
	return fmt.Sprintf("Kubernetes[%s] @ %s", kd.namespace, kd.config.Host)
}

func checkPodFailure(dpl *appsv1.Deployment) (bool, string) {
	isAvailable := true
	isProgressing := true
	reason := ""

	for _, c := range dpl.Status.Conditions {
		// A deployment failure is detected when the progression has stopped
		// but the availability status failed to be true.
		if c.Type == appsv1.DeploymentProgressing && c.Status == apiv1.ConditionFalse {
			isProgressing = false
			reason = c.Reason
		}

		if c.Type == appsv1.DeploymentAvailable && c.Status == apiv1.ConditionFalse {
			// We announce the failure and return the reason why the progression
			// has stopped.
			isAvailable = false
		}
	}

	return !isAvailable && !isProgressing, reason
}

func int32Ptr(v int32) *int32 {
	return &v
}

func makeDeployment(node string, container apiv1.Container) *appsv1.Deployment {
	labels := map[string]string{
		LabelApp:  AppName,
		LabelID:   AppID,
		LabelNode: node,
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   fmt.Sprintf("simnet-%s", node),
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			ProgressDeadlineSeconds: int32Ptr(TimeoutAppDeployment),
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						container,
						{
							Name:  ContainerMonitorName,
							Image: fmt.Sprintf("dedis/simnet-monitor:%s", daemon.Version),
							// ImagePullPolicy: "Never",
							Args: []string{
								"--container",
								fmt.Sprintf("simnet-%s", node),
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      "dockersocket",
									MountPath: "/var/run/docker.sock",
								},
							},
							SecurityContext: &apiv1.SecurityContext{
								Capabilities: &apiv1.Capabilities{
									Add: []apiv1.Capability{"NET_ADMIN"},
								},
							},
							Resources: apiv1.ResourceRequirements{
								Requests: apiv1.ResourceList{
									"memory": DeamonRequestMemory,
									"cpu":    DeamonRequestCPU,
								},
								Limits: apiv1.ResourceList{
									"memory": DeamonLimitMemory,
									"cpu":    DeamonLimitCPU,
								},
							},
						},
					},
					Volumes: []apiv1.Volume{
						{
							Name: "dockersocket",
							VolumeSource: apiv1.VolumeSource{
								HostPath: &apiv1.HostPathVolumeSource{
									Path: "/var/run/docker.sock",
								},
							},
						},
					},
				},
			},
		},
	}
}

func makeRouterDeployment() *appsv1.Deployment {
	labels := map[string]string{
		LabelApp: AppName,
		LabelID:  RouterID,
	}

	// This is necessary in environment where the ip forward is not enabled
	// by the host. It allows the container to enable it for the namespace
	// and thus redirecting the traffic to the cluster LAN.
	privileged := true

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "simnet-router",
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			ProgressDeadlineSeconds: int32Ptr(TimeoutRouterDeployment),
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					InitContainers: []apiv1.Container{
						// The initialization container will generate the
						// different keys that will be used for the session.
						{
							Name:  "router-init",
							Image: fmt.Sprintf("dedis/simnet-router-init:%s", daemon.Version),
							// ImagePullPolicy: "Never",
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      "openvpn",
									MountPath: "/etc/openvpn",
								},
							},
						},
					},
					Containers: []apiv1.Container{
						{
							Name:  ContainerRouterName,
							Image: fmt.Sprintf("dedis/simnet-router:%s", daemon.Version),
							// ImagePullPolicy: "Never",
							Env: []apiv1.EnvVar{
								// Environment variables used in the startup
								// script of the router.
								{Name: "NETDEV", Value: DefaultNetworkDevice},
								{Name: "DEFAULT_MASK", Value: DefaultNetworkMask},
							},
							Ports: []apiv1.ContainerPort{
								{
									ContainerPort: 1194,
									Protocol:      apiv1.ProtocolUDP,
								},
							},
							SecurityContext: &apiv1.SecurityContext{
								Privileged: &privileged,
								Capabilities: &apiv1.Capabilities{
									Add: []apiv1.Capability{"NET_ADMIN"},
								},
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      "openvpn",
									MountPath: "/etc/openvpn",
								},
							},
						},
					},
					Volumes: []apiv1.Volume{
						{
							Name: "openvpn",
							VolumeSource: apiv1.VolumeSource{
								HostPath: &apiv1.HostPathVolumeSource{
									Path: "/etc/openvpn",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (kd *kubeEngine) makeRouterService() *apiv1.Service {
	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "simnet-router",
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{
				{
					Port:     1194,
					Protocol: apiv1.ProtocolUDP,
				},
			},
			// NodePort type is selected so that a connection to the VPN can
			// be established by using the host/IP taken from the configuration.
			Type: apiv1.ServiceTypeNodePort,
			Selector: map[string]string{
				LabelID: RouterID,
			},
		},
	}
}
