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
	"go.dedis.ch/simnet/sim"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

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

func TestStrategy_NewFailures(t *testing.T) {
	defer setMockClientConfig()

	e := errors.New("namespace error")
	setMockBadClientConfig(e, nil)
	_, err := NewStrategy("")
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())

	e = errors.New("config error")
	setMockBadClientConfig(nil, e)
	_, err = NewStrategy("")
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())

	setMockBadClientConfig(nil, nil)
	_, err = NewStrategy("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't create the engine")

	// Expect an error as the output directory cannot be created.
	_, err = NewStrategy("", sim.WithOutput("/abc"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't create the data folder")
}

func TestStrategy_DeployWithFailures(t *testing.T) {
	deployer := &testEngine{}
	stry := &Strategy{
		engine:  deployer,
		options: sim.NewOptions(nil),
		tun:     testTunnel{},
	}

	err := stry.Deploy()
	require.NoError(t, err)

	// Errors are tested in reverse order to avoid to reset err fields
	// all the time.

	e := errors.New("tunnel start error")
	stry.tun = testTunnel{err: e}
	err = stry.Deploy()
	require.Equal(t, e, err)

	e = errors.New("fetch router error")
	deployer.errFetchCerts = e
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
	key := sim.FilesKey("a")
	options := []sim.Option{
		sim.WithFileMapper(key, sim.FileMapper{
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
		options: sim.NewOptions(options),
	}

	done := make(chan struct{}, 1)
	h := func(ctx context.Context) {
		contents := ctx.Value(key).(sim.Files)
		require.Equal(t, len(stry.pods), len(contents))
		require.Equal(t, "content", contents[sim.Identifier{Index: 0, ID: "a", IP: "a.a.a.a"}])

		nodes := ctx.Value(sim.NodeInfoKey{}).([]sim.NodeInfo)
		require.Len(t, nodes, len(stry.pods))
		for i, node := range nodes {
			require.Equal(t, stry.pods[i].Name, node.Name)
			require.Equal(t, stry.pods[i].Status.PodIP, node.Address)
		}

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
	options := []sim.Option{
		sim.WithFileMapper(sim.FilesKey(""), sim.FileMapper{
			Path: "/a/file/path",
			Mapper: func(io.Reader) (interface{}, error) {
				return nil, e2
			},
		}),
	}

	stry := &Strategy{
		pods:    []apiv1.Pod{{}},
		engine:  &testEngine{errRead: e},
		options: sim.NewOptions(options),
	}

	err := stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, e, err)

	e = errors.New("stream error")
	stry.engine = &testEngine{errStreamLogs: e}
	err = stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, err, e)

	stry.engine = &testEngine{}
	err = stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, e2, err)

	stry.options = sim.NewOptions([]sim.Option{})
	e = errors.New("round error")
	err = stry.Execute(testRound{err: e})
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("configuration error")
	err = stry.Execute(testRound{errCfg: e})
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

func TestStrategy_WriteStats(t *testing.T) {
	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	reader, writer := io.Pipe()
	stry := &Strategy{
		pods:        []apiv1.Pod{{}},
		engine:      &testEngine{reader: reader},
		makeEncoder: makeJSONEncoder,
		options: &sim.Options{
			OutputDir: dir,
		},
	}

	writer.Close()

	file := "data.json"
	err = stry.WriteStats(file)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, file))
	require.NoError(t, err)
}

func TestStrategy_WriteStatsFailures(t *testing.T) {
	e := errors.New("read stats error")
	stry := &Strategy{
		pods:    []apiv1.Pod{{}},
		engine:  &testEngine{errRead: e},
		options: &sim.Options{},
	}

	err := stry.WriteStats("")
	require.Error(t, err)
	require.Equal(t, e, err)

	stry.engine = &testEngine{}

	err = stry.WriteStats("")
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	stry.makeEncoder = newBadEncoder
	stry.options.OutputDir = dir
	err = stry.WriteStats("data.json")
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
	stry.tun = testTunnel{}
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

func TestStrategy_String(t *testing.T) {
	stry := &Strategy{engine: &testEngine{}}
	require.Equal(t, stry.String(), "engine")
}

type testEngine struct {
	reader            io.ReadCloser
	errDeployment     error
	errWaitDeployment error
	errFetchPods      error
	errUploadConfig   error
	errDeployRouter   error
	errWaitRouter     error
	errFetchCerts     error
	errDeleteAll      error
	errWaitDeletion   error
	errStreamLogs     error
	errRead           error
}

func (te *testEngine) CreateDeployment() (watch.Interface, error) {
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

func (te *testEngine) WaitRouter(watch.Interface) (*apiv1.ServicePort, string, error) {
	return &apiv1.ServicePort{}, "", te.errWaitRouter
}

func (te *testEngine) FetchCertificates() (sim.Certificates, error) {
	return sim.Certificates{}, te.errFetchCerts
}

func (te *testEngine) DeleteAll() (watch.Interface, error) {
	return watch.NewFake(), te.errDeleteAll
}

func (te *testEngine) WaitDeletion(watch.Interface, time.Duration) error {
	return te.errWaitDeletion
}

func (te *testEngine) StreamLogs(<-chan struct{}) error {
	return te.errStreamLogs
}

func (te *testEngine) ReadStats(string, time.Time, time.Time) (metrics.NodeStats, error) {
	return metrics.NodeStats{}, te.errRead
}

func (te *testEngine) Read(pod, path string) (io.ReadCloser, error) {
	return nil, te.errRead
}

func (te *testEngine) Write(node, path string, content io.Reader) error {
	return nil
}

func (te *testEngine) Exec(node string, cmd []string, options sim.ExecOptions) error {
	return nil
}

func (te *testEngine) String() string {
	return "engine"
}

type testRound struct {
	h      func(context.Context)
	err    error
	errCfg error
}

func (tr testRound) Configure(simio sim.IO) error {
	return tr.errCfg
}

func (tr testRound) Execute(ctx context.Context, simio sim.IO) error {
	if tr.err != nil {
		return tr.err
	}

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

func (t testTunnel) Start(...sim.TunOption) error {
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

type badClientConfig struct {
	errNamespace error
	errRest      error
}

func (c badClientConfig) ClientConfig() (*rest.Config, error) {
	return &rest.Config{Host: ":"}, c.errRest
}

func (c badClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return nil
}

func (c badClientConfig) Namespace() (string, bool, error) {
	return "", false, c.errNamespace
}

func (c badClientConfig) RawConfig() (clientcmdapi.Config, error) {
	return clientcmdapi.Config{}, nil
}

func setMockClientConfig() {
	newClientConfig = clientcmd.NewNonInteractiveDeferredLoadingClientConfig
}

func setMockBadClientConfig(ns, rest error) {
	newClientConfig = func(clientcmd.ClientConfigLoader, *clientcmd.ConfigOverrides) clientcmd.ClientConfig {
		return badClientConfig{
			errNamespace: ns,
			errRest:      rest,
		}
	}
}
