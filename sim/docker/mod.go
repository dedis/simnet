package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
)

const (
	// ContainerStopTimeout is the maximum amount of time given to a container
	// to stop.
	ContainerStopTimeout = 10 * time.Second
)

// Strategy implements the strategy interface for running simulations inside a
// Docker environment.
type Strategy struct {
	out        io.Writer
	cli        client.APIClient
	options    *sim.Options
	containers []types.ContainerJSON
	stats      *metrics.Stats
	statsLock  sync.Mutex
}

// NewStrategy creates a docker strategy for simulations.
func NewStrategy(opts ...sim.Option) (*Strategy, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	return &Strategy{
		out:        os.Stdout,
		cli:        cli,
		options:    sim.NewOptions(opts),
		containers: make([]types.ContainerJSON, 0),
		stats: &metrics.Stats{
			Nodes: make(map[string]metrics.NodeStats),
		},
	}, nil
}

func (s *Strategy) pullImage(ctx context.Context) error {
	ref := fmt.Sprintf("docker.io/%s", s.options.Image)

	reader, err := s.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	io.Copy(s.out, reader) // ignore potential errors.

	return nil
}

func (s *Strategy) createContainer(ctx context.Context) error {
	ports := nat.PortSet{}
	for _, port := range s.options.Ports {
		// TODO: handle protocol
		ports[nat.Port(fmt.Sprintf("%d", port.Value()))] = struct{}{}
	}

	cfg := &container.Config{
		Image:        s.options.Image,
		Cmd:          append(append([]string{}, s.options.Cmd...), s.options.Args...),
		ExposedPorts: ports,
	}

	for _, node := range s.options.Topology.GetNodes() {
		resp, err := s.cli.ContainerCreate(ctx, cfg, nil, nil, node.String())
		if err != nil {
			return err
		}

		err = s.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
		if err != nil {
			return err
		}

		container, err := s.cli.ContainerInspect(ctx, resp.ID)
		if err != nil {
			return err
		}

		s.containers = append(s.containers, container)
	}

	return nil
}

// Deploy pulls the application image and starts a container per node.
func (s *Strategy) Deploy() error {
	ctx := context.Background()

	err := s.pullImage(ctx)
	if err != nil {
		return fmt.Errorf("couldn't pull the image: %v", err)
	}

	err = s.createContainer(ctx)
	if err != nil {
		return fmt.Errorf("couldn't create the container: %v", err)
	}

	time.Sleep(1 * time.Second)

	fmt.Fprintln(s.out, "Deployment done.")

	return nil
}

func (s *Strategy) makeExecutionContext() (context.Context, error) {
	ctx := context.Background()

	for key, fm := range s.options.Files {
		files := make(sim.Files)

		for i, container := range s.containers {
			reader, _, err := s.cli.CopyFromContainer(ctx, container.ID, "/root/.config/conode/private.toml")
			if err != nil {
				return nil, fmt.Errorf("couldn't copy file: %v", err)
			}

			tr := tar.NewReader(reader)
			_, err = tr.Next()
			if err != nil {
				return nil, fmt.Errorf("couldn't untar: %v", err)
			}

			ident := sim.Identifier{
				Index: i,
				ID:    network.Node(container.Name),
				IP:    container.NetworkSettings.DefaultNetworkSettings.IPAddress,
			}

			files[ident], err = fm.Mapper(tr)
			if err != nil {
				return nil, fmt.Errorf("mapper failed: %v", err)
			}

			reader.Close()
		}

		ctx = context.WithValue(ctx, key, files)
	}

	return ctx, nil
}

func (s *Strategy) monitorContainer(ctx context.Context, container types.ContainerJSON) (io.ReadCloser, error) {
	resp, err := s.cli.ContainerStats(ctx, container.ID, true)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(resp.Body)

	ns := &metrics.NodeStats{}

	go func() {
		for {
			data := &types.StatsJSON{}
			err := dec.Decode(data)
			if err != nil {
				return
			}

			ns.Timestamps = append(ns.Timestamps, time.Now().Unix())
			ns.RxBytes = append(ns.RxBytes, data.Networks["eth0"].RxBytes)
			ns.TxBytes = append(ns.TxBytes, data.Networks["eth0"].TxBytes)
			ns.CPU = append(ns.CPU, data.CPUStats.CPUUsage.TotalUsage)
			ns.Memory = append(ns.Memory, data.MemoryStats.Usage)

			s.statsLock.Lock()
			s.stats.Nodes[container.Name] = *ns
			s.statsLock.Unlock()
		}
	}()

	return resp.Body, nil
}

// Execute takes the round and execute it against the context created from the
// options.
func (s *Strategy) Execute(round sim.Round) error {
	ctx, err := s.makeExecutionContext()
	if err != nil {
		return fmt.Errorf("couldn't create the context: %v", err)
	}

	closers := make([]io.ReadCloser, 0, len(s.containers))

	// It's important to close any existing stream even if an error
	// occurred.
	defer func() {
		for _, closer := range closers {
			closer.Close()
		}
	}()

	for _, container := range s.containers {
		closer, err := s.monitorContainer(context.Background(), container)
		if err != nil {
			return err
		}

		closers = append(closers, closer)
	}

	err = round.Execute(ctx)
	if err != nil {
		return fmt.Errorf("couldn't execute: %v", err)
	}

	fmt.Fprintln(s.out, "Execution done.")

	return nil
}

// WriteStats writes the statistics of the nodes to the file.
func (s *Strategy) WriteStats(filename string) error {
	s.statsLock.Lock()
	defer s.statsLock.Unlock()

	file, err := os.Create(filename)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	err = enc.Encode(s.stats)
	if err != nil {
		return fmt.Errorf("couldn't encode the stats: %v", err)
	}

	fmt.Fprintln(s.out, "Statistics written.")

	return nil
}

// Clean stops and removes all the containers created by the simulation.
func (s *Strategy) Clean() error {
	ctx := context.Background()
	errs := []error{}

	timeout := ContainerStopTimeout

	for _, container := range s.containers {
		err := s.cli.ContainerStop(ctx, container.ID, &timeout)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = s.cli.ContainerRemove(ctx, container.ID, types.ContainerRemoveOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("couldn't remove the containers: %v", errs)
	}

	fmt.Fprintln(s.out, "Cleaning done.")

	return nil
}
