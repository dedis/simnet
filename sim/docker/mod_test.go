package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	s, clean := newTestStrategy(t)
	defer clean()

	err := s.Deploy()
	require.NoError(t, err)
}

func TestStrategy_PullImageFailures(t *testing.T) {
	e := errors.New("pull image error")
	s, clean := newTestStrategyWithClient(t, &testClient{numContainers: 3, errImagePull: e})
	defer clean()

	err := s.Deploy()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
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

	s.containers = []types.ContainerJSON{makeTestContainer()}

	e := errors.New("pull netem image error")
	client.errImagePull = e

	err := s.configureContainers(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

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

	s.containers = []types.ContainerJSON{makeTestContainer()}

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

	s.containers = []types.ContainerJSON{makeTestContainer()}

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

	s.containers = []types.ContainerJSON{makeTestContainer()}

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

	s.containers = []types.ContainerJSON{makeTestContainer()}

	err := s.Clean()
	require.NoError(t, err)
}

func TestStrategy_CleanFailures(t *testing.T) {
	client := &testClient{}
	s, clean := newTestStrategyWithClient(t, client)
	defer clean()

	s.containers = []types.ContainerJSON{makeTestContainer()}

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

type testClient struct {
	*client.Client
	numContainers int

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

func (c *testClient) ImagePull(context.Context, string, types.ImagePullOptions) (io.ReadCloser, error) {
	reader, _ := io.Pipe()
	reader.Close()
	return reader, c.errImagePull
}

func (c *testClient) ContainerCreate(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, string) (container.ContainerCreateCreatedBody, error) {
	return container.ContainerCreateCreatedBody{}, c.errContainerCreate
}

func (c *testClient) ContainerStart(context.Context, string, types.ContainerStartOptions) error {
	return c.errContainerStart
}

func makeTestContainer() types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{Name: "/node0"},
		NetworkSettings: &types.NetworkSettings{
			DefaultNetworkSettings: types.DefaultNetworkSettings{
				IPAddress: "1.2.3.4",
			},
		},
	}
}

func (c *testClient) ContainerInspect(context.Context, string) (types.ContainerJSON, error) {
	return makeTestContainer(), c.errContainerInspect
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

func (c *testClient) ContainerExecCreate(context.Context, string, types.ExecConfig) (types.IDResponse, error) {
	return types.IDResponse{ID: "123"}, c.errContainerExecCreate
}

func (c *testClient) ContainerExecAttach(context.Context, string, types.ExecConfig) (types.HijackedResponse, error) {
	conn := &testConn{
		buffer: new(bytes.Buffer),
		err:    c.errAttachConn,
	}

	return types.HijackedResponse{Conn: conn}, c.errContainerExecAttach
}

func (c *testClient) ContainerExecStart(context.Context, string, types.ExecStartCheck) error {
	return c.errContainerExecStart
}

func (c *testClient) ContainerStop(context.Context, string, *time.Duration) error {
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

func (c *testClient) Events(context.Context, types.EventsOptions) (<-chan events.Message, <-chan error) {
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
					"execID":   "123",
					"exitCode": "0",
				},
			},
		}
	}

	return ch, nil
}

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
			sim.WithTopology(snet.NewSimpleTopology(3, 0)),
			sim.WithImage(
				"path/to/image",
				[]string{"cmd"},
				[]string{"arg1"},
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
