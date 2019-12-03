package kubernetes

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/buger/goterm"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	// RouterBasePort defines what will be the first port used when proxying port-forward
	// to actual simulation nodes. It is then incremented by 1.
	RouterBasePort = 6000

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

type deployer interface {
	CreateDeployment() (watch.Interface, error)
	WaitDeployment(watch.Interface, time.Duration) error
	FetchPods() ([]apiv1.Pod, error)
	DeployRouter([]apiv1.Pod) (watch.Interface, error)
	WaitRouter(watch.Interface) error
	FetchRouter() (apiv1.Pod, error)
	DeleteAll() (watch.Interface, error)
	WaitDeletion(watch.Interface, time.Duration) error
	ReadFromPod(string, string, string) (io.Reader, error)
	MakeTunnel() Tunnel
}

type kubeDeployer struct {
	nodes           []string
	namespace       string
	config          *rest.Config
	client          kubernetes.Interface
	restclient      rest.Interface
	executorFactory func(*rest.Config, string, *url.URL) (remotecommand.Executor, error)

	router  apiv1.Pod
	mapping routerMapping
	pods    []apiv1.Pod
}

func newKubeDeployer(config *rest.Config, ns string, nodes []string) (*kubeDeployer, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("client: %v", err)
	}

	return &kubeDeployer{
		nodes:           nodes,
		namespace:       ns,
		config:          config,
		client:          client,
		restclient:      client.CoreV1().RESTClient(),
		executorFactory: remotecommand.NewSPDYExecutor,
	}, nil
}

func (kd kubeDeployer) CreateDeployment() (watch.Interface, error) {
	fmt.Print("Creating deployment...")

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

	for _, node := range kd.nodes {
		deployment := makeDeployment(node)

		_, err = intf.Create(deployment)
		if err != nil {
			return nil, err
		}
	}

	fmt.Println(goterm.ResetLine("Creating deployment... ok"))

	return w, nil
}

func (kd *kubeDeployer) WaitDeployment(w watch.Interface, timeout time.Duration) error {
	fmt.Print("Waiting deployment...")
	readyMap := make(map[string]struct{})
	tick := time.After(timeout)

	for {
		select {
		case <-tick:
			fmt.Println(goterm.ResetLine("Waiting deployment... failed"))
			return errors.New("timeout")
		case evt := <-w.ResultChan():
			dpl := evt.Object.(*appsv1.Deployment)

			if dpl.Status.AvailableReplicas > 0 {
				readyMap[dpl.Name] = struct{}{}
			}

			if len(readyMap) == len(kd.nodes) {
				fmt.Println(goterm.ResetLine("Waiting deployment... ok"))
				return nil
			}
		}
	}
}

func (kd *kubeDeployer) FetchPods() ([]apiv1.Pod, error) {
	fmt.Printf("Fetching pods...")

	pods, err := kd.client.CoreV1().Pods(kd.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, AppID),
	})

	if err != nil {
		return nil, err
	}

	if len(pods.Items) != len(kd.nodes) {
		return nil, fmt.Errorf("invalid number of pods: %d vs %d", len(pods.Items), len(kd.nodes))
	}

	fmt.Println(goterm.ResetLine("Fetching pods... ok"))
	kd.pods = pods.Items
	return pods.Items, nil
}

func (kd *kubeDeployer) DeployRouter(pods []apiv1.Pod) (watch.Interface, error) {
	fmt.Printf("Deploying the router...")

	intf := kd.client.AppsV1().Deployments(kd.namespace)

	w, err := intf.Watch(metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", LabelID, RouterID)})
	if err != nil {
		return nil, err
	}

	base := int32(RouterBasePort)
	mapping := make(map[string][]portTuple)

	for _, pod := range pods {
		ports := make([]portTuple, 0)

		// expect the first container to be the application.
		for _, port := range pod.Spec.Containers[0].Ports {
			ports = append(ports, portTuple{pod: port.ContainerPort, router: base})
			base++
		}

		mapping[pod.Status.PodIP] = ports
	}

	// Deploy the router that will redirect port forward tunnels to each pod.
	_, err = intf.Create(makeRouterDeployment(mapping))
	if err != nil {
		return nil, err
	}

	fmt.Println(goterm.ResetLine("Deploying the router... ok"))
	kd.mapping = mapping
	return w, nil
}

func (kd *kubeDeployer) WaitRouter(w watch.Interface) error {
	fmt.Printf("Waiting for the router...")
	for {
		select {
		// TODO: timeout
		case evt := <-w.ResultChan():
			dpl := evt.Object.(*appsv1.Deployment)
			if dpl.Status.AvailableReplicas > 0 {
				fmt.Println(goterm.ResetLine("Waiting for the router... ok"))
				return nil
			}
		}
	}
}

func (kd *kubeDeployer) FetchRouter() (apiv1.Pod, error) {
	fmt.Printf("Fetching the router...")

	pods, err := kd.client.CoreV1().Pods(kd.namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelID, RouterID),
	})
	if err != nil {
		return apiv1.Pod{}, err
	}

	if len(pods.Items) != 1 {
		return apiv1.Pod{}, errors.New("invalid number of pods")
	}

	fmt.Println(goterm.ResetLine("Fetching the router... ok"))
	kd.router = pods.Items[0]
	return pods.Items[0], nil
}

func (kd *kubeDeployer) DeleteAll() (watch.Interface, error) {
	fmt.Println("Deleting deployment...")

	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	selector := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelApp, AppName),
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

func (kd *kubeDeployer) WaitDeletion(w watch.Interface, timeout time.Duration) error {
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

			if countDeleted >= len(kd.nodes)+1 {
				// No more replicas so the deployment is deleted.
				return nil
			}
		}
	}
}

func (kd *kubeDeployer) ReadFromPod(podName string, containerName string, srcPath string) (io.Reader, error) {
	reader, outStream := io.Pipe()

	req := kd.restclient.
		Get().
		Namespace(kd.namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		VersionedParams(&apiv1.PodExecOptions{
			Container: containerName,
			Command:   []string{"cat", srcPath},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := kd.executorFactory(kd.config, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	go func() {
		defer outStream.Close()
		err := exec.Stream(remotecommand.StreamOptions{
			Stdin:  os.Stdin,
			Stdout: outStream,
			Stderr: os.Stderr,
			Tty:    false,
		})

		if err != nil {
			reader.CloseWithError(err)
		}
	}()

	return reader, nil
}

func (kd *kubeDeployer) MakeTunnel() Tunnel {
	return newTunnel(kd)
}

func int32Ptr(v int32) *int32 {
	return &v
}

func makeDeployment(node string) *appsv1.Deployment {
	labels := map[string]string{
		LabelApp: AppName,
		LabelID:  AppID,
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
						{
							Name:  "app",
							Image: "dedis/conode:latest",
							Ports: []apiv1.ContainerPort{
								{
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: 7770,
								},
								{
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: 7771,
								},
							},
							Command: []string{"/bin/sh", "-c"},
							Args: []string{
								"/root/conode setup --non-interactive --port 7770 && /root/conode -d 2 server",
							},
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
						},
						{
							Name:  "pumba",
							Image: "gaiaadm/pumba",
							Args: []string{
								"netem",
								"--tc-image",
								"gaiadocker/iproute2",
								"--duration",
								"1h",
								"loss",
								"--percent",
								"30",
								"re2:.*app_simnet-node0.*",
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      "dockersocket",
									MountPath: "/var/run/docker.sock",
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
						{
							Name:            "monitor",
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

func makeRouterDeployment(mapping map[string][]portTuple) *appsv1.Deployment {
	containerPorts := make([]apiv1.ContainerPort, 0)
	args := []string{}
	for ip, tuples := range mapping {
		for _, t := range tuples {
			containerPorts = append(containerPorts, apiv1.ContainerPort{
				Protocol:      apiv1.ProtocolTCP,
				ContainerPort: t.router,
			})

			args = append(args, "--proxy", fmt.Sprintf("%d=%s:%d", t.router, ip, t.pod))
		}
	}

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
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:            "router",
							Image:           "dedis/simnet-router:latest",
							ImagePullPolicy: "Never", // TODO: Remove after the image is pushed to DockerHub.
							Ports:           containerPorts,
							Args:            args,
						},
					},
				},
			},
		},
	}
}
