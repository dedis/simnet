package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.dedis.ch/simnet/metrics"
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

var userHomeDir = os.UserHomeDir
var newClientConfig = clientcmd.NewNonInteractiveDeferredLoadingClientConfig

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

	// Create the directory where the data will be stored.
	err := os.MkdirAll(options.output, 0755)
	if err != nil {
		return nil, fmt.Errorf("couldn't create the data folder: %v", err)
	}

	clientcfg := newClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: cfg},
		&clientcmd.ConfigOverrides{},
	)

	restcfg, err := clientcfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("config: %v", err)
	}

	namespace, _, err := clientcfg.Namespace()
	if err != nil {
		return nil, fmt.Errorf("couldn't read the namespace: %v", err)
	}

	engine, err := newKubeEngine(restcfg, namespace, options)
	if err != nil {
		return nil, err
	}

	return &Strategy{
		engine:      engine,
		tun:         sim.NewDefaultTunnel(options.output),
		namespace:   namespace,
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

	port, host, err := s.engine.WaitRouter(w)
	w.Stop()
	if err != nil {
		return err
	}

	certificates, err := s.engine.FetchCertificates()
	if err != nil {
		return err
	}

	err = s.tun.Start(
		sim.WithPort(port.NodePort),
		sim.WithHost(host),
		sim.WithCertificate(certificates),
	)
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
func (s *Strategy) WriteStats(filename string) error {
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

	f, err := os.Create(filepath.Join(s.options.output, filename))
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

func (s *Strategy) String() string {
	return fmt.Sprintf("%v", s.engine)
}
