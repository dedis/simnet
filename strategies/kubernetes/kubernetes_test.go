package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/strategies"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
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

func TestKubernetesEngine_New(t *testing.T) {
	// It should fail as the file does not exist
	_, err := NewEngine("abc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "config: ")

	// Now let's try with an actual file.
	f, err := ioutil.TempFile(os.TempDir(), "")
	require.NoError(t, err)
	defer f.Close()
	defer os.Remove(f.Name())

	_, err = f.Write([]byte(testConfig))
	require.NoError(t, err)

	sim, err := NewEngine(f.Name())
	require.NoError(t, err)
	require.NotNil(t, sim)
}

func TestStrategy_DeployWithFailures(t *testing.T) {
	deployer := &testDeployer{}
	stry := &Engine{
		deployer: deployer,
	}

	err := stry.Deploy()
	require.NoError(t, err)

	e := errors.New("fetch router error")
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

	stry := &Engine{
		pods: []apiv1.Pod{
			{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Status: apiv1.PodStatus{PodIP: "a.a.a.a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Status: apiv1.PodStatus{PodIP: "b.b.b.b"}},
		},
		deployer: &testDeployer{},
		options:  NewOptions(options),
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

	stry := &Engine{
		pods:     []apiv1.Pod{{}},
		deployer: &testDeployer{errReadFromPod: e},
		options:  NewOptions(options),
	}

	err := stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, e, err)

	stry.deployer = &testDeployer{}
	err = stry.Execute(testRound{})
	require.Error(t, err)
	require.Equal(t, e2, err)
}

func TestStrategy_WriteStats(t *testing.T) {
	stry := &Engine{
		pods:     []apiv1.Pod{{}},
		deployer: &testDeployer{},
	}

	// Get a temporary file path.
	f, err := ioutil.TempFile(os.TempDir(), "")
	require.NoError(t, err)

	filepath := f.Name()
	require.NoError(t, os.Remove(filepath))

	err = stry.WriteStats(filepath)
	require.NoError(t, err)

	_, err = os.Stat(filepath)
	require.NoError(t, err)
}

func TestStrategy_CleanWithFailure(t *testing.T) {
	stry := &Engine{
		deployer: &testDeployer{},
	}

	err := stry.Clean()
	require.NoError(t, err)

	e := errors.New("delete all error")
	stry.deployer = &testDeployer{errDeleteAll: e}
	err = stry.Clean()
	require.Error(t, err)
	require.Equal(t, e, err)

	e = errors.New("delete wait error")
	stry.deployer = &testDeployer{errWaitDeletion: e}
	err = stry.Clean()
	require.Error(t, err)
	require.Equal(t, e, err)
}

type testDeployer struct {
	errDeployment     error
	errWaitDeployment error
	errFetchPods      error
	errDeployRouter   error
	errWaitRouter     error
	errFetchRouter    error
	errDeleteAll      error
	errWaitDeletion   error
	errReadFromPod    error
}

func (td *testDeployer) CreateDeployment() (watch.Interface, error) {
	return watch.NewFake(), td.errDeployment
}

func (td *testDeployer) WaitDeployment(watch.Interface, time.Duration) error {
	return td.errWaitDeployment
}

func (td *testDeployer) FetchPods() ([]apiv1.Pod, error) {
	return nil, td.errFetchPods
}

func (td *testDeployer) DeployRouter([]apiv1.Pod) (watch.Interface, error) {
	return watch.NewFake(), td.errDeployRouter
}

func (td *testDeployer) WaitRouter(watch.Interface) error {
	return td.errWaitRouter
}

func (td *testDeployer) FetchRouter() (apiv1.Pod, error) {
	return apiv1.Pod{}, td.errFetchRouter
}

func (td *testDeployer) DeleteAll() (watch.Interface, error) {
	return watch.NewFake(), td.errDeleteAll
}

func (td *testDeployer) WaitDeletion(watch.Interface, time.Duration) error {
	return td.errWaitDeletion
}

func (td *testDeployer) ReadFromPod(string, string, string) (io.Reader, error) {
	return bytes.NewReader([]byte{}), td.errReadFromPod
}

func (td *testDeployer) MakeTunnel() Tunnel {
	return Tunnel{}
}

type testRound struct {
	h func(context.Context)
}

func (tr testRound) Execute(ctx context.Context, tun strategies.Tunnel) {
	if tr.h != nil {
		tr.h(ctx)
	}
}
