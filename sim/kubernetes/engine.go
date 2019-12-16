package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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

	// TimeoutRouterDeployment is the amount time in seconds that the router
	// has to progress to full availability.
	TimeoutRouterDeployment = 15
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

type engine interface {
	CreateDeployment(container apiv1.Container) (watch.Interface, error)
	WaitDeployment(watch.Interface, time.Duration) error
	FetchPods() ([]apiv1.Pod, error)
	UploadConfig() error
	DeployRouter([]apiv1.Pod) (watch.Interface, error)
	WaitRouter(watch.Interface) error
	InitVPN() (sim.Tunnel, error)
	DeleteAll() (watch.Interface, error)
	WaitDeletion(watch.Interface, time.Duration) error
	ReadStats(pod string, start, end time.Time) (metrics.NodeStats, error)
	ReadFile(pod, path string) (io.Reader, error)
}

type kubeEngine struct {
	writer    io.Writer
	topology  network.Topology
	namespace string
	config    *rest.Config
	client    kubernetes.Interface
	fs        fs
	pods      []apiv1.Pod
}

func newKubeDeployer(config *rest.Config, ns string, topo network.Topology) (*kubeEngine, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("client: %v", err)
	}

	return &kubeEngine{
		writer:    os.Stdout,
		topology:  topo,
		namespace: ns,
		config:    config,
		client:    client,
		fs: kfs{
			restclient: client.CoreV1().RESTClient(),
			namespace:  ns,
			makeExecutor: func(u *url.URL) (remotecommand.Executor, error) {
				return remotecommand.NewSPDYExecutor(config, "POST", u)
			},
		},
	}, nil
}

func (kd kubeEngine) CreateDeployment(container apiv1.Container) (watch.Interface, error) {
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

	for _, node := range kd.topology.GetNodes() {
		deployment := makeDeployment(node.String(), container)

		_, err = intf.Create(deployment)
		if err != nil {
			return nil, err
		}
	}

	fmt.Fprintln(kd.writer, goterm.ResetLine("Creating deployment... ok"))

	return w, nil
}

func (kd *kubeEngine) WaitDeployment(w watch.Interface, timeout time.Duration) error {
	// TODO: check for errors and failed deployment
	fmt.Fprint(kd.writer, "Waiting deployment...")
	readyMap := make(map[string]struct{})
	tick := time.After(timeout)

	for {
		select {
		case <-tick:
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting deployment... failed"))
			return errors.New("timeout")
		case evt := <-w.ResultChan():
			dpl := evt.Object.(*appsv1.Deployment)

			if dpl.Status.AvailableReplicas > 0 {
				readyMap[dpl.Name] = struct{}{}
			}

			if len(readyMap) == kd.topology.Len() {
				fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting deployment... ok"))
				return nil
			}
		}
	}
}

func (kd *kubeEngine) FetchPods() ([]apiv1.Pod, error) {
	fmt.Fprintf(kd.writer, "Fetching pods...")

	pods, err := kd.client.CoreV1().Pods(kd.namespace).List(metav1.ListOptions{
		// TODO: fetch ready
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, AppID),
	})

	if err != nil {
		return nil, err
	}

	if len(pods.Items) != kd.topology.Len() {
		return nil, fmt.Errorf("invalid number of pods: %d vs %d", len(pods.Items), kd.topology.Len())
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
		err = enc.Encode(kd.topology.Rules(node, mapping))

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

func (kd *kubeEngine) createVPNService() error {
	intf := kd.client.CoreV1().Services(kd.namespace)

	opts, err := kd.makeRouterService()
	if err != nil {
		return err
	}

	_, err = intf.Create(opts)
	if err != nil {
		return err
	}

	return nil
}

func (kd *kubeEngine) WaitRouter(w watch.Interface) error {
	fmt.Fprintf(kd.writer, "Waiting for the router...")

	for {
		// Deployment will time out after some time if it has not
		// progressed.
		evt := <-w.ResultChan()
		dpl := evt.Object.(*appsv1.Deployment)

		if dpl.Status.AvailableReplicas > 0 {
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting for the router... ok"))
			err := kd.createVPNService()
			if err != nil {
				return err
			}
			return nil
		}

		hasFailure, reason := checkPodFailure(dpl)
		if hasFailure {
			fmt.Fprintln(kd.writer, goterm.ResetLine("Waiting for the router... failure"))
			return errors.New(reason)
		}
	}
}

func (kd *kubeEngine) InitVPN() (sim.Tunnel, error) {
	fmt.Fprintf(kd.writer, "Fetching the router...")

	pods, err := kd.client.CoreV1().Pods(kd.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, RouterID),
	})
	if err != nil {
		return nil, err
	}

	if len(pods.Items) != 1 {
		return nil, errors.New("invalid number of pods")
	}

	router := pods.Items[0]
	// Those files are generated by the init container and thus they exist
	// by definition as the app container wouldn't start if it has failed.
	files := []string{
		"/etc/openvpn/pki/issued/client1.crt",
		"/etc/openvpn/pki/private/client1.key",
		"/etc/openvpn/pki/ca.crt",
	}
	readers := make([]io.Reader, 3)

	for i, file := range files {
		r, err := kd.fs.Read(router.Name, ContainerRouterName, file)
		if err != nil {
			return nil, err
		}

		readers[i] = r
	}

	u, err := url.Parse(kd.config.Host)
	if err != nil {
		return nil, err
	}

	tun, err := sim.NewDefaultTunnel(sim.WithHost(u.Hostname()), sim.WithCertificate(readers[2], readers[1], readers[0]))
	if err != nil {
		return nil, err
	}

	fmt.Fprintln(kd.writer, goterm.ResetLine("Fetching the router... ok"))
	return tun, nil
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

			if countDeleted >= kd.topology.Len()+1 {
				// No more replicas so the deployment is deleted.
				return nil
			}
		}
	}
}

func (kd *kubeEngine) ReadStats(pod string, start, end time.Time) (metrics.NodeStats, error) {
	reader, err := kd.fs.Read(pod, "monitor", "/root/data")
	if err != nil {
		return metrics.NodeStats{}, err
	}

	ns := metrics.NewNodeStats(reader, start, end)

	return ns, nil
}

func (kd *kubeEngine) ReadFile(pod, path string) (io.Reader, error) {
	return kd.fs.Read(pod, ContainerAppName, path)
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
			Name:   "simnet-" + node,
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						container,
						{
							Name:            ContainerMonitorName,
							Image:           "dedis/simnet-monitor:latest",
							ImagePullPolicy: "Never", // TODO: Remove after the image is pushed to DockerHub.
							Args: []string{
								"--container",
								"simnet-" + node,
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
							Name:            "router-init",
							Image:           "dedis/simnet-router-init:latest",
							ImagePullPolicy: "Never",
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
							Name:            ContainerRouterName,
							Image:           "dedis/simnet-router:latest",
							ImagePullPolicy: "Never", // TODO: Remove after the image is pushed to DockerHub.
							Ports: []apiv1.ContainerPort{
								{
									ContainerPort: 1194,
									Protocol:      apiv1.ProtocolUDP,
								},
							},
							SecurityContext: &apiv1.SecurityContext{
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

func (kd kubeEngine) makeRouterService() (*apiv1.Service, error) {
	u, err := url.Parse(kd.config.Host)
	if err != nil {
		return nil, err
	}

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
			ExternalIPs: []string{u.Hostname()},
			Selector: map[string]string{
				LabelID: RouterID,
			},
		},
	}, nil
}
