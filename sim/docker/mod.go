package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
)

const (
	// ContainerStopTimeout is the maximum amount of time given to a container
	// to stop.
	ContainerStopTimeout = 2 * time.Second

	// ExecWaitTimeout is the maximum amount of time given to an exec to end.
	ExecWaitTimeout = 10 * time.Second

	// ImageBaseURL is the origin where the images will be pulled.
	ImageBaseURL = "docker.io"
	// ImageMonitor is the path to the docker image that contains the network
	// emulator tool.
	ImageMonitor = "dedis/simnet-monitor:latest"
)

var (
	monitorNetEmulatorCommand = []string{"./netem", "-log", "/dev/stdout"}
)

// Encoder is the function used to encode the statistics in a given format.
type Encoder func(io.Writer, *metrics.Stats) error

func jsonEncoder(writer io.Writer, stats *metrics.Stats) error {
	enc := json.NewEncoder(writer)
	return enc.Encode(stats)
}

var makeDockerClient = func() (client.APIClient, error) {
	return client.NewEnvClient()
}

// Strategy implements the strategy interface for running simulations inside a
// Docker environment.
type Strategy struct {
	out        io.Writer
	cli        client.APIClient
	options    *sim.Options
	containers []types.ContainerJSON
	stats      *metrics.Stats
	statsLock  sync.Mutex
	encoder    Encoder
}

// NewStrategy creates a docker strategy for simulations.
func NewStrategy(opts ...sim.Option) (*Strategy, error) {
	cli, err := makeDockerClient()
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
		encoder: jsonEncoder,
	}, nil
}

func (s *Strategy) pullImage(ctx context.Context) error {
	ref := fmt.Sprintf("docker.io/%s", s.options.Image)

	reader, err := s.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	io.Copy(s.out, reader) // TODO: parse status

	return nil
}

func (s *Strategy) createContainers(ctx context.Context) error {
	ports := nat.PortSet{}
	for _, port := range s.options.Ports {
		key := fmt.Sprintf("%d/%s", port.Value(), port.Protocol())
		ports[nat.Port(key)] = struct{}{}
	}

	cfg := &container.Config{
		Image:        s.options.Image,
		Cmd:          append(append([]string{}, s.options.Cmd...), s.options.Args...),
		ExposedPorts: ports,
	}

	hcfg := &container.HostConfig{
		AutoRemove: true,
	}

	for _, node := range s.options.Topology.GetNodes() {
		resp, err := s.cli.ContainerCreate(ctx, cfg, hcfg, nil, node.String())
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

func waitExec(execID string, msgCh <-chan events.Message, errCh <-chan error, t time.Duration) error {
	timeout := time.After(t)

	for {
		select {
		case msg := <-msgCh:
			if msg.Status == "exec_die" && msg.Actor.Attributes["execID"] == execID {
				code := msg.Actor.Attributes["exitCode"]
				if code != "0" {
					return fmt.Errorf("exit code %s", code)
				}

				return nil
			}
		case err := <-errCh:
			return err
		case <-timeout:
			return errors.New("timeout")
		}
	}
}

func (s *Strategy) configureContainer(
	ctx context.Context,
	c types.ContainerJSON,
	cfg *container.Config,
	mapping map[network.Node]string,
	msgCh <-chan events.Message,
	errCh <-chan error,
) (err error) {
	hcfg := &container.HostConfig{
		AutoRemove:  true,
		CapAdd:      []string{"NET_ADMIN"},
		NetworkMode: container.NetworkMode(fmt.Sprintf("container:%s", c.ID)),
	}

	resp, err := s.cli.ContainerCreate(ctx, cfg, hcfg, nil, "")
	if err != nil {
		return fmt.Errorf("couldn't create netem container: %w", err)
	}

	err = s.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("couldn't start netem container: %w", err)
	}

	rules := s.options.Topology.Rules(network.Node(c.Name[1:]), mapping)

	ecfg := types.ExecConfig{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		// Logs are by default disabled to /dev/null
		Cmd: monitorNetEmulatorCommand,
	}
	exec, err := s.cli.ContainerExecCreate(ctx, resp.ID, ecfg)
	if err != nil {
		return fmt.Errorf("couldn't create exec: %w", err)
	}

	conn, err := s.cli.ContainerExecAttach(ctx, exec.ID, types.ExecConfig{})
	if err != nil {
		return fmt.Errorf("couldn't attach to exec: %w", err)
	}

	defer conn.Close()

	err = s.cli.ContainerExecStart(ctx, exec.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("couldn't start exec: %w", err)
	}

	defer func() {
		timeout := ContainerStopTimeout
		e := s.cli.ContainerStop(ctx, resp.ID, &timeout)
		if e != nil {
			err = fmt.Errorf("couldn't stop the container: %w", e)
		}
	}()

	enc := json.NewEncoder(conn.Conn)
	err = enc.Encode(&rules)
	if err != nil {
		return fmt.Errorf("couldn't encode the rules: %w", err)
	}

	// Output from the monitor container executing the netem tool.
	// io.Copy(ioutil.Discard, conn.Reader)

	err = waitExec(exec.ID, msgCh, errCh, ExecWaitTimeout)
	if err != nil {
		return fmt.Errorf("couldn't apply rule: %w", err)
	}

	return
}

func (s *Strategy) configureContainers(ctx context.Context) error {
	reader, err := s.cli.ImagePull(ctx, "docker.io/dedis/simnet-monitor:latest", types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("couldn't pull netem image: %w", err)
	}

	io.Copy(s.out, reader) // TODO: parse

	cfg := &container.Config{
		Image: ImageMonitor,
		// The container must remain alive so that commands can be executed.
		// TODO: investigate without exec.
		Entrypoint: []string{"sh", "-c", "trap 'trap - TERM; kill -s TERM -- -$$' TERM; tail -f /dev/null && wait"},
	}

	// Create the mapping between node names and IPs.
	mapping := make(map[network.Node]string)
	for _, c := range s.containers {
		mapping[network.Node(c.Name[1:])] = c.NetworkSettings.DefaultNetworkSettings.IPAddress
	}

	// Caller is responsible to cancel the context.
	msgCh, errCh := s.cli.Events(ctx, types.EventsOptions{})

	for i, c := range s.containers {
		fmt.Fprintf(s.out, "Configuring container %d/%d\n", i+1, len(s.containers))

		err = s.configureContainer(ctx, c, cfg, mapping, msgCh, errCh)
		if err != nil {
			return err
		}
	}

	return nil
}

// Deploy pulls the application image and starts a container per node.
func (s *Strategy) Deploy() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.pullImage(ctx)
	if err != nil {
		return fmt.Errorf("couldn't pull the image: %w", err)
	}

	err = s.createContainers(ctx)
	if err != nil {
		return fmt.Errorf("couldn't create the container: %w", err)
	}

	err = s.configureContainers(ctx)
	if err != nil {
		return fmt.Errorf("couldn't configure the containers: %w", err)
	}

	fmt.Fprintln(s.out, "Deployment done.")

	return nil
}

func (s *Strategy) makeExecutionContext() (context.Context, error) {
	ctx := context.Background()

	for key, fm := range s.options.Files {
		files := make(sim.Files)

		for i, container := range s.containers {
			reader, _, err := s.cli.CopyFromContainer(ctx, container.ID, fm.Path)
			if err != nil {
				return nil, fmt.Errorf("couldn't copy file: %w", err)
			}

			tr := tar.NewReader(reader)
			_, err = tr.Next()
			if err != nil {
				return nil, fmt.Errorf("couldn't untar: %w", err)
			}

			ident := sim.Identifier{
				Index: i,
				ID:    network.Node(container.Name),
				IP:    container.NetworkSettings.DefaultNetworkSettings.IPAddress,
			}

			files[ident], err = fm.Mapper(tr)
			if err != nil {
				return nil, fmt.Errorf("mapper failed: %w", err)
			}

			reader.Close()
		}

		ctx = context.WithValue(ctx, key, files)
	}

	return ctx, nil
}

func (s *Strategy) monitorContainer(ctx context.Context, container types.ContainerJSON) (func(), error) {
	resp, err := s.cli.ContainerStats(ctx, container.ID, true)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(resp.Body)
	ns := &metrics.NodeStats{}
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		for {
			data := &types.StatsJSON{}
			err := dec.Decode(data)
			if err != nil {
				wg.Done()
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

	closer := func() {
		resp.Body.Close()
		wg.Wait()
	}

	return closer, nil
}

// Execute takes the round and execute it against the context created from the
// options.
func (s *Strategy) Execute(round sim.Round) error {
	ctx, err := s.makeExecutionContext()
	if err != nil {
		return fmt.Errorf("couldn't create the context: %w", err)
	}

	closers := make([]func(), 0, len(s.containers))

	// It's important to close any existing stream even if an error
	// occurred.
	defer func() {
		for _, closer := range closers {
			closer()
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
		return fmt.Errorf("couldn't execute: %w", err)
	}

	fmt.Fprintln(s.out, "Execution done.")

	return nil
}

// WriteStats writes the statistics of the nodes to the file.
func (s *Strategy) WriteStats(filename string) error {
	s.statsLock.Lock()
	defer s.statsLock.Unlock()

	file, err := os.Create(filepath.Join(s.options.OutputDir, filename))
	if err != nil {
		return err
	}

	err = s.encoder(file, s.stats)
	if err != nil {
		return fmt.Errorf("couldn't encode the stats: %w", err)
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
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("couldn't remove the containers: %v", errs)
	}

	fmt.Fprintln(s.out, "Cleaning done.")

	return nil
}

func (s *Strategy) String() string {
	return "Docker"
}
