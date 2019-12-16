package kubernetes

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/metrics"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

func TestOption_FileMapper(t *testing.T) {
	e := errors.New("oops")
	options := NewOptions([]Option{WithFileMapper(FilesKey("abc"), FileMapper{
		Path: "/path/to/file",
		Mapper: func(r io.Reader) (interface{}, error) {
			return nil, e
		},
	})})

	fm, ok := options.files[FilesKey("abc")]
	require.True(t, ok)
	require.Equal(t, "/path/to/file", fm.Path)
	_, err := fm.Mapper(nil)
	require.Equal(t, e, err)

	fm, ok = options.files[FilesKey("")]
	require.False(t, ok)
}

func TestOption_Topology(t *testing.T) {
	topo := network.NewSimpleTopology(5, 0)
	options := NewOptions([]Option{WithTopology(topo)})

	require.Equal(t, topo.Len(), options.topology.Len())
}

func TestOption_Image(t *testing.T) {
	options := NewOptions([]Option{WithImage(
		"path/to/image",
		[]string{"cmd"},
		[]string{"arg1", "arg2"},
		NewTCP(2000),
		NewUDP(3000),
		NewTCP(3001),
	)})

	require.Equal(t, ContainerAppName, options.container.Name)
	require.Equal(t, "path/to/image", options.container.Image)
	require.Equal(t, "cmd", options.container.Command[0])
	require.Equal(t, []string{"arg1", "arg2"}, options.container.Args)
	require.Equal(t, apiv1.ProtocolTCP, options.container.Ports[0].Protocol)
	require.Equal(t, int32(2000), options.container.Ports[0].ContainerPort)
	require.Equal(t, apiv1.ProtocolUDP, options.container.Ports[1].Protocol)
	require.Equal(t, int32(3000), options.container.Ports[1].ContainerPort)
	require.Equal(t, apiv1.ProtocolTCP, options.container.Ports[2].Protocol)
	require.Equal(t, int32(3001), options.container.Ports[2].ContainerPort)
}

const testConfig = `apiVersion: v1
clusters:
- cluster:
    server: https://127.0.0.1:8443
  name: minikube
contexts:
- context:
    cluster: minikube
    user: minikube
  name: minikube
current-context: minikube
kind: Config
preferences: {}
users:
- name: minikube
`

func TestStrategy_New(t *testing.T) {
	// It should fail as the file does not exist
	_, err := NewStrategy("abc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "config: ")

	// Now let's try with an actual file.
	f, err := ioutil.TempFile(os.TempDir(), "")
	require.NoError(t, err)
	defer f.Close()
	defer os.Remove(f.Name())

	_, err = f.Write([]byte(testConfig))
	require.NoError(t, err)

	sim, err := NewStrategy(f.Name())
	require.NoError(t, err)
	require.NotNil(t, sim)
}

func TestStrategy_DeployWithFailures(t *testing.T) {
	deployer := &testEngine{}
	stry := &Strategy{
		engine:  deployer,
		options: NewOptions(nil),
	}

	err := stry.Deploy()
	require.NoError(t, err)

	// Errors are tested in reverse order to avoid to reset err fields
	// all the time.

	e := errors.New("vpn start error")
	deployer.errVpn = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("fetch router error")
	deployer.errFetchRouter = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("wait router error")
	deployer.errWaitRouter = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("deploy router error")
	deployer.errDeployRouter = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("upload config error")
	deployer.errUploadConfig = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("fetch pods error")
	deployer.errFetchPods = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("wait deployment error")
	deployer.errWaitDeployment = e
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("create deployment error")
	deployer.errDeployment = e
	err = stry.Deploy()
	require.Equal(t, e, err)
}

func TestStrategy_Execute(t *testing.T) {
	key := FilesKey("a")
	options := []Option{
		WithFileMapper(key, FileMapper{
			Path: "/a/file/path",
			Mapper: func(io.Reader) (interface{}, error) {
				return "content", nil
			},
		}),
	}

	stry := &Strategy{
		pods: []apiv1.Pod{
			{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Status: apiv1.PodStatus{PodIP: "a.a.a.a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Status: apiv1.PodStatus{PodIP: "b.b.b.b"}},
		},
		engine:  &testEngine{},
		options: NewOptions(options),
	}

	done := make(chan struct{}, 1)
	h := func(ctx context.Context) {
		contents := ctx.Value(key).(map[string]interface{})
		require.Equal(t, len(stry.pods), len(contents))
		require.Equal(t, "content", contents["a.a.a.a"])

		done <- struct{}{}
	}

	err := stry.Execute(testRound{h: h})
	require.NoError(t, err)
	require.True(t, stry.doneTime.After(stry.executeTime))

	select {
	case <-done:
	default:
		t.Fatal("handler not called")
	}
}

func TestStrategy_ExecuteFailure(t *testing.T) {
	e := errors.New("read error")
	e2 := errors.New("mapper error")
	options := []Option{
		WithFileMapper(FilesKey(""), FileMapper{
			Path: "/a/file/path",
			Mapper: func(io.Reader) (interface{}, error) {
				return nil, e2
			},
		}),
	}

	stry := &Strategy{
		pods:    []apiv1.Pod{{}},
		engine:  &testEngine{errRead: e},
		options: NewOptions(options),
	}

	err := stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, e, err)

	stry.engine = &testEngine{}
	err = stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, e2, err)
}

func TestStrategy_WriteStats(t *testing.T) {
	reader, writer := io.Pipe()
	stry := &Strategy{
		pods:        []apiv1.Pod{{}},
		engine:      &testEngine{reader: reader},
		makeEncoder: makeJSONEncoder,
	}

	// Get a temporary file path.
	f, err := ioutil.TempFile(os.TempDir(), "")
	require.NoError(t, err)

	filepath := f.Name()
	require.NoError(t, os.Remove(filepath))

	writer.Close()

	err = stry.WriteStats(filepath)
	require.NoError(t, err)

	_, err = os.Stat(filepath)
	require.NoError(t, err)
}

func TestStrategy_WriteStatsFailures(t *testing.T) {
	e := errors.New("read stats error")
	stry := &Strategy{
		pods:   []apiv1.Pod{{}},
		engine: &testEngine{errRead: e},
	}

	err := stry.WriteStats("")
	require.Error(t, err)
	require.Equal(t, e, err)

	stry.engine = &testEngine{}

	err = stry.WriteStats("")
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	stry.makeEncoder = newBadEncoder
	err = stry.WriteStats(filepath.Join(dir, "data"))
	require.Error(t, err)
	require.Equal(t, "encoding error", err.Error())
}

func TestStrategy_Clean(t *testing.T) {
	stry := &Strategy{
		engine: &testEngine{},
		tun:    testTunnel{},
	}

	err := stry.Clean()
	require.NoError(t, err)
}

func TestStrategy_CleanWithFailure(t *testing.T) {
	e := errors.New("tunnel error")
	stry := &Strategy{
		tun:    testTunnel{err: e},
		engine: &testEngine{},
	}

	err := stry.Clean()
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())

	e = errors.New("delete all error")
	stry.tun = nil
	stry.engine = &testEngine{errDeleteAll: e}
	err = stry.Clean()
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())

	e = errors.New("delete wait error")
	stry.engine = &testEngine{errWaitDeletion: e}
	err = stry.Clean()
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())
}

type testEngine struct {
	reader            io.ReadCloser
	errDeployment     error
	errWaitDeployment error
	errFetchPods      error
	errUploadConfig   error
	errDeployRouter   error
	errWaitRouter     error
	errFetchRouter    error
	errDeleteAll      error
	errWaitDeletion   error
	errRead           error
	errVpn            error
}

func (te *testEngine) CreateDeployment(container apiv1.Container) (watch.Interface, error) {
	return watch.NewFake(), te.errDeployment
}

func (te *testEngine) WaitDeployment(watch.Interface) error {
	return te.errWaitDeployment
}

func (te *testEngine) FetchPods() ([]apiv1.Pod, error) {
	return nil, te.errFetchPods
}

func (te *testEngine) UploadConfig() error {
	return te.errUploadConfig
}

func (te *testEngine) DeployRouter([]apiv1.Pod) (watch.Interface, error) {
	return watch.NewFake(), te.errDeployRouter
}

func (te *testEngine) WaitRouter(watch.Interface) error {
	return te.errWaitRouter
}

func (te *testEngine) InitVPN() (sim.Tunnel, error) {
	return testVPN{err: te.errVpn}, te.errFetchRouter
}

func (te *testEngine) DeleteAll() (watch.Interface, error) {
	return watch.NewFake(), te.errDeleteAll
}

func (te *testEngine) WaitDeletion(watch.Interface, time.Duration) error {
	return te.errWaitDeletion
}

func (te *testEngine) ReadStats(string, time.Time, time.Time) (metrics.NodeStats, error) {
	return metrics.NodeStats{}, te.errRead
}

func (te *testEngine) ReadFile(string, string) (io.Reader, error) {
	return te.reader, te.errRead
}

type testRound struct {
	h func(context.Context)
}

func (tr testRound) Execute(ctx context.Context) error {
	if tr.h != nil {
		tr.h(ctx)
	}

	return nil
}

type testVPN struct {
	err error
}

func (v testVPN) Start() error {
	return v.err
}

func (v testVPN) Stop() error {
	return v.err
}

type testTunnel struct {
	err error
}

func (t testTunnel) Start() error {
	return t.err
}

func (t testTunnel) Stop() error {
	return t.err
}

type testBadEncoder struct{}

func newBadEncoder(io.Writer) Encoder {
	return testBadEncoder{}
}

func (e testBadEncoder) Encode(interface{}) error {
	return errors.New("encoding error")
}
