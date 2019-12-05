package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/strategies"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// Options contains the different options for a simulation execution.
type Options struct {
	files map[interface{}]FileMapper
}

// NewOptions creates empty options.
func NewOptions(opts []Option) *Options {
	o := &Options{
		files: make(map[interface{}]FileMapper),
	}

	for _, f := range opts {
		f(o)
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

// Strategy is a simulation strategy that will deploy simulation nodes on Kubernetes.
type Strategy struct {
	engine      engine
	namespace   string
	options     *Options
	pods        []apiv1.Pod
	executeTime time.Time
	doneTime    time.Time
}

// NewStrategy creates a new simulation engine.
func NewStrategy(cfg string, opts ...Option) (*Strategy, error) {
	config, err := clientcmd.BuildConfigFromFlags("", cfg)
	if err != nil {
		return nil, fmt.Errorf("config: %v", err)
	}

	nodes := make([]string, 5)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node%d", i)
	}

	deployer, err := newKubeDeployer(config, "default", nodes)
	if err != nil {
		return nil, err
	}

	return &Strategy{
		engine:    deployer,
		namespace: "default",
		options:   NewOptions(opts),
	}, nil
}

// Deploy will create a deployment on the Kubernetes cluster. A pod will then
// be assigned to simulation nodes.
func (s *Strategy) Deploy() error {
	w, err := s.engine.CreateDeployment()
	if err != nil {
		return err
	}

	err = s.engine.WaitDeployment(w, 300*time.Second)
	w.Stop()
	if err != nil {
		return err
	}

	pods, err := s.engine.FetchPods()
	if err != nil {
		return err
	}

	s.pods = pods

	w, err = s.engine.DeployRouter(pods)
	if err != nil {
		return err
	}

	err = s.engine.WaitRouter(w)
	w.Stop()
	if err != nil {
		return err
	}

	_, err = s.engine.FetchRouter()
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
			reader, err := s.engine.ReadFromPod(pod.Name, "app", fm.Path)
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
func (s *Strategy) Execute(round strategies.Round) error {
	ctx, err := s.makeContext()
	if err != nil {
		return err
	}

	s.executeTime = time.Now()

	round.Execute(ctx, s.engine.MakeTunnel())

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
		reader, err := s.engine.ReadFromPod(pod.Name, "monitor", "/root/data")
		if err != nil {
			return err
		}

		stats.Nodes[pod.Name] = metrics.NewNodeStats(reader)
	}

	f, err := os.Create(filepath)
	if err != nil {
		return err
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
func (s *Strategy) Clean() error {
	w, err := s.engine.DeleteAll()
	if err != nil {
		return err
	}

	defer w.Stop()

	err = s.engine.WaitDeletion(w, 60*time.Second)
	if err != nil {
		return err
	}

	return nil
}
