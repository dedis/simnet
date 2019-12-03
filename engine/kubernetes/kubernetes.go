package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"go.dedis.ch/simnet/engine"
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

// Engine is a simulation engine that will deploy simulation nodes on Kubernetes.
type Engine struct {
	deployer    deployer
	namespace   string
	options     *Options
	pods        []apiv1.Pod
	executeTime time.Time
	doneTime    time.Time
}

// NewEngine creates a new simulation engine.
func NewEngine(cfg string, opts ...Option) (*Engine, error) {
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

	return &Engine{
		deployer:  deployer,
		namespace: "default",
		options:   NewOptions(opts),
	}, nil
}

// Deploy will create a deployment on the Kubernetes cluster. A pod will then
// be assigned to simulation nodes.
func (e *Engine) Deploy() error {
	w, err := e.deployer.CreateDeployment()
	if err != nil {
		return err
	}

	err = e.deployer.WaitDeployment(w, 300*time.Second)
	w.Stop()
	if err != nil {
		return err
	}

	pods, err := e.deployer.FetchPods()
	if err != nil {
		return err
	}

	e.pods = pods

	w, err = e.deployer.DeployRouter(pods)
	if err != nil {
		return err
	}

	err = e.deployer.WaitRouter(w)
	w.Stop()
	if err != nil {
		return err
	}

	_, err = e.deployer.FetchRouter()
	if err != nil {
		return err
	}

	return nil
}

func (e *Engine) makeContext() (context.Context, error) {
	ctx := context.Background()

	for key, fm := range e.options.files {
		files := make(map[string]interface{})

		for _, pod := range e.pods {
			reader, err := e.deployer.ReadFromPod(pod.Name, "app", fm.Path)
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
func (e *Engine) Execute(round engine.Round) error {
	ctx, err := e.makeContext()
	if err != nil {
		return err
	}

	e.executeTime = time.Now()

	round.Execute(ctx, e.deployer.MakeTunnel())

	e.doneTime = time.Now()

	return nil
}

// WriteStats fetches the stats of the nodes then write them into a JSON
// formatted file.
func (e *Engine) WriteStats(filepath string) error {
	stats := engine.Stats{
		Timestamp: e.executeTime.Unix(),
		Nodes:     make(map[string]engine.NodeStats),
	}

	for _, pod := range e.pods {
		reader, err := e.deployer.ReadFromPod(pod.Name, "monitor", "/root/data")
		if err != nil {
			return err
		}

		stats.Nodes[pod.Name] = engine.NewNodeStats(reader)
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
func (e *Engine) Clean() error {
	w, err := e.deployer.DeleteAll()
	if err != nil {
		return err
	}

	defer w.Stop()

	err = e.deployer.WaitDeletion(w, 60*time.Second)
	if err != nil {
		return err
	}

	return nil
}
