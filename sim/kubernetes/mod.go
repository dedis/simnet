package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// ContainerAppName is the name of the container where the application
	// will be deployed.
	ContainerAppName = "app"
	// ContainerMonitorName is the name of the container where the monitor
	// will be deployed.
	ContainerMonitorName = "monitor"
	// ContainerRouterName is the name of the container where the router
	// will be deployed.
	ContainerRouterName = "router"
)

// Options contains the different options for a simulation execution.
type Options struct {
	files     map[interface{}]FileMapper
	topology  network.Topology
	container apiv1.Container
}

// NewOptions creates empty options.
func NewOptions(opts []Option) *Options {
	o := &Options{
		files: make(map[interface{}]FileMapper),
	}

	for _, f := range opts {
		f(o)
	}

	if o.topology == nil {
		o.topology = network.NewSimpleTopology(3, 0)
	}

	return o
}

// Option is a function that changes the global options.
type Option func(opts *Options)

// FilesKey is the kind of key that will be used to retrieve the files
// inside the execution context.
type FilesKey string

// FileMapper gives a file path and a map function so that the engine can read
// a file from the simulation node and map the content to a generic instance.
type FileMapper struct {
	Path   string
	Mapper func(io.Reader) (interface{}, error)
}

// WithFileMapper is an option for simulation engines to read files on the
// simulation nodes and convert the content. It will then be available in the
// execution context.
func WithFileMapper(key FilesKey, fm FileMapper) Option {
	return func(opts *Options) {
		opts.files[key] = fm
	}
}

// WithTopology is an option for simulation engines to use the topology when
// deploying the nodes.
func WithTopology(topo network.Topology) Option {
	return func(opts *Options) {
		opts.topology = topo
	}
}

// Port is a parameter for the container image to open a port with a given
// protocol. It can also be a range.
type Port interface {
	asContainerPort() apiv1.ContainerPort
}

// TCP is a single TCP port.
type TCP struct {
	port int32
}

// NewTCP creates a new tcp port.
func NewTCP(port int32) TCP {
	return TCP{port: port}
}

func (tcp TCP) asContainerPort() apiv1.ContainerPort {
	return apiv1.ContainerPort{
		Protocol:      apiv1.ProtocolTCP,
		ContainerPort: tcp.port,
	}
}

// UDP is a single udp port.
type UDP struct {
	port int32
}

// NewUDP creates a new udp port.
func NewUDP(port int32) UDP {
	return UDP{port: port}
}

func (udp UDP) asContainerPort() apiv1.ContainerPort {
	return apiv1.ContainerPort{
		Protocol:      apiv1.ProtocolUDP,
		ContainerPort: udp.port,
	}
}

// WithImage is an option for simulation engines to use this Docker image as
// the base application to run.
func WithImage(image string, cmd, args []string, ports ...Port) Option {
	pp := make([]apiv1.ContainerPort, len(ports))
	for i, port := range ports {
		pp[i] = port.asContainerPort()
	}

	return func(opts *Options) {
		opts.container = apiv1.Container{
			Name:    ContainerAppName,
			Image:   image,
			Command: cmd,
			Args:    args,
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
}

// Encoder is the interface used to instantiate the encoder that will write
// the statistics into a file.
type Encoder interface {
	Encode(interface{}) error
}

func makeJSONEncoder(w io.Writer) Encoder {
	return json.NewEncoder(w)
}

// Strategy is a simulation strategy that will deploy simulation nodes on Kubernetes.
type Strategy struct {
	engine      engine
	namespace   string
	options     *Options
	pods        []apiv1.Pod
	tun         sim.Tunnel
	executeTime time.Time
	doneTime    time.Time
	makeEncoder func(io.Writer) Encoder
}

// NewStrategy creates a new simulation engine.
func NewStrategy(cfg string, opts ...Option) (*Strategy, error) {
	options := NewOptions(opts)

	config, err := clientcmd.BuildConfigFromFlags("", cfg)
	if err != nil {
		return nil, fmt.Errorf("config: %v", err)
	}

	nodes := make([]string, 5)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node%d", i)
	}

	engine, err := newKubeDeployer(config, "default", options.topology)
	if err != nil {
		return nil, err
	}

	return &Strategy{
		engine:      engine,
		namespace:   "default",
		options:     options,
		makeEncoder: makeJSONEncoder,
	}, nil
}

// Deploy will create a deployment on the Kubernetes cluster. A pod will then
// be assigned to simulation nodes.
func (s *Strategy) Deploy() error {
	w, err := s.engine.CreateDeployment(s.options.container)
	if err != nil {
		return err
	}

	err = s.engine.WaitDeployment(w)
	w.Stop()
	if err != nil {
		return err
	}

	pods, err := s.engine.FetchPods()
	if err != nil {
		return err
	}

	err = s.engine.UploadConfig()
	if err != nil {
		return err
	}

	s.pods = pods

	w, err = s.engine.DeployRouter(pods)
	if err != nil {
		return err
	}

	port, err := s.engine.WaitRouter(w)
	w.Stop()
	if err != nil {
		return err
	}

	vpn, err := s.engine.InitVPN(port)
	if err != nil {
		return err
	}

	s.tun = vpn
	err = vpn.Start()
	if err != nil {
		return err
	}

	return nil
}

func (s *Strategy) makeContext() (context.Context, error) {
	ctx := context.Background()

	for key, fm := range s.options.files {
		files := make(map[string]interface{})

		for _, pod := range s.pods {
			reader, err := s.engine.ReadFile(pod.Name, fm.Path)
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
func (s *Strategy) Execute(round sim.Round) error {
	ctx, err := s.makeContext()
	if err != nil {
		return err
	}

	s.executeTime = time.Now()

	err = round.Execute(ctx)
	if err != nil {
		return err
	}

	s.doneTime = time.Now()

	return nil
}

// WriteStats fetches the stats of the nodes then write them into a JSON
// formatted file.
func (s *Strategy) WriteStats(filepath string) error {
	stats := metrics.Stats{
		Timestamp: s.executeTime.Unix(),
		Nodes:     make(map[string]metrics.NodeStats),
	}

	for _, pod := range s.pods {
		ns, err := s.engine.ReadStats(pod.Name, s.executeTime, s.doneTime)
		if err != nil {
			return err
		}

		stats.Nodes[pod.Name] = ns
	}

	f, err := os.Create(filepath)
	if err != nil {
		return err
	}

	defer f.Close()

	enc := s.makeEncoder(f)
	err = enc.Encode(&stats)
	if err != nil {
		return err
	}

	return nil
}

// Clean removes any resource created for the simulation.
func (s *Strategy) Clean() error {
	errs := make([]error, 0, 2)

	if s.tun != nil {
		err := s.tun.Stop()
		if err != nil {
			errs = append(errs, err)
		}
	}

	w, err := s.engine.DeleteAll()
	if err != nil {
		errs = append(errs, err)
	} else {
		defer w.Stop()

		err = s.engine.WaitDeletion(w, 60*time.Second)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleaning error: %v", errs)
	}

	return nil
}
