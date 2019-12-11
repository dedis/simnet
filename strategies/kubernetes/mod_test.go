package kubernetes

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/metrics"
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
		engine: deployer,
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
		engine:  &testEngine{errReadFromPod: e},
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
		pods:   []apiv1.Pod{{}},
		engine: &testEngine{reader: reader},
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

func TestStrategy_CleanWithFailure(t *testing.T) {
	stry := &Strategy{
		engine: &testEngine{},
	}

	err := stry.Clean()
	require.NoError(t, err)

	e := errors.New("delete all error")
	stry.engine = &testEngine{errDeleteAll: e}
	err = stry.Clean()
	require.Error(t, err)
	require.Equal(t, e, err)

	e = errors.New("delete wait error")
	stry.engine = &testEngine{errWaitDeletion: e}
	err = stry.Clean()
	require.Error(t, err)
	require.Equal(t, e, err)
}

type testEngine struct {
	reader            io.ReadCloser
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

func (te *testEngine) CreateDeployment() (watch.Interface, error) {
	return watch.NewFake(), te.errDeployment
}

func (te *testEngine) WaitDeployment(watch.Interface, time.Duration) error {
	return te.errWaitDeployment
}

func (te *testEngine) FetchPods() ([]apiv1.Pod, error) {
	return nil, te.errFetchPods
}

func (te *testEngine) UploadConfig() error {
	return nil
}

func (te *testEngine) DeployRouter([]apiv1.Pod) (watch.Interface, error) {
	return watch.NewFake(), te.errDeployRouter
}

func (te *testEngine) WaitRouter(watch.Interface) error {
	return te.errWaitRouter
}

func (te *testEngine) InitVPN() (Tunnel, error) {
	return testVPN{}, te.errFetchRouter
}

func (te *testEngine) DeleteAll() (watch.Interface, error) {
	return watch.NewFake(), te.errDeleteAll
}

func (te *testEngine) WaitDeletion(watch.Interface, time.Duration) error {
	return te.errWaitDeletion
}

func (te *testEngine) ReadStats(string) (metrics.NodeStats, error) {
	return metrics.NodeStats{}, nil
}

func (te *testEngine) ReadFile(string, string) (io.Reader, error) {
	return te.reader, te.errReadFromPod
}

type testRound struct {
	h func(context.Context)
}

func (tr testRound) Execute(ctx context.Context) {
	if tr.h != nil {
		tr.h(ctx)
	}
}

type testVPN struct{}

func (v testVPN) Start() error {
	return nil
}

func (v testVPN) Stop() error {
	return nil
}
