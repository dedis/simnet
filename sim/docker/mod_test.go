package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/daemon"
	snet "go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
)

func TestStrategy_New(t *testing.T) {
	s, err := NewStrategy()
	require.NoError(t, err)
	require.NotNil(t, s)

	makeDockerClient = func() (client.APIClient, error) {
		return nil, errors.New("client error")
	}

	_, err = NewStrategy()
	require.Error(t, err)
}

func TestStrategy_Option(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	s.Option(sim.WithVPN("abc"))
	require.Equal(t, s.options.VPNExecutable, "abc")
}

func TestStrategy_Deploy(t *testing.T) {
	n := 3
	client := &testClient{numContainers: n}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	client.bufferPullImage = new(bytes.Buffer)
	enc := json.NewEncoder(client.bufferPullImage)
	require.NoError(t, enc.Encode(&Event{Status: "Test"}))

	err := s.Deploy(context.Background(), &testRound{})
	require.NoError(t, err)

	// Check that application and monitor images are pulled.
	require.Len(t, client.callsImagePull, 2)
	require.Equal(t, fmt.Sprintf("%s/%s", ImageBaseURL, testImage), client.callsImagePull[0].ref)
	require.Equal(t, fmt.Sprintf("%s/%s:%s", ImageBaseURL, ImageMonitor, daemon.Version), client.callsImagePull[1].ref)

	// Check that the correct list of containers is created.
	// - n for the application
	// - n for the netem container
	require.Len(t, client.callsContainerCreate, n*2)

	for _, call := range client.callsContainerCreate {
		require.True(t, call.hcfg.AutoRemove)
	}

	for _, call := range client.callsContainerCreate[:n] {
		require.Equal(t, testImage, call.cfg.Image)
		require.EqualValues(t, call.cfg.Cmd[:len(testCmd)], testCmd)
		require.EqualValues(t, call.cfg.Cmd[len(testCmd):], testArgs)

		_, ok := call.cfg.ExposedPorts[nat.Port("2000/tcp")]
		require.True(t, ok)
		_, ok = call.cfg.ExposedPorts[nat.Port("2001/tcp")]
		require.False(t, ok)
		_, ok = call.cfg.ExposedPorts[nat.Port("2001/udp")]
		require.True(t, ok)
		_, ok = call.cfg.ExposedPorts[nat.Port("2000/udp")]
		require.False(t, ok)

		require.Len(t, call.hcfg.Mounts, 1)
		require.Equal(t, call.hcfg.Mounts[0].Target, "/storage")
	}

	for i, call := range client.callsContainerCreate[n:] {
		require.Equal(t, fmt.Sprintf("%s:%s", ImageMonitor, daemon.Version), call.cfg.Image)
		require.True(t, call.cfg.AttachStdin)
		require.True(t, call.cfg.AttachStdout)
		require.True(t, call.cfg.AttachStderr)
		require.True(t, call.cfg.OpenStdin)
		require.True(t, call.cfg.StdinOnce)
		require.EqualValues(t, []string{"NET_ADMIN"}, call.hcfg.CapAdd)
		require.Equal(t, container.NetworkMode(fmt.Sprintf("container:%s", s.containers[i].ID)), call.hcfg.NetworkMode)
	}

	// Check that the containers are correctly started.
	require.Len(t, client.callsContainerStart, n*2)
	for i, call := range client.callsContainerStart[:n] {
		require.Equal(t, s.containers[i].ID, call.id)
	}
	for _, call := range client.callsContainerStart[n:] {
		// Name is empty for monitor containers.
		require.Equal(t, "id:", call.id)
	}

	// Check that the containers are correctly fetched.
	require.Len(t, client.callsContainerList, 1)
	value := fmt.Sprintf("%s=%s", ContainerLabelKey, ContainerLabelValue)
	require.True(t, client.callsContainerList[0].options.Filters.ExactMatch("label", value))

	// Check that the events are listened.
	require.Len(t, client.callsEvents, 1)

	rules := []snet.Rule{{IP: "ip:node0", Delay: snet.Delay{Value: 50}}}
	buffer := new(bytes.Buffer)
	enc = json.NewEncoder(buffer)
	require.NoError(t, enc.Encode(&rules))

	// Check that it attaches the I/O and write the rules.
	require.Len(t, client.callsContainerAttach, n)
	for i, call := range client.callsContainerAttach {
		require.Equal(t, "id:", call.id)
		require.True(t, call.options.Stdin)
		require.True(t, call.options.Stdout)
		require.True(t, call.options.Stderr)
		require.True(t, call.options.Stream)

		if i != 0 {
			require.Equal(t, buffer.String(), call.buffer.String())
		}
	}

	// Check that the states are marked as updated.
	require.True(t, s.updated)

	// Check that an error is returned if logs cannot be streamed.
	s.options.OutputDir = "\000"
	s.streamingLogs = false
	err = s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't stream logs: failed removing logs: open \x00: invalid argument")
}

func TestStrategy_RoundConfigureFailure(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	e := errors.New("configure error")
	err := s.Deploy(context.Background(), &testRound{errBefore: e})
	require.EqualError(t, err, "failed running 'Before': configure error")
}

func TestStrategy_DeployVPNError(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	s.vpn = fakeVPN{err: errors.New("oops")}
	err := s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't deploy the vpn: oops")
}

func TestStrategy_PullImageFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	e := errors.New("pull image error")
	client.errImagePull = e

	err := s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't pull the image: failed pulling image 'docker.io/path/to/image': pull image error")

	// Stream error happening during a pull.
	client.resetErrors()
	client.bufferPullImage = new(bytes.Buffer)

	enc := json.NewEncoder(client.bufferPullImage)
	strerr := "stream error"
	require.NoError(t, enc.Encode(&Event{Error: strerr}))

	err = pullImage(context.Background(), s.cli, "", ioutil.Discard)
	require.EqualError(t, err, "couldn't complete pull: stream error: stream error")

	// Data received is corrupted.
	client.bufferPullImage.Reset()
	client.bufferPullImage.Write([]byte("invalid event"))

	err = pullImage(context.Background(), s.cli, "", ioutil.Discard)
	require.EqualError(t, err, "couldn't complete pull: couldn't decode the event: invalid character 'i' looking for beginning of value")
}

func TestStrategy_CreateContainerFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	e := errors.New("create container error")
	client.errContainerCreate = e

	err := s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't create the container: failed creating container: create container error")

	e = errors.New("start container error")
	client.resetErrors()
	client.errContainerStart = e

	err = s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't create the container: failed starting container: start container error")

	e = errors.New("container list error")
	client.resetErrors()
	client.errContainerList = e

	err = s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't create the container: couldn't refresh the list of containers: failed refreshing containers: container list error")
}

func TestStrategy_ConfigureContainersFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.Container{makeTestContainer("id:node0")}

	e := errors.New("pull netem image error")
	client.errImagePull = e

	err := s.configureContainers(context.Background())
	require.EqualError(t, err, "couldn't pull netem image: pull netem image error")

	client.resetErrors()
	client.bufferPullImage = bytes.NewBuffer([]byte("invalid event"))

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid character")

	e = errors.New("create netem container error")
	client.resetErrors()
	client.errContainerCreate = e

	err = s.configureContainers(context.Background())
	require.EqualError(t, err, "failed configuring container: couldn't create netem container: create netem container error")

	e = errors.New("attach netem container error")
	client.resetErrors()
	client.errContainerAttach = e

	err = s.configureContainers(context.Background())
	require.EqualError(t, err, "failed configuring container: failed attaching container: attach netem container error")

	e = errors.New("start netem container error")
	client.resetErrors()
	client.errContainerStart = e

	err = s.configureContainers(context.Background())
	require.EqualError(t, err, "failed configuring container: couldn't start netem container: start netem container error")

	e = errors.New("encoding rules error")
	client.resetErrors()
	client.errAttachConn = e

	err = s.configureContainers(context.Background())
	require.EqualError(t, err, "failed configuring container: couldn't encode the rules: encoding rules error")

	e = errors.New("wait exec error")
	client.resetErrors()
	client.errEvent = e

	err = s.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't configure the containers: failed configuring container: couldn't apply rule: wait exec error")
}

func TestStrategy_WaitExecFailures(t *testing.T) {
	err := waitExec("", nil, nil, 0)
	require.Error(t, err)
	require.EqualError(t, err, "timeout")

	ch := make(chan events.Message, 1)
	id := "123"
	ch <- events.Message{
		Status: "die",
		Actor: events.Actor{
			ID: id,
			Attributes: map[string]string{
				"exitCode": "1",
			},
		},
	}

	err = waitExec(id, ch, nil, time.Second)
	require.Error(t, err)
	require.EqualError(t, err, "exit code 1")
}

type testRound struct {
	errBefore error
	errAfter  error
	nodes     []sim.NodeInfo
	afterDone bool
}

func (r *testRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	r.nodes = nodes
	return r.errBefore
}

func (r *testRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	r.nodes = nodes
	return r.errBefore
}

func (r *testRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
	r.afterDone = true
	return r.errAfter
}

func TestStrategy_Execute(t *testing.T) {
	n := 3
	client := &testClient{numContainers: n}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	round := &testRound{}
	err := s.Execute(context.Background(), round)
	require.NoError(t, err)

	require.True(t, s.updated)
	require.True(t, round.afterDone)

	require.Len(t, round.nodes, n)
	for i, node := range round.nodes {
		require.Equal(t, containerName(s.containers[i]), node.Name)
		netcfg := s.containers[i].NetworkSettings.Networks[DefaultContainerNetwork]
		require.Equal(t, netcfg.IPAddress, node.Address)
	}
}

func TestStrategy_ExecuteFailure(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	e := errors.New("log error")
	client.errContainerLogs = e
	err := s.Execute(context.Background(), &testRound{})
	require.EqualError(t, err, "failed streaming logs: failed accessing container logs: log error")

	client.resetErrors()
	e = errors.New("sim error")
	err = s.Execute(context.Background(), &testRound{errBefore: e})
	require.EqualError(t, err, "couldn't execute: sim error")

	e = errors.New("after error")
	err = s.Execute(context.Background(), &testRound{errAfter: e})
	require.EqualError(t, err, "couldn't perform after step: after error")

	s.updated = false
	e = errors.New("container list error")
	client.errContainerList = e

	err = s.Execute(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't update the states: failed refreshing containers: container list error")
}

func TestStrategy_StreamLogsFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.Container{
		makeTestContainer("id:node0"),
		makeTestContainer("id:node1"),
	}

	e := errors.New("log stream error")
	client.errContainerLogs = e
	err := s.streamLogs(context.Background())
	require.EqualError(t, err, "failed accessing container logs: log stream error")

	client.resetErrors()
	s.containers[1].Names = []string{"\000"}
	s.streamingLogs = false
	err = s.streamLogs(context.Background())
	require.Error(t, err)

	s.options.OutputDir = "\000"
	s.streamingLogs = false
	err = s.streamLogs(context.Background())
	require.Error(t, err)

	s.options.OutputDir = "/etc"
	s.streamingLogs = false
	err = s.streamLogs(context.Background())
	require.Error(t, err)
}

func TestStrategy_WriteStats(t *testing.T) {
	// TODO:
}

func TestStrategy_Clean(t *testing.T) {
	n := 3
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	for i := 0; i < n; i++ {
		s.containers = append(s.containers, makeTestContainer(fmt.Sprintf("id:node%d", i)))
	}

	err := s.Clean(context.Background())
	require.NoError(t, err)

	require.True(t, s.updated)

	require.Len(t, client.callsContainerStop, n)
	for i, call := range client.callsContainerStop {
		require.Equal(t, s.containers[i].ID, call.id)
		require.Equal(t, ContainerStopTimeout, *call.t)
	}
}

func TestStrategy_CleanFailures(t *testing.T) {
	client := &testClient{}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.vpn = fakeVPN{err: errors.New("oops")}
	err := s.Clean(context.Background())
	require.EqualError(t, err, "couldn't remove the containers: [vpn: oops]")

	client.errContainerList = errors.New("container list error")
	err = s.Clean(context.Background())
	require.EqualError(t, err, "couldn't get running containers: failed refreshing containers: container list error")

	s.updated = true
	s.containers = []types.Container{makeTestContainer("id:node0")}

	e := errors.New("container stop error")
	client.resetErrors()
	client.errContainerStop = e

	err = s.Clean(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())
}

func TestStrategy_String(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	require.NoError(t, os.Setenv("DOCKER_HOST", ""))
	require.Equal(t, fmt.Sprintf("Docker[1.0] @ %s", client.DefaultDockerHost), s.String())

	require.NoError(t, os.Setenv("DOCKER_HOST", "docker.host"))
	require.Equal(t, fmt.Sprintf("Docker[1.0] @ docker.host"), s.String())
}

type testConn struct {
	buffer *bytes.Buffer
	err    error
}

func (c *testConn) Read(b []byte) (int, error) {
	return c.buffer.Read(b)
}

func (c *testConn) Write(b []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}

	return c.buffer.Write(b)
}

func (c *testConn) Close() error {
	return nil
}

func (c *testConn) LocalAddr() net.Addr {
	return nil
}

func (c *testConn) RemoteAddr() net.Addr {
	return nil
}

func (c *testConn) SetDeadline(time.Time) error {
	return nil
}

func (c *testConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *testConn) SetWriteDeadline(time.Time) error {
	return nil
}

type testCallPullImage struct {
	ctx     context.Context
	ref     string
	options types.ImagePullOptions
}

type testCallContainerCreate struct {
	ctx  context.Context
	cfg  *container.Config
	hcfg *container.HostConfig
	ncfg *network.NetworkingConfig
	name string
}

type testCallContainerAttach struct {
	ctx     context.Context
	id      string
	options types.ContainerAttachOptions
	buffer  *bytes.Buffer
}

type testCallContainerStart struct {
	ctx     context.Context
	id      string
	options types.ContainerStartOptions
}

type testCallContainerList struct {
	ctx     context.Context
	options types.ContainerListOptions
}

type testCallContainerStop struct {
	ctx context.Context
	id  string
	t   *time.Duration
}

type testCallEvents struct {
	ctx     context.Context
	options types.EventsOptions
}

type testClient struct {
	*client.Client
	numContainers int

	callsImagePull       []testCallPullImage
	callsContainerCreate []testCallContainerCreate
	callsContainerAttach []testCallContainerAttach
	callsContainerStart  []testCallContainerStart
	callsContainerStop   []testCallContainerStop
	callsContainerList   []testCallContainerList
	callsEvents          []testCallEvents

	bufferPullImage *bytes.Buffer

	// Don't forget to update the reset function when adding new errors.
	errImagePull       error
	errContainerCreate error
	errContainerAttach error
	errContainerStart  error
	errContainerStop   error
	errContainerList   error
	errContainerLogs   error
	errAttachConn      error
	errEvent           error
}

func (c *testClient) resetErrors() {
	c.bufferPullImage = nil
	c.errImagePull = nil
	c.errContainerCreate = nil
	c.errContainerAttach = nil
	c.errContainerStart = nil
	c.errContainerStop = nil
	c.errContainerList = nil
	c.errContainerLogs = nil
	c.errAttachConn = nil
	c.errEvent = nil
}

func (c *testClient) ClientVersion() string {
	return "1.0"
}

func (c *testClient) ImagePull(ctx context.Context, ref string, opts types.ImagePullOptions) (io.ReadCloser, error) {
	c.callsImagePull = append(c.callsImagePull, testCallPullImage{ctx, ref, opts})

	if c.bufferPullImage != nil {
		return ioutil.NopCloser(c.bufferPullImage), c.errImagePull
	}

	return ioutil.NopCloser(new(bytes.Buffer)), c.errImagePull
}

func (c *testClient) ContainerCreate(ctx context.Context, cfg *container.Config, hcfg *container.HostConfig, ncfg *network.NetworkingConfig, name string) (container.ContainerCreateCreatedBody, error) {
	c.callsContainerCreate = append(c.callsContainerCreate, testCallContainerCreate{ctx, cfg, hcfg, ncfg, name})

	return container.ContainerCreateCreatedBody{ID: fmt.Sprintf("id:%s", name)}, c.errContainerCreate
}

func (c *testClient) ContainerAttach(ctx context.Context, id string, options types.ContainerAttachOptions) (types.HijackedResponse, error) {
	buffer := new(bytes.Buffer)
	c.callsContainerAttach = append(c.callsContainerAttach, testCallContainerAttach{ctx, id, options, buffer})

	conn := &testConn{
		buffer: buffer,
		err:    c.errAttachConn,
	}

	return types.HijackedResponse{Conn: conn}, c.errContainerAttach
}

func (c *testClient) ContainerStart(ctx context.Context, id string, options types.ContainerStartOptions) error {
	c.callsContainerStart = append(c.callsContainerStart, testCallContainerStart{ctx, id, options})

	return c.errContainerStart
}

func makeTestContainer(id string) types.Container {
	return types.Container{
		Names: []string{fmt.Sprintf("/%s", id[3:])},
		ID:    id,
		NetworkSettings: &types.SummaryNetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				DefaultContainerNetwork: {IPAddress: fmt.Sprintf("ip:%s", id[3:])},
			},
		},
	}
}

func (c *testClient) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	c.callsContainerList = append(c.callsContainerList, testCallContainerList{ctx, options})

	containers := make([]types.Container, c.numContainers)
	for i := range containers {
		containers[i] = makeTestContainer(fmt.Sprintf("id:node%d", i))
	}

	return containers, c.errContainerList
}

func (c *testClient) ContainerStop(ctx context.Context, id string, t *time.Duration) error {
	c.callsContainerStop = append(c.callsContainerStop, testCallContainerStop{ctx, id, t})

	return c.errContainerStop
}

func (c *testClient) ContainerLogs(ctx context.Context, id string, options types.ContainerLogsOptions) (io.ReadCloser, error) {
	reader := ioutil.NopCloser(new(bytes.Buffer))

	return reader, c.errContainerLogs
}

func (c *testClient) Events(ctx context.Context, options types.EventsOptions) (<-chan events.Message, <-chan error) {
	c.callsEvents = append(c.callsEvents, testCallEvents{ctx, options})

	if c.errEvent != nil {
		ch := make(chan error, 1)
		ch <- c.errEvent
		return nil, ch
	}

	ch := make(chan events.Message, c.numContainers)
	for i := 0; i < c.numContainers; i++ {
		ch <- events.Message{
			Status: "die",
			Actor: events.Actor{
				ID: "id:",
				Attributes: map[string]string{
					"exitCode": "0",
				},
			},
		}
	}

	return ch, nil
}

func (c *testClient) NetworkCreate(ctx context.Context, name string, options types.NetworkCreate) (types.NetworkCreateResponse, error) {
	return types.NetworkCreateResponse{}, nil
}

func (c *testClient) NetworkConnect(ctx context.Context, networkID, container string, config *network.EndpointSettings) error {
	return nil
}

func (c *testClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
}

type testDockerIO struct {
	IO
	err error
}

func (dio testDockerIO) Read(container, path string) (io.ReadCloser, error) {
	return nil, dio.err
}

func (dio testDockerIO) Write(container, path string, content io.Reader) error {
	return nil
}

func (dio testDockerIO) Exec(container string, cmd []string, options sim.ExecOptions) error {
	return nil
}

func (dio testDockerIO) FetchStats(start, end time.Time, filename string) error {
	return nil
}

func (dio testDockerIO) monitorContainers(context.Context, []types.Container) (func(), error) {
	return func() {}, nil
}

const (
	testImage         = "path/to/image"
	testStatBaseValue = uint64(123)
)

var (
	testCmd  = []string{"cmd"}
	testArgs = []string{"arg1"}
)

type fakeVPN struct {
	err error
}

func (vpn fakeVPN) Deploy() error {
	return vpn.err
}

func (vpn fakeVPN) Clean() error {
	return vpn.err
}

func newTestStrategy(t *testing.T) (*Strategy, func()) {
	out, err := ioutil.TempDir(os.TempDir(), "simnet-docker-test")
	require.NoError(t, err)

	cleaner := func() {
		os.RemoveAll(out)
	}

	client := &testClient{numContainers: 3}

	return &Strategy{
		cli: client,
		vpn: fakeVPN{},
		dio: testDockerIO{},
		out: ioutil.Discard,
		options: sim.NewOptions([]sim.Option{
			sim.WithOutput(out),
			sim.WithTopology(snet.NewSimpleTopology(3, 50)),
			sim.WithImage(
				testImage,
				testCmd,
				testArgs,
				sim.NewTCP(2000),
				sim.NewUDP(2001),
			),
			sim.WithTmpFS("/storage", 2*sim.GB),
		}),
		containers: make([]types.Container, 0),
	}, cleaner
}

func newTestStrategyWithClient(t *testing.T, client *testClient) (*Strategy, func()) {
	s, cleaner := newTestStrategy(t)
	s.cli = client

	return s, cleaner
}
