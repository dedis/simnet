package kubernetes

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	restfake "k8s.io/client-go/rest/fake"
	testcore "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
)

const testTimeout = 500 * time.Millisecond

func TestEngine_CreateDeployments(t *testing.T) {
	n := 3
	engine, client := makeEngine(n)

	w, err := engine.CreateDeployment()
	require.NoError(t, err)
	defer w.Stop()

	for i := 0; i < n; i++ {
		select {
		case <-w.ResultChan():
			// TODO: check deployment
		case <-time.After(testTimeout):
			t.Fatal("timeout")
		}
	}

	for _, act := range client.Actions() {
		if wa, ok := act.(testcore.WatchActionImpl); ok {
			value, has := wa.WatchRestrictions.Labels.RequiresExactMatch(LabelID)
			if has {
				require.Equal(t, AppID, value)
				return
			}
		}
	}

	t.Fatal("watch action not found")
}

func TestEngine_CreateDeploymentFailure(t *testing.T) {
	n := 3
	engine, client := makeEngine(n)

	// First test that the error on creation is handled.
	e := errors.New("create error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})
	_, err := engine.CreateDeployment()
	require.Error(t, err)
	require.Equal(t, e, err)

	// Then test that the watcher error is handled aswell.
	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("*", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.CreateDeployment()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_WaitDeployment(t *testing.T) {
	n := 3
	engine, _ := makeEngine(n)

	w := watch.NewFakeWithChanSize(n, false)

	for i := 0; i < n; i++ {
		w.Modify(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node%d", i),
			},
			Status: appsv1.DeploymentStatus{
				AvailableReplicas: 1,
			},
		})
	}

	require.NoError(t, engine.WaitDeployment(w, testTimeout))
}

func TestEngine_WaitDeploymentFailure(t *testing.T) {
	engine, _ := makeEngine(0)

	w := watch.NewFake()
	err := engine.WaitDeployment(w, time.Millisecond)
	require.Error(t, err)
	require.Equal(t, "timeout", err.Error())
}

func TestEngine_FetchPods(t *testing.T) {
	list := &apiv1.PodList{
		Items: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "a",
					Labels: map[string]string{LabelID: AppID},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "b"},
			},
		},
	}
	engine := newKubeEngineTest(fake.NewSimpleClientset(list), "", []string{"a"})

	pods, err := engine.FetchPods()
	require.NoError(t, err)
	require.Equal(t, 1, len(pods))
	require.Equal(t, 1, len(engine.pods))
}

func TestEngine_FetchPodsFailure(t *testing.T) {
	engine, client := makeEngine(0)

	e := errors.New("list error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.FetchPods()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_DeployRouter(t *testing.T) {
	engine, _ := makeEngine(3)

	pods := []apiv1.Pod{
		{
			Spec: apiv1.PodSpec{
				Containers: []apiv1.Container{
					{
						Ports: []apiv1.ContainerPort{
							{
								ContainerPort: 2000,
							},
						},
					},
				},
			},
		},
	}

	w, err := engine.DeployRouter(pods)
	require.NoError(t, err)
	w.Stop()

	select {
	case <-time.After(testTimeout):
		t.Fatal("timeout")
	case <-w.ResultChan():
	}
}

func TestEngine_DeployRouterFailure(t *testing.T) {
	engine, client := makeEngine(0)

	e := errors.New("create error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.DeployRouter([]apiv1.Pod{})
	require.Error(t, err)
	require.Equal(t, e, err)

	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("*", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.DeployRouter([]apiv1.Pod{})
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_WaitRouter(t *testing.T) {
	engine := newKubeEngineTest(fake.NewSimpleClientset(), "", nil)

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
		},
	})

	err := engine.WaitRouter(w)
	require.NoError(t, err)
}

func TestEngine_FetchRouter(t *testing.T) {
	list := &apiv1.PodList{
		Items: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "a",
					Labels: map[string]string{LabelID: RouterID},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "b"},
			},
		},
	}
	engine := newKubeEngineTest(fake.NewSimpleClientset(list), "", nil)

	router, err := engine.FetchRouter()
	require.NoError(t, err)
	require.Equal(t, "a", router.ObjectMeta.Name)
}

func TestEngine_FetchRouterFailure(t *testing.T) {
	engine, client := makeEngine(0)

	_, err := engine.FetchRouter()
	require.Error(t, err)
	require.Equal(t, "invalid number of pods", err.Error())

	e := errors.New("list error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err = engine.FetchRouter()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_Delete(t *testing.T) {
	actions := make(chan testcore.Action, 1)
	client := fake.NewSimpleClientset()
	// As the fake client does not implement the delete collection action, we
	// test differently by listening for the action.
	client.AddReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		actions <- action
		return true, nil, nil
	})

	engine := newKubeEngineTest(client, "", nil)

	w, err := engine.DeleteAll()
	require.NoError(t, err)
	defer w.Stop()

	act := <-actions
	require.NotNil(t, act)

	labels := act.(testcore.DeleteCollectionActionImpl).ListRestrictions.Labels
	value, has := labels.RequiresExactMatch(LabelApp)
	require.True(t, has)
	require.Equal(t, AppName, value)
}

func TestEngine_DeleteFailure(t *testing.T) {
	engine, client := makeEngine(0)

	e := errors.New("delete error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.DeleteAll()
	require.Error(t, err)
	require.Equal(t, e, err)

	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("*", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.DeleteAll()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_WaitDeletion(t *testing.T) {
	engine, _ := makeEngine(0)

	w := watch.NewFakeWithChanSize(1, false)

	w.Delete(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas:   0,
			UnavailableReplicas: 0,
		},
	})

	err := engine.WaitDeletion(w, testTimeout)
	require.NoError(t, err)
}

func TestEngine_WaitDeletionFailure(t *testing.T) {
	engine, _ := makeEngine(0)

	fw := watch.NewFake()
	err := engine.WaitDeletion(fw, time.Millisecond)
	require.Error(t, err)
	require.Equal(t, "timeout", err.Error())
}

func TestEngine_ReadPod(t *testing.T) {
	engine := kubeEngine{
		client:          fake.NewSimpleClientset(),
		restclient:      &restfake.RESTClient{},
		config:          &rest.Config{},
		executorFactory: testExecutorFactory,
	}

	reader, err := engine.ReadFromPod("pod", "container", "this/is/a/file")
	require.NoError(t, err)

	data, err := ioutil.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("deadbeef"), data)
}

func TestEngine_ReadPodFailure(t *testing.T) {
	engine := kubeEngine{
		client:          fake.NewSimpleClientset(),
		restclient:      &restfake.RESTClient{},
		executorFactory: testExecutorFactory,
	}

	_, err := engine.ReadFromPod("", "", "")
	require.Error(t, err)
	require.Equal(t, "missing config", err.Error())

	engine.config = &rest.Config{}
	engine.executorFactory = testFailingExecutorFactory
	reader, err := engine.ReadFromPod("", "", "")
	require.NoError(t, err)

	_, err = reader.Read(make([]byte, 1))
	require.Error(t, err)
	require.Equal(t, "io: read/write on closed pipe", err.Error())
}

func TestEngine_MakeTunnel(t *testing.T) {
	engine := kubeEngine{
		pods: []apiv1.Pod{{}},
	}

	tun := engine.MakeTunnel()
	require.NotNil(t, tun)
	require.Equal(t, 1, len(tun.engine.pods))
}

func newKubeEngineTest(client kubernetes.Interface, ns string, nodes []string) *kubeEngine {
	return &kubeEngine{
		client:    client,
		namespace: ns,
		nodes:     nodes,
	}
}

func makeEngine(n int) (*kubeEngine, *fake.Clientset) {
	client := fake.NewSimpleClientset()

	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node%d", i)
	}

	engine := newKubeEngineTest(client, "", nodes)

	return engine, client
}

type fakeExecutor struct {
	config *rest.Config
	method string
	url    *url.URL
	err    error
}

func testExecutorFactory(c *rest.Config, m string, u *url.URL) (remotecommand.Executor, error) {
	if c == nil {
		return nil, errors.New("missing config")
	}

	return &fakeExecutor{c, m, u, nil}, nil
}

func testFailingExecutorFactory(c *rest.Config, m string, u *url.URL) (remotecommand.Executor, error) {
	return &fakeExecutor{c, m, u, errors.New("stream error")}, nil
}

func (e *fakeExecutor) Stream(options remotecommand.StreamOptions) error {
	if e.err != nil {
		return e.err
	}

	if options.Stdout != nil {
		_, err := options.Stdout.Write([]byte("deadbeef"))
		return err
	}

	return nil
}
