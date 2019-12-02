package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/buger/goterm"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

var (
	// RouterBasePort defines what will be the first port used when proxying port-forward
	// to actual simulation nodes. It is then incremented by 1.
	RouterBasePort = 6000

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

type portTuple struct {
	pod    int32
	router int32
}

type routerMapping map[string][]portTuple

// KubernetesEngine is a simulation engine that will deploy simulation nodes on Kubernetes.
type KubernetesEngine struct {
	nodes       []string
	namespace   string
	config      *rest.Config
	clientset   *kubernetes.Clientset
	options     *Options
	pods        []apiv1.Pod
	router      apiv1.Pod
	mapping     routerMapping
	executeTime time.Time
	doneTime    time.Time
}

// NewKubernetesEngine creates a new simulation engine.
func NewKubernetesEngine(cfg string, opts ...Option) (*KubernetesEngine, error) {
	config, err := clientcmd.BuildConfigFromFlags("", cfg)
	if err != nil {
		return nil, fmt.Errorf("config: %v", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("client: %v", err)
	}

	nodes := make([]string, 5)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node%d", i)
	}

	return &KubernetesEngine{
		nodes:     nodes,
		config:    config,
		clientset: client,
		namespace: "default",
		mapping:   make(routerMapping),
		options:   NewOptions(opts),
	}, nil
}

// Deploy will create a deployment on the Kubernetes cluster. A pod will then
// be assigned to simulation nodes.
func (e *KubernetesEngine) Deploy() error {
	fmt.Print("Creating deployment...")
	d := e.clientset.AppsV1().Deployments(e.namespace)

	// Watch for the deployment status so that it can wait for all the containers
	// to have started.
	w, err := d.Watch(metav1.ListOptions{})
	if err != nil {
		return err
	}

	defer w.Stop()

	for _, node := range e.nodes {
		deployment := makeDeployment(node)

		_, err = d.Create(deployment)
		if err != nil {
			return err
		}
	}

	fmt.Println(" ok")

	fmt.Print("Wait deployment...")
	timeout := time.After(300 * time.Second)
	readyMap := make(map[string]struct{})
	routerDone := false

	for {
		select {
		case <-timeout:
			fmt.Println(goterm.ResetLine("Wait deployment... failed"))
			return errors.New("timeout during deployment")
		case evt := <-w.ResultChan():
			dpl := evt.Object.(*appsv1.Deployment)

			for _, cond := range dpl.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == apiv1.ConditionTrue {
					readyMap[dpl.Name] = struct{}{}
				} else if cond.Type == appsv1.DeploymentProgressing && cond.Status == apiv1.ConditionTrue {
					// If the condition Available is not true, the Progressing message is shown.
					fmt.Print(goterm.ResetLine(fmt.Sprintf("Wait deployment... %s", cond.Message)))
				}

				if len(readyMap) == len(e.nodes) && !routerDone {
					routerDone = true

					fmt.Printf(goterm.ResetLine("Wait deployment... fetching the pods"))
					err = e.fetchPods()
					if err != nil {
						return err
					}

					err = e.deployRouter()
					if err != nil {
						return err
					}

					// Now wait for the router deployment..
				} else if len(readyMap) == len(e.nodes)+1 {
					err = e.fetchRouter()
					if err != nil {
						return err
					}

					fmt.Println(goterm.ResetLine("Wait deployment... ok"))
					return nil
				}
			}
		}
	}
}

func (e *KubernetesEngine) makeContext() (context.Context, error) {
	ctx := context.Background()

	for key, fm := range e.options.files {
		files := make(map[string]interface{})

		for _, pod := range e.pods {
			reader, err := e.readFromPod(pod.Name, "app", fm.Path)
			if err != nil {
				return nil, err
			}

			files[pod.Status.PodIP], err = fm.Mapper(reader)
			if err != nil {
				return nil, err
			}
		}

		ctx = context.WithValue(ctx, key, files)
	}

	return ctx, nil
}

// Execute uses the round implementation to execute a simulation round.
func (e *KubernetesEngine) Execute(round Round) error {
	ctx, err := e.makeContext()
	if err != nil {
		return err
	}

	e.executeTime = time.Now()

	round.Execute(ctx, KubernetesTunnel{
		config:    e.config,
		namespace: e.namespace,
		router:    e.router,
		mapping:   e.mapping,
		pods:      e.pods,
	})

	e.doneTime = time.Now()

	return nil
}

// WriteStats fetches the stats of the nodes then write them into a JSON
// formatted file.
func (e *KubernetesEngine) WriteStats(filepath string) error {
	stats := Stats{
		Timestamp: e.executeTime.Unix(),
		Nodes:     make(map[string]NodeStats),
	}

	f, err := os.Create(filepath)
	if err != nil {
		return err
	}

	for _, pod := range e.pods {
		reader, err := e.readFromPod(pod.Name, "monitor", "/root/data")
		if err != nil {
			return err
		}

		stats.Nodes[pod.Name] = NewNodeStats(reader)
	}

	enc := json.NewEncoder(f)
	err = enc.Encode(&stats)
	if err != nil {
		return err
	}

	err = f.Close()
	if err != nil {
		return err
	}

	return nil
}

// Clean removes any resource created for the simulation.
func (e *KubernetesEngine) Clean() error {
	fmt.Println("Deleting deployment...")
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	d := e.clientset.AppsV1().Deployments(e.namespace)
	w, err := d.Watch(metav1.ListOptions{})
	if err != nil {
		return err
	}

	defer w.Stop()

	for _, node := range e.nodes {
		err = d.Delete("simnet-"+node, deleteOptions)
		if err != nil {
			return err
		}
	}

	err = d.Delete("simnet-router", deleteOptions)
	if err != nil {
		return err
	}

	timeout := time.After(30 * time.Second)
	countDeleted := 0

	for {
		select {
		case <-timeout:
			return errors.New("timeout during deletion")
		case evt := <-w.ResultChan():
			dpl := evt.Object.(*appsv1.Deployment)
			if dpl.Status.AvailableReplicas == 0 && dpl.Status.UnavailableReplicas == 0 {
				countDeleted++
			}

			if countDeleted >= len(e.nodes)+1 {
				// No more replicas so the deployment is deleted.
				return nil
			}
		}
	}
}

func (e *KubernetesEngine) fetchPods() error {
	pods, err := e.clientset.CoreV1().
		Pods(e.namespace).
		List(metav1.ListOptions{LabelSelector: "app=simnet"})
	if err != nil {
		return err
	}

	e.pods = pods.Items
	return nil
}

func (e *KubernetesEngine) deployRouter() error {
	d := e.clientset.AppsV1().Deployments(e.namespace)

	base := int32(RouterBasePort)

	for _, pod := range e.pods {
		ports := make([]portTuple, 0)

		// expect the first container to be the application.
		for _, port := range pod.Spec.Containers[0].Ports {
			ports = append(ports, portTuple{pod: port.ContainerPort, router: base})
			base++
		}

		e.mapping[pod.Status.PodIP] = ports
	}

	// Deploy the router that will redirect port forward tunnels to each pod.
	_, err := d.Create(e.makeRouterDeployment())
	if err != nil {
		return err
	}

	return nil
}

func (e *KubernetesEngine) fetchRouter() error {
	pods, err := e.clientset.CoreV1().
		Pods(e.namespace).
		List(metav1.ListOptions{LabelSelector: "app=simnet-router"})
	if err != nil {
		return err
	}

	if len(pods.Items) != 1 {
		return errors.New("invalid number of pods")
	}

	e.router = pods.Items[0]
	return nil
}

func (e *KubernetesEngine) readFromPod(podName string, containerName string, srcPath string) (io.Reader, error) {
	reader, outStream := io.Pipe()

	req := e.clientset.CoreV1().RESTClient().
		Get().
		Namespace(e.namespace).
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

	exec, err := remotecommand.NewSPDYExecutor(e.config, "POST", req.URL())
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
			fmt.Printf("Stream error: %v\n", err)
		}
	}()

	return reader, nil
}

func int32Ptr(v int32) *int32 {
	return &v
}

func makeDeployment(node string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "simnet-" + node,
			Labels: map[string]string{
				"app": "simnet",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "simnet-" + node,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":  "simnet",
						"name": "simnet-" + node,
					},
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

func (e *KubernetesEngine) makeRouterDeployment() *appsv1.Deployment {
	containerPorts := make([]apiv1.ContainerPort, 0)
	args := []string{}
	for ip, tuples := range e.mapping {
		for _, t := range tuples {
			containerPorts = append(containerPorts, apiv1.ContainerPort{
				Protocol:      apiv1.ProtocolTCP,
				ContainerPort: t.router,
			})

			args = append(args, "--proxy", fmt.Sprintf("%d=%s:%d", t.router, ip, t.pod))
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "simnet-router",
			Labels: map[string]string{
				"app": "simnet",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "simnet-router",
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "simnet-router",
					},
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
