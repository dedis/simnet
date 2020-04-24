package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/buger/goterm"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"go.dedis.ch/simnet/daemon"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"golang.org/x/xerrors"
)

const (
	// ContainerStopTimeout is the maximum amount of time given to a container
	// to stop.
	ContainerStopTimeout = 10 * time.Second

	// ExecWaitTimeout is the maximum amount of time given to an exec to end.
	ExecWaitTimeout = 10 * time.Second

	// ImageBaseURL is the origin where the images will be pulled.
	ImageBaseURL = "docker.io"
	// ImageMonitor is the path to the docker image that contains the network
	// emulator tool.
	ImageMonitor = "dedis/simnet-monitor"

	// ContainerLabelKey is the label key assigned to every application
	// container.
	ContainerLabelKey = "go.dedis.ch.simnet.container"
	// ContainerLabelValue is the value for application containers.
	ContainerLabelValue = "app"

	// DefaultContainerNetwork is the default network used by Docker when no
	// additionnal network is required when creating the container.
	// This should be different whatsoever the Docker environment settings.
	DefaultContainerNetwork = "bridge"
)

var (
	monitorNetEmulatorCommand = []string{"./netem", "-log", "/dev/stdout"}
)

// Event is the json encoded events sent when pulling an image.
type Event struct {
	Status   string `json:"status"`
	Error    string `json:"error"`
	Progress string `json:"progress"`
}

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
	dio        sim.IO
	options    *sim.Options
	containers []types.Container
	stats      metrics.Stats
	statsLock  sync.Mutex
	encoder    Encoder
	vpn        VPN

	// Depending on which step the simulation is booting, it is necessary
	// to know if some states need to be loaded.
	updated bool

	// Streaming can start either during deployment or during execute so we
	// start the process only once.
	streamingLogs bool
}

// NewStrategy creates a docker strategy for simulations.
func NewStrategy(opts ...sim.Option) (*Strategy, error) {
	cli, err := makeDockerClient()
	if err != nil {
		return nil, err
	}

	options := sim.NewOptions(opts)
	stats := metrics.NewStats()

	return &Strategy{
		out:        os.Stdout,
		cli:        cli,
		vpn:        newDockerOpenVPN(cli, os.Stdout, options),
		dio:        newDockerIO(cli, &stats),
		options:    options,
		containers: make([]types.Container, 0),
		stats:      stats,
		encoder:    jsonEncoder,
	}, nil
}

// Option allows to change the options defined at the creation of the strategy.
func (s *Strategy) Option(opt sim.Option) {
	opt(s.options)
}

// Get the list of containers running in the Docker environment that are in
// scope with the simulation.
func (s *Strategy) refreshContainers(ctx context.Context) error {
	if s.updated {
		// The list have already been refreshed.
		return nil
	}

	args := filters.NewArgs()
	args.Add("label", fmt.Sprintf("%s=%s", ContainerLabelKey, ContainerLabelValue))

	containers, err := s.cli.ContainerList(ctx, types.ContainerListOptions{
		Filters: args,
	})
	if err != nil {
		return err
	}

	s.containers = containers

	return nil
}

func waitImagePull(reader io.Reader, image string, out io.Writer) error {
	dec := json.NewDecoder(reader)
	evt := Event{}
	for {
		err := dec.Decode(&evt)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintf(out, goterm.ResetLine("Pull image %s... Done."), image)
				fmt.Fprintln(out, "")
				return nil
			}

			fmt.Fprintln(out, goterm.ResetLine("Pull image... Failed."))
			return fmt.Errorf("couldn't decode the event: %w", err)
		}

		if evt.Error != "" {
			// Typically a stream errors or a server side error.
			return fmt.Errorf("stream error: %s", evt.Error)
		}

		fmt.Fprintf(out, goterm.ResetLine("Pull image... %s %s"), evt.Status, evt.Progress)
	}
}

func pullImage(ctx context.Context, cli client.APIClient, image string, out io.Writer) error {
	ref := fmt.Sprintf("%s/%s", ImageBaseURL, image)

	reader, err := cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	defer reader.Close()

	err = waitImagePull(reader, image, out)
	if err != nil {
		return fmt.Errorf("couldn't complete pull: %w", err)
	}

	return nil
}

func (s *Strategy) createContainers(ctx context.Context) error {
	ports := nat.PortSet{}
	for _, port := range s.options.Ports {
		key := fmt.Sprintf("%d/%s", port.Value(), port.Protocol())
		ports[nat.Port(key)] = struct{}{}
	}

	hcfg := &container.HostConfig{
		AutoRemove: true,
	}

	for _, volume := range s.options.TmpFS {
		hcfg.Mounts = append(hcfg.Mounts, mount.Mount{
			Type:   mount.TypeTmpfs,
			Target: volume.Destination,
			TmpfsOptions: &mount.TmpfsOptions{
				SizeBytes: volume.Size,
			},
		})
	}

	for _, node := range s.options.Topology.GetNodes() {
		cfg := &container.Config{
			Image: s.options.Image,
			Cmd:   append(append([]string{}, s.options.Cmd...), s.options.Args...),
			Labels: map[string]string{
				ContainerLabelKey: ContainerLabelValue,
			},
			ExposedPorts: ports,
			Hostname:     node.String(),
		}

		resp, err := s.cli.ContainerCreate(ctx, cfg, hcfg, nil, node.String())
		if err != nil {
			return err
		}

		err = s.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
		if err != nil {
			return err
		}
	}

	err := s.refreshContainers(ctx)
	if err != nil {
		return fmt.Errorf("couldn't refresh the list of containers: %w", err)
	}

	return nil
}

func waitExec(containerID string, msgCh <-chan events.Message, errCh <-chan error, t time.Duration) error {
	timeout := time.After(t)

	for {
		select {
		case msg := <-msgCh:
			if msg.Status == "die" && msg.Actor.ID == containerID {
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
	c types.Container,
	cfg *container.Config,
	mapping map[network.Node]string,
	msgCh <-chan events.Message,
	errCh <-chan error,
) error {
	hcfg := &container.HostConfig{
		AutoRemove:  true,
		CapAdd:      []string{"NET_ADMIN"},
		NetworkMode: container.NetworkMode(fmt.Sprintf("container:%s", c.ID)),
	}

	resp, err := s.cli.ContainerCreate(ctx, cfg, hcfg, nil, "")
	if err != nil {
		return fmt.Errorf("couldn't create netem container: %w", err)
	}

	conn, err := s.cli.ContainerAttach(ctx, resp.ID, types.ContainerAttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return err
	}

	defer conn.Close()

	err = s.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("couldn't start netem container: %w", err)
	}

	rules := s.options.Topology.Rules(network.Node(containerName(c)), mapping)

	enc := json.NewEncoder(conn.Conn)
	err = enc.Encode(&rules)
	if err != nil {
		return fmt.Errorf("couldn't encode the rules: %w", err)
	}

	// Output from the monitor container executing the netem tool.
	// io.Copy(ioutil.Discard, conn.Reader)

	err = waitExec(resp.ID, msgCh, errCh, ExecWaitTimeout)
	if err != nil {
		return fmt.Errorf("couldn't apply rule: %w", err)
	}

	return nil
}

func (s *Strategy) configureContainers(ctx context.Context) error {
	ref := fmt.Sprintf("%s/%s:%s", ImageBaseURL, ImageMonitor, daemon.Version)
	reader, err := s.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("couldn't pull netem image: %w", err)
	}

	defer reader.Close()

	err = waitImagePull(reader, ref, s.out)
	if err != nil {
		return fmt.Errorf("couldn't complete pull: %w", err)
	}

	cfg := &container.Config{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    true,
		StdinOnce:    true,
		Image:        fmt.Sprintf("%s:%s", ImageMonitor, daemon.Version),
		Entrypoint:   monitorNetEmulatorCommand,
	}

	// Create the mapping between node names and IPs.
	mapping := make(map[network.Node]string)
	for _, c := range s.containers {
		mapping[network.Node(containerName(c))] = c.NetworkSettings.Networks[DefaultContainerNetwork].IPAddress
	}

	// Caller is responsible to cancel the context.
	msgCh, errCh := s.cli.Events(ctx, types.EventsOptions{})

	for i, c := range s.containers {
		fmt.Fprintf(s.out, goterm.ResetLine("Configure containers... Progress [%d/%d]"), i+1, len(s.containers))

		err = s.configureContainer(ctx, c, cfg, mapping, msgCh, errCh)
		if err != nil {
			fmt.Fprintln(s.out, goterm.ResetLine("Configure containers... Failed."))
			return err
		}
	}

	fmt.Fprintln(s.out, "")

	return nil
}

// Deploy pulls the application image and starts a container per node.
func (s *Strategy) Deploy(ctx context.Context, round sim.Round) error {
	err := pullImage(ctx, s.cli, s.options.Image, s.out)
	if err != nil {
		return xerrors.Errorf("couldn't pull the image: %w", err)
	}

	fmt.Fprintf(s.out, "Creating containers... In Progress.")
	err = s.createContainers(ctx)
	if err != nil {
		fmt.Fprintln(s.out, goterm.ResetLine("Creating containers... Failed."))
		return xerrors.Errorf("couldn't create the container: %w", err)
	}
	fmt.Fprintln(s.out, goterm.ResetLine("Creating containers... Done."))

	err = s.streamLogs(ctx)
	if err != nil {
		return xerrors.Errorf("couldn't stream logs: %v", err)
	}

	err = s.configureContainers(ctx)
	if err != nil {
		return xerrors.Errorf("couldn't configure the containers: %w", err)
	}

	err = s.vpn.Deploy()
	if err != nil {
		return xerrors.Errorf("couldn't deploy the vpn: %v", err)
	}

	err = round.Before(s.dio, s.makeExecutionContext())
	if err != nil {
		return err
	}

	fmt.Fprintln(s.out, "Deployment... Done.")
	s.updated = true // All states are loaded at that point.

	return nil
}

func (s *Strategy) makeExecutionContext() []sim.NodeInfo {
	nodes := make([]sim.NodeInfo, len(s.containers))
	for i, container := range s.containers {
		netcfg := container.NetworkSettings.Networks[DefaultContainerNetwork]

		nodes[i].Name = containerName(container)
		nodes[i].Address = netcfg.IPAddress
	}

	return nodes
}

func (s *Strategy) monitorContainer(ctx context.Context, container types.Container) (func(), error) {
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
			s.stats.Nodes[containerName(container)] = *ns
			s.statsLock.Unlock()
		}
	}()

	closer := func() {
		resp.Body.Close()
		wg.Wait()
	}

	return closer, nil
}

func (s *Strategy) streamLogs(ctx context.Context) error {
	if s.streamingLogs {
		return nil
	}
	s.streamingLogs = true

	logFolder := filepath.Join(s.options.OutputDir, "logs")

	// Clean the folder so it does not mix different simulations.
	err := os.RemoveAll(logFolder)
	if err != nil {
		return err
	}

	err = os.MkdirAll(logFolder, 0755)
	if err != nil {
		return err
	}

	for _, container := range s.containers {
		reader, err := s.cli.ContainerLogs(ctx, container.ID, types.ContainerLogsOptions{
			ShowStderr: true,
			ShowStdout: true,
			Timestamps: true,
			Follow:     true,
		})

		if err != nil {
			return err
		}

		f, err := os.Create(filepath.Join(logFolder, container.Names[0]))
		if err != nil {
			return err
		}

		go func() {
			io.Copy(f, reader)
			f.Close()
		}()
	}

	return nil
}

// Execute takes the round and execute it against the context created from the
// options.
func (s *Strategy) Execute(ctx context.Context, round sim.Round) error {
	err := s.refreshContainers(ctx)
	if err != nil {
		return fmt.Errorf("couldn't update the states: %w", err)
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
		closer, err := s.monitorContainer(ctx, container)
		if err != nil {
			return err
		}

		closers = append(closers, closer)
	}

	// Listen for the container logs and write the output in separate files
	// for each of them in the output folder.
	err = s.streamLogs(ctx)
	if err != nil {
		return xerrors.Errorf("couldn't stream logs: %v", err)
	}

	s.statsLock.Lock()
	s.stats.Timestamp = time.Now().Unix()
	s.statsLock.Unlock()

	nodes := s.makeExecutionContext()

	err = round.Execute(s.dio, nodes)
	if err != nil {
		return fmt.Errorf("couldn't execute: %w", err)
	}

	// After step so that it is executed after each experiment.
	err = round.After(s.dio, nodes)
	if err != nil {
		return fmt.Errorf("couldn't perform after step: %w", err)
	}

	fmt.Fprintln(s.out, "Execution... Done.")
	s.updated = true

	return nil
}

// WriteStats writes the statistics of the nodes to the file.
func (s *Strategy) WriteStats(ctx context.Context, filename string) error {
	s.statsLock.Lock()
	defer s.statsLock.Unlock()

	err := os.MkdirAll(s.options.OutputDir, 0755)
	if err != nil {
		return err
	}

	file, err := os.Create(filepath.Join(s.options.OutputDir, filename))
	if err != nil {
		return err
	}

	err = s.encoder(file, &s.stats)
	if err != nil {
		return fmt.Errorf("couldn't encode the stats: %w", err)
	}

	fmt.Fprintln(s.out, "Write statistics... Done.")

	return nil
}

// Clean stops and removes all the containers created by the simulation.
func (s *Strategy) Clean(ctx context.Context) error {
	errs := []error{}

	err := s.vpn.Clean()
	if err != nil {
		errs = append(errs, fmt.Errorf("vpn: %v", err))
	}

	err = s.refreshContainers(ctx)
	if err != nil {
		return fmt.Errorf("couldn't get running containers: %w", err)
	}

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

	fmt.Fprintln(s.out, "Cleaning... Done.")
	s.updated = true // All states are loaded at that point.

	return nil
}

func (s *Strategy) String() string {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = client.DefaultDockerHost
	}

	return fmt.Sprintf("Docker[%s] @ %s", s.cli.ClientVersion(), host)
}

func containerName(c types.Container) string {
	// The name is well-defined and thus it must be present as the first index
	// and no additionnal names should be there. The first character is stripped
	// to remove the "/" character.
	return c.Names[0][1:]
}
