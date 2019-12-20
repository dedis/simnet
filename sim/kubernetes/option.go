package kubernetes

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.dedis.ch/simnet/network"
	apiv1 "k8s.io/api/core/v1"
)

// Options contains the different options for a simulation execution.
type Options struct {
	files     map[interface{}]FileMapper
	topology  network.Topology
	container apiv1.Container
	output    string
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

	if o.output == "" {
		homeDir, err := userHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Couldn't get the user home directory: %v\n", err)
			// Default to relative folder if the home directory cannot be found.
			homeDir = ""
		}

		o.output = filepath.Join(homeDir, ".config", "simnet")
	}

	return o
}

// Option is a function that changes the global options.
type Option func(opts *Options)

// WithOutput is an option to change the default directory that will be used
// to write data about the simulation.
func WithOutput(dir string) Option {
	return func(opts *Options) {
		opts.output = dir
	}
}

// FilesKey is the kind of key that will be used to retrieve the files
// inside the execution context.
type FilesKey string

// Identifier stores information about a node that can uniquely identify it.
type Identifier struct {
	Index int
	ID    network.Node
	IP    string
}

func (id Identifier) String() string {
	return fmt.Sprintf("%s@%s", id.ID, id.IP)
}

// Files is the structure stored in the context for the file mappers.
type Files map[Identifier]interface{}

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
