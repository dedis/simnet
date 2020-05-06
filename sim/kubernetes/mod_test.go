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
	"golang.org/x/xerrors"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func TestStrategy_Option(t *testing.T) {
	stry := &Strategy{options: sim.NewOptions(nil)}
	stry.Option(sim.WithVPN("abc"))
	require.Equal(t, stry.options.VPNExecutable, "abc")
}

func TestStrategy_DeployWithFailures(t *testing.T) {
	deployer := &testEngine{}
	stry := &Strategy{
		engine:  deployer,
		options: sim.NewOptions(nil),
		tun:     testTunnel{},
	}

	err := stry.Deploy(context.Background(), &testRound{})
	require.NoError(t, err)

	// Errors are tested in reverse order to avoid to reset err fields
	// all the time.

	e := errors.New("configuration error")
	err = stry.Deploy(context.Background(), &testRound{errBefore: e})
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("tunnel start error")
	stry.tun = testTunnel{err: e}
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	e = errors.New("fetch router error")
	deployer.errFetchCerts = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	e = errors.New("wait router error")
	deployer.errWaitRouter = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	e = errors.New("deploy router error")
	deployer.errDeployRouter = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	e = errors.New("upload config error")
	deployer.errUploadConfig = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	deployer.errStreamLogs = xerrors.New("oops")
	err = stry.Deploy(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't stream logs: oops")

	e = errors.New("fetch pods error")
	deployer.errFetchPods = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	e = errors.New("wait deployment error")
	deployer.errWaitDeployment = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)

	e = errors.New("create deployment error")
	deployer.errDeployment = e
	err = stry.Deploy(context.Background(), &testRound{})
	require.Equal(t, e, err)
}

func TestStrategy_Execute(t *testing.T) {
	options := []sim.Option{}

	stry := &Strategy{
		pods: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelNode: "a"}},
				Status:     apiv1.PodStatus{PodIP: "a.a.a.a"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelNode: "b"}},
				Status:     apiv1.PodStatus{PodIP: "b.b.b.b"},
			},
		},
		engine:  &testEngine{},
		options: sim.NewOptions(options),
		updated: true,
	}

	round := &testRound{}

	err := stry.Execute(context.Background(), round)
	require.NoError(t, err)
	require.True(t, stry.doneTime.After(stry.executeTime))
	require.True(t, round.afterDone)
	require.Len(t, round.nodes, 2)
	require.Equal(t, "a", round.nodes[0].Name)
	require.Equal(t, "b", round.nodes[1].Name)
}

func TestStrategy_ExecuteFailure(t *testing.T) {
	stry := &Strategy{
		pods:    []apiv1.Pod{{}},
		engine:  &testEngine{},
		options: sim.NewOptions(nil),
	}

	e := errors.New("stream error")
	stry.engine = &testEngine{errStreamLogs: e}
	err := stry.Execute(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't stream logs: stream error")

	stry.engine = &testEngine{}
	e = errors.New("round error")
	err = stry.Execute(context.Background(), &testRound{err: e})
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	e = errors.New("after error")
	err = stry.Execute(context.Background(), &testRound{errAfter: e})
	require.Error(t, err)
	require.True(t, errors.Is(err, e))

	stry.engine = &testEngine{errFetchPods: xerrors.New("oops")}
	err = stry.Execute(context.Background(), &testRound{})
	require.EqualError(t, err, "couldn't fetch pods: oops")
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
	err = stry.WriteStats(context.Background(), file)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, file))
	require.NoError(t, err)
}

func TestStrategy_WriteStatsFailures(t *testing.T) {
	e := errors.New("read stats error")
	stry := &Strategy{
		updated: true,
		pods:    []apiv1.Pod{{}},
		engine:  &testEngine{errRead: e},
		options: &sim.Options{},
	}

	err := stry.WriteStats(context.Background(), "")
	require.Error(t, err)
	require.Equal(t, e, err)

	stry.engine = &testEngine{}

	err = stry.WriteStats(context.Background(), "")
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	stry.makeEncoder = newBadEncoder
	stry.options.OutputDir = dir
	err = stry.WriteStats(context.Background(), "data.json")
	require.Error(t, err)
	require.Equal(t, "encoding error", err.Error())
}

func TestStrategy_Clean(t *testing.T) {
	stry := &Strategy{
		engine: &testEngine{},
		tun:    testTunnel{},
	}

	err := stry.Clean(context.Background())
	require.NoError(t, err)
}

func TestStrategy_CleanWithFailure(t *testing.T) {
	e := errors.New("tunnel error")
	stry := &Strategy{
		tun:    testTunnel{err: e},
		engine: &testEngine{},
	}

	err := stry.Clean(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())

	e = errors.New("delete all error")
	stry.tun = testTunnel{}
	stry.engine = &testEngine{errDeleteAll: e}
	err = stry.Clean(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())

	e = errors.New("delete wait error")
	stry.engine = &testEngine{errWaitDeletion: e}
	err = stry.Clean(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), e.Error())
}

func TestStrategy_String(t *testing.T) {
	stry := &Strategy{engine: &testEngine{}}
	require.Equal(t, stry.String(), "engine")
}

func TestWithResources(t *testing.T) {
	options := sim.NewOptions([]sim.Option{WithResources("200m", "32Mi")})
	require.Equal(t, resource.MustParse("32Mi"), options.Data[OptionMemoryAlloc].(resource.Quantity))
	require.Equal(t, resource.MustParse("200m"), options.Data[OptionCPUAlloc].(resource.Quantity))
}

type testEngine struct {
	engine
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

func (te *testEngine) GetTags() map[int64]string {
	return map[int64]string{
		time.Now().Unix(): "tag",
	}
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

func (te *testEngine) StreamLogs(context.Context) error {
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
	nodes     []sim.NodeInfo
	afterDone bool
	err       error
	errBefore error
	errAfter  error
}

func (tr *testRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	return tr.errBefore
}

func (tr *testRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	if tr.err != nil {
		return tr.err
	}

	tr.nodes = nodes

	return nil
}

func (tr *testRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
	tr.afterDone = true
	return tr.errAfter
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
