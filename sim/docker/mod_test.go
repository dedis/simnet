package docker

import (
	"archive/tar"
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
	"go.dedis.ch/simnet/metrics"
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

func TestStrategy_Deploy(t *testing.T) {
	n := 3
	client := &testClient{numContainers: n}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	client.bufferPullImage = new(bytes.Buffer)
	enc := json.NewEncoder(client.bufferPullImage)
	require.NoError(t, enc.Encode(&Event{Status: "Test"}))

	err := s.Deploy()
	require.NoError(t, err)

	// Check that application and monitor images are pulled.
	require.Len(t, client.callsImagePull, 2)
	require.Equal(t, fmt.Sprintf("%s/%s", ImageBaseURL, testImage), client.callsImagePull[0].ref)
	require.Equal(t, fmt.Sprintf("%s/%s", ImageBaseURL, ImageMonitor), client.callsImagePull[1].ref)

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
	}

	for i, call := range client.callsContainerCreate[n:] {
		require.Equal(t, ImageMonitor, call.cfg.Image)
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
	require.Len(t, client.callsContainerInspect, n)
	require.Len(t, s.containers, n)
	for i, call := range client.callsContainerInspect {
		require.Equal(t, s.containers[i].ID, call.id)
	}

	// Check that the events are listened.
	require.Len(t, client.callsEvents, 1)

	// Check that exec request are created.
	require.Len(t, client.callsExecCreate, n)
	for _, call := range client.callsExecCreate {
		require.Equal(t, "id:", call.id)
		require.True(t, call.options.AttachStdin)
		require.True(t, call.options.AttachStderr)
		require.True(t, call.options.AttachStdout)
		require.Equal(t, monitorNetEmulatorCommand, call.options.Cmd)
	}

	rules := []snet.Rule{{IP: "ip:node0", Delay: snet.Delay{Value: 50}}}
	buffer := new(bytes.Buffer)
	enc = json.NewEncoder(buffer)
	require.NoError(t, enc.Encode(&rules))

	// Check that it attaches the I/O and write the rules.
	require.Len(t, client.callsExecAttach, n)
	for i, call := range client.callsExecAttach {
		require.Equal(t, testExecID, call.id)

		if i != 0 {
			require.Equal(t, buffer.String(), call.buffer.String())
		}
	}

	// Check that it starts the execs.
	require.Len(t, client.callsExecStart, n)
	for _, call := range client.callsExecStart {
		require.Equal(t, testExecID, call.id)
	}

	// Check that the monitor containers are stopped.
	require.Len(t, client.callsContainerStop, n)
	for _, call := range client.callsContainerStop {
		require.Equal(t, "id:", call.id)
		require.Equal(t, ContainerStopTimeout, *call.t)
	}
}

func TestStrategy_PullImageFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	e := errors.New("pull image error")
	client.errImagePull = e

	err := s.Deploy()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	// Stream error happening during a pull.
	client.resetErrors()
	client.bufferPullImage = new(bytes.Buffer)

	enc := json.NewEncoder(client.bufferPullImage)
	strerr := "stream error"
	require.NoError(t, enc.Encode(&Event{Error: strerr}))

	err = s.pullImage(context.Background())
	require.Error(t, err)
	require.EqualError(t, errors.Unwrap(err), fmt.Sprintf("stream error: %s", strerr))

	// Data received is corrupted.
	client.bufferPullImage.Reset()
	client.bufferPullImage.Write([]byte("invalid event"))

	err = s.pullImage(context.Background())
	require.Error(t, err)
	require.EqualError(t, errors.Unwrap(errors.Unwrap(err)), "invalid character 'i' looking for beginning of value")
}

func TestStrategy_CreateContainerFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	e := errors.New("create container error")
	client.errContainerCreate = e

	err := s.Deploy()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("start container error")
	client.resetErrors()
	client.errContainerStart = e

	err = s.Deploy()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("insepct container error")
	client.resetErrors()
	client.errContainerInspect = e

	err = s.Deploy()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

func TestStrategy_ConfigureContainersFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer("id:node0")}

	e := errors.New("pull netem image error")
	client.errImagePull = e

	err := s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	client.resetErrors()
	client.bufferPullImage = bytes.NewBuffer([]byte("invalid event"))

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid character")

	e = errors.New("create netem container error")
	client.resetErrors()
	client.errContainerCreate = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("start netem container error")
	client.resetErrors()
	client.errContainerStart = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("create exec error")
	client.resetErrors()
	client.errContainerExecCreate = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("attach exec error")
	client.resetErrors()
	client.errContainerExecAttach = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("start exec error")
	client.resetErrors()
	client.errContainerExecStart = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("stop netem container error")
	client.resetErrors()
	client.errContainerStop = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("encoding rules error")
	client.resetErrors()
	client.errAttachConn = e

	err = s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e), err.Error())

	e = errors.New("wait exec error")
	client.resetErrors()
	client.errEvent = e

	err = s.Deploy()
	require.Error(t, err)
	require.True(t, errors.Is(err, e), err.Error())
}

func TestStrategy_WaitExecFailures(t *testing.T) {
	err := waitExec("", nil, nil, 0)
	require.Error(t, err)
	require.EqualError(t, err, "timeout")

	ch := make(chan events.Message, 1)
	execID := "123"
	ch <- events.Message{
		Status: "exec_die",
		Actor: events.Actor{
			Attributes: map[string]string{
				"execID":   execID,
				"exitCode": "1",
			},
		},
	}

	err = waitExec(execID, ch, nil, time.Second)
	require.Error(t, err)
	require.EqualError(t, err, "exit code 1")
}

type testRound struct {
	err error
}

func (r testRound) Execute(context.Context) error {
	return r.err
}

func TestStrategy_Execute(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer("id:node0")}

	err := s.Execute(testRound{})
	require.NoError(t, err)
}

func TestStrategy_ExecuteFailure(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	e := errors.New("sim error")

	err := s.Execute(testRound{err: e})
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

func TestStrategy_MakeContextFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer("id:node0")}

	e := errors.New("copy file error")
	client.errCopyFromContainer = e

	err := s.Execute(testRound{})
	require.Error(t, err)
	require.True(t, errors.Is(err, e), err.Error())

	client.resetErrors()
	client.errCopyReader = true

	err = s.Execute(testRound{})
	require.Error(t, err)
	require.EqualError(t, errors.Unwrap(errors.Unwrap(err)), "io: read/write on closed pipe")

	client.resetErrors()
	e = errors.New("mapper error")
	s.options.Files[sim.FilesKey("error")] = sim.FileMapper{
		Path: "",
		Mapper: func(io.Reader) (interface{}, error) {
			return nil, e
		},
	}

	err = s.Execute(testRound{})
	require.Error(t, err)
	require.True(t, errors.Is(err, e), err.Error())
}

func TestStrategy_MonitorContainerFailures(t *testing.T) {
	client := &testClient{numContainers: 3}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer("id:node0")}

	e := errors.New("monitor container error")
	client.errContainerStats = e

	err := s.Execute(testRound{})
	require.Error(t, err)
	require.True(t, errors.Is(err, e), err.Error())
}

func TestStrategy_WriteStats(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	err := s.WriteStats("abc")
	require.NoError(t, err)
}

func TestStrategy_WriteStatsFailures(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	e := errors.New("encoder error")
	s.encoder = func(io.Writer, *metrics.Stats) error {
		return e
	}

	err := s.WriteStats("1")
	require.Error(t, err)
	require.True(t, errors.Is(err, e), err.Error())

	s.encoder = jsonEncoder
	s.options.OutputDir = ""
	err = s.WriteStats("")
	require.Error(t, err)
	require.EqualError(t, err, "open : no such file or directory")
}

func TestStrategy_Clean(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer("id:node0")}

	err := s.Clean()
	require.NoError(t, err)
}

func TestStrategy_CleanFailures(t *testing.T) {
	client := &testClient{}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer("id:node0")}

	e := errors.New("container stop error")
	client.errContainerStop = e

	err := s.Clean()
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())
}

func TestStrategy_String(t *testing.T) {
	s, clean := newTestStrategy(t)
	defer clean()

	require.Equal(t, "Docker", s.String())
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

type testCallContainerStart struct {
	ctx     context.Context
	id      string
	options types.ContainerStartOptions
}

type testCallContainerInspect struct {
	ctx context.Context
	id  string
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

type testCallExecCreate struct {
	ctx     context.Context
	id      string
	options types.ExecConfig
}

type testCallExecAttach struct {
	ctx     context.Context
	id      string
	options types.ExecConfig
	buffer  *bytes.Buffer
}

type testCallExecStart struct {
	ctx     context.Context
	id      string
	options types.ExecStartCheck
}

type testClient struct {
	*client.Client
	numContainers int

	callsImagePull        []testCallPullImage
	callsContainerCreate  []testCallContainerCreate
	callsContainerStart   []testCallContainerStart
	callsContainerInspect []testCallContainerInspect
	callsContainerStop    []testCallContainerStop
	callsEvents           []testCallEvents
	callsExecCreate       []testCallExecCreate
	callsExecAttach       []testCallExecAttach
	callsExecStart        []testCallExecStart

	bufferPullImage *bytes.Buffer

	// Don't forget to update the reset function when adding new errors.
	errImagePull           error
	errContainerCreate     error
	errContainerStart      error
	errContainerInspect    error
	errContainerStop       error
	errContainerStats      error
	errContainerExecCreate error
	errContainerExecAttach error
	errContainerExecStart  error
	errCopyFromContainer   error
	errAttachConn          error
	errEvent               error

	errCopyReader bool
}

func (c *testClient) resetErrors() {
	c.bufferPullImage = nil
	c.errImagePull = nil
	c.errContainerCreate = nil
	c.errContainerStart = nil
	c.errContainerInspect = nil
	c.errContainerStop = nil
	c.errContainerStats = nil
	c.errContainerExecCreate = nil
	c.errContainerExecAttach = nil
	c.errContainerExecStart = nil
	c.errCopyFromContainer = nil
	c.errAttachConn = nil
	c.errEvent = nil
	c.errCopyReader = false
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

func (c *testClient) ContainerStart(ctx context.Context, id string, options types.ContainerStartOptions) error {
	c.callsContainerStart = append(c.callsContainerStart, testCallContainerStart{ctx, id, options})

	return c.errContainerStart
}

func makeTestContainer(id string) types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			Name: fmt.Sprintf("/%s", id[3:]),
			ID:   id,
		},
		NetworkSettings: &types.NetworkSettings{
			DefaultNetworkSettings: types.DefaultNetworkSettings{
				IPAddress: fmt.Sprintf("ip:%s", id[3:]),
			},
		},
	}
}

func (c *testClient) ContainerInspect(ctx context.Context, id string) (types.ContainerJSON, error) {
	c.callsContainerInspect = append(c.callsContainerInspect, testCallContainerInspect{ctx, id})

	return makeTestContainer(id), c.errContainerInspect
}

func (c *testClient) ContainerStats(context.Context, string, bool) (types.ContainerStats, error) {
	buffer := new(bytes.Buffer)

	enc := json.NewEncoder(buffer)
	enc.Encode(&types.StatsJSON{
		Networks: map[string]types.NetworkStats{
			"eth0": types.NetworkStats{},
		},
		Stats: types.Stats{
			CPUStats:    types.CPUStats{},
			MemoryStats: types.MemoryStats{},
		},
	})

	return types.ContainerStats{Body: ioutil.NopCloser(buffer)}, c.errContainerStats
}

func (c *testClient) ContainerExecCreate(ctx context.Context, id string, options types.ExecConfig) (types.IDResponse, error) {
	c.callsExecCreate = append(c.callsExecCreate, testCallExecCreate{ctx, id, options})

	return types.IDResponse{ID: testExecID}, c.errContainerExecCreate
}

func (c *testClient) ContainerExecAttach(ctx context.Context, id string, options types.ExecConfig) (types.HijackedResponse, error) {
	buffer := new(bytes.Buffer)
	c.callsExecAttach = append(c.callsExecAttach, testCallExecAttach{ctx, id, options, buffer})

	conn := &testConn{
		buffer: buffer,
		err:    c.errAttachConn,
	}

	return types.HijackedResponse{Conn: conn}, c.errContainerExecAttach
}

func (c *testClient) ContainerExecStart(ctx context.Context, id string, options types.ExecStartCheck) error {
	c.callsExecStart = append(c.callsExecStart, testCallExecStart{ctx, id, options})

	return c.errContainerExecStart
}

func (c *testClient) ContainerStop(ctx context.Context, id string, t *time.Duration) error {
	c.callsContainerStop = append(c.callsContainerStop, testCallContainerStop{ctx, id, t})

	return c.errContainerStop
}

func (c *testClient) CopyFromContainer(context.Context, string, string) (io.ReadCloser, types.ContainerPathStat, error) {
	if c.errCopyReader {
		r, _ := io.Pipe()
		r.Close()
		return r, types.ContainerPathStat{}, nil
	}

	reader, writer := io.Pipe()
	tw := tar.NewWriter(writer)

	go func() {
		content := "test"
		tw.WriteHeader(&tar.Header{Name: "abc", Mode: 0600, Size: int64(len(content))})
		tw.Write([]byte(content))
		tw.Close()
	}()

	return reader, types.ContainerPathStat{}, c.errCopyFromContainer
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
			Status: "exec_die",
			Actor: events.Actor{
				Attributes: map[string]string{
					"execID":   testExecID,
					"exitCode": "0",
				},
			},
		}
	}

	return ch, nil
}

const (
	testImage  = "path/to/image"
	testExecID = "feeddaed"
)

var (
	testCmd  = []string{"cmd"}
	testArgs = []string{"arg1"}
)

func newTestStrategy(t *testing.T) (*Strategy, func()) {
	out, err := ioutil.TempDir(os.TempDir(), "simnet-docker-test")
	require.NoError(t, err)

	cleaner := func() {
		os.RemoveAll(out)
	}

	return &Strategy{
		cli: &testClient{numContainers: 3},
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
			sim.WithFileMapper(sim.FilesKey("testFiles"), sim.FileMapper{
				Path: "/path/to/file",
				Mapper: func(io.Reader) (interface{}, error) {
					return nil, nil
				},
			}),
		}),
		containers: make([]types.ContainerJSON, 0),
		stats: &metrics.Stats{
			Nodes: make(map[string]metrics.NodeStats),
		},
		encoder: jsonEncoder,
	}, cleaner
}

func newTestStrategyWithClient(t *testing.T, client *testClient) (*Strategy, func()) {
	s, cleaner := newTestStrategy(t)
	s.cli = client

	return s, cleaner
}
