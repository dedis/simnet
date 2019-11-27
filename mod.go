package simnet

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// Round is executed during the simulation.
type Round interface {
	Execute(ctx context.Context, tun Tunnel)
}

// Simulation is a Kubernetes simulation.
type Simulation struct {
	config    *rest.Config
	clientset *kubernetes.Clientset
	round     Round
	namespace string
}

// NewSimulation creates a new simulation that can be deployed on Kubernetes.
func NewSimulation(cfg string, r Round) (*Simulation, error) {
	config, err := clientcmd.BuildConfigFromFlags("", cfg)
	if err != nil {
		return nil, fmt.Errorf("config: %v", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("client: %v", err)
	}

	return &Simulation{
		config:    config,
		clientset: client,
		round:     r,
		namespace: "default",
	}, nil
}

func int32Ptr(v int32) *int32 {
	return &v
}

// Deploy creates the simnet deployment in the cluster.
func (sim *Simulation) Deploy() error {
	listwatch := cache.NewListWatchFromClient(sim.clientset.AppsV1().RESTClient(), "deployments", sim.namespace, fields.Everything())
	w, err := listwatch.Watch(metav1.ListOptions{})
	if err != nil {
		return err
	}

	defer w.Stop()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "simnet",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "simnet",
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "simnet",
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
								"delay",
								"--time",
								"100",
								"re2:^/k8s_app",
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      "dockersocket",
									MountPath: "/var/run/docker.sock",
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

	fmt.Print("Creating deployment...")
	d := sim.clientset.AppsV1().Deployments(sim.namespace)
	result, err := d.Create(deployment)
	if err != nil {
		return err
	}

	fmt.Printf(" %s: ok\n", result.GetName())

	fmt.Print("Wait deployment...")
	for {
		evt := <-w.ResultChan()
		dpl := evt.Object.(*appsv1.Deployment)

		for _, cond := range dpl.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == apiv1.ConditionTrue {
				fmt.Println("\nWait deployment... ok")
				return nil
			} else if cond.Type == appsv1.DeploymentProgressing && cond.Status == apiv1.ConditionTrue {
				// If the condition Available is not true, the Progressing message is shown.
				fmt.Printf("\n%s", cond.Message)
			}
		}
	}
}

func (sim *Simulation) Before(filePath string) (map[string]string, error) {
	// Get the pods of the deployment.
	pods, err := sim.clientset.CoreV1().Pods(sim.namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	files := make(map[string]string)

	for _, pod := range pods.Items {
		fmt.Printf("Found pod %s with IP %s\n", pod.Name, pod.Status.PodIP)

		content, err := sim.readFromPod(pod.Name, filePath)
		if err != nil {
			return nil, err
		}

		files[pod.Status.PodIP] = content
	}

	return files, nil
}

// Run uses the round interface to run the simulation.
func (sim *Simulation) Run(ctx context.Context) error {
	pods, err := sim.clientset.CoreV1().Pods(sim.namespace).List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	tun := KubernetesTunnel{
		config:    sim.config,
		namespace: sim.namespace,
		pods:      pods.Items,
	}

	sim.round.Execute(ctx, tun)
	return nil
}

// Clean removes simnet deployment to restore the cluster.
func (sim *Simulation) Clean() error {
	fmt.Println("Deleting deployment...")
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	d := sim.clientset.AppsV1().Deployments(sim.namespace)
	err := d.Delete("simnet", deleteOptions)
	if err != nil {
		return err
	}

	return nil
}

func (sim *Simulation) readFromPod(podName string, srcPath string) (string, error) {
	reader, outStream := io.Pipe()

	req := sim.clientset.CoreV1().RESTClient().
		Get().
		Namespace(sim.namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		VersionedParams(&apiv1.PodExecOptions{
			Container: "app",
			Command:   []string{"cat", srcPath},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(sim.config, "POST", req.URL())
	if err != nil {
		return "", err
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

	buffer, err := ioutil.ReadAll(reader)
	if err != nil {
		return "", err
	}

	return string(buffer), nil
}
