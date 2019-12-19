package kubernetes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	testcore "k8s.io/client-go/testing"
)

const testTimeout = 500 * time.Millisecond

func init() {
	setMockTunnel()
}

func TestEngine_NewFailures(t *testing.T) {
	setMockBadClient()
	defer setMockClient()

	engine, err := newKubeEngine(nil, "", &Options{})
	require.Error(t, err)
	require.Equal(t, "client: client error", err.Error())
	require.Nil(t, engine)
}

func TestEngine_CreateDeployments(t *testing.T) {
	n := 3
	engine, client := makeEngine(n)

	w, err := engine.CreateDeployment(apiv1.Container{})
	require.NoError(t, err)
	defer w.Stop()

	for i := 0; i < n; i++ {
		select {
		case evt := <-w.ResultChan():
			dpl, ok := evt.Object.(*appsv1.Deployment)
			require.True(t, ok)
			require.Equal(t, 2, len(dpl.Spec.Template.Spec.Containers))
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
	_, err := engine.CreateDeployment(apiv1.Container{})
	require.Error(t, err)
	require.Equal(t, e, err)

	// Then test that the watcher error is handled aswell.
	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("*", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.CreateDeployment(apiv1.Container{})
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

	require.NoError(t, engine.WaitDeployment(w))
}

func TestEngine_WaitDeploymentFailure(t *testing.T) {
	engine, _ := makeEngine(3)

	reason := "oops"

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: apiv1.ConditionFalse,
				},
				{
					Type:   appsv1.DeploymentProgressing,
					Status: apiv1.ConditionFalse,
					Reason: reason,
				},
			},
		},
	})

	err := engine.WaitDeployment(w)
	require.Error(t, err)
	require.Equal(t, reason, err.Error())
}

func TestEngine_FetchPods(t *testing.T) {
	list := &apiv1.PodList{
		Items: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node0",
					Labels: map[string]string{LabelID: AppID},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			},
		},
	}
	engine := newKubeEngineTest(fake.NewSimpleClientset(list), "", 1)

	pods, err := engine.FetchPods()
	require.NoError(t, err)
	require.Equal(t, 1, len(pods))
	require.Equal(t, 1, len(engine.pods))
}

func TestEngine_FetchPodsFailure(t *testing.T) {
	engine, client := makeEngine(1)

	_, err := engine.FetchPods()
	require.Error(t, err)

	e := errors.New("list error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err = engine.FetchPods()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_UploadConfig(t *testing.T) {
	engine, _ := makeEngine(3)
	engine.pods = []apiv1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelNode: "node1",
				},
			},
		},
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		dec := json.NewDecoder(engine.fs.(*testFS).reader)

		var rules []network.Rule
		err := dec.Decode(&rules)
		require.NoError(t, err)
		require.Equal(t, 1, len(rules))
	}()

	err := engine.UploadConfig()
	require.NoError(t, err)

	wg.Wait()
}

func TestEngine_UploadConfigFailures(t *testing.T) {
	engine, _ := makeEngine(1)
	engine.pods = []apiv1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelNode: "node1",
				},
			},
		},
	}

	e := errors.New("writing error")
	engine.fs = &testFS{err: e}

	err := engine.UploadConfig()
	require.Error(t, err)
	require.Equal(t, e, err)

	_, writer := io.Pipe()
	writer.Close()
	engine.fs = &testFS{writer: writer}
	err = engine.UploadConfig()
	require.Error(t, err)
	require.Contains(t, err.Error(), "io: read/write on closed pipe")
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
	client := fake.NewSimpleClientset()
	engine := newKubeEngineTest(client, "", 0)

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
		},
	})

	fw := watch.NewFakeWithChanSize(1, false)
	fw.Modify(&apiv1.Service{Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{NodePort: 31000}}}})
	client.PrependWatchReactor("services", testcore.DefaultWatchReactor(fw, nil))

	_, err := engine.WaitRouter(w)
	require.NoError(t, err)
}

func TestEngine_WaitRouterFailure(t *testing.T) {
	engine := newKubeEngineTest(fake.NewSimpleClientset(), "", 0)

	reason := "oops"

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: apiv1.ConditionFalse,
				},
				{
					Type:   appsv1.DeploymentProgressing,
					Status: apiv1.ConditionFalse,
					Reason: reason,
				},
			},
		},
	})

	_, err := engine.WaitRouter(w)
	require.Error(t, err)
	require.Equal(t, reason, err.Error())
}

func TestEngine_WaitRouterVPNFailure(t *testing.T) {
	engine, client := makeEngine(1)

	e := errors.New("create error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.createVPNService()
	require.Error(t, err)
	require.Equal(t, e, err)

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
		},
	})

	engine.config.Host = ":"
	_, err = engine.WaitRouter(w)
	require.Error(t, err)
}

func TestEngine_CreateVPNFailures(t *testing.T) {
	engine, client := makeEngine(0)
	fw := watch.NewFake()

	e := errors.New("watch error")
	client.PrependWatchReactor("services", testcore.DefaultWatchReactor(fw, e))

	_, err := engine.createVPNService()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

func TestEngine_InitVPN(t *testing.T) {
	list := &apiv1.PodList{
		Items: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "simnet-router",
					Labels: map[string]string{LabelID: RouterID},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node0"},
			},
		},
	}

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	engine := newKubeEngineTest(fake.NewSimpleClientset(list), "", 1)
	engine.outDir = dir

	vpn, err := engine.InitVPN(&apiv1.ServicePort{})
	require.NoError(t, err)
	require.NotNil(t, vpn)
}

func TestEngine_InitVPNFailures(t *testing.T) {
	engine, _ := makeEngine(0)

	list := &apiv1.PodList{
		Items: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "simnet-router",
					Labels: map[string]string{LabelID: RouterID},
				},
			},
		},
	}
	engine.client = fake.NewSimpleClientset(list)

	setMockBadTunnel()
	defer setMockTunnel()

	_, err := engine.InitVPN(&apiv1.ServicePort{})
	require.Error(t, err)
	require.Equal(t, err.Error(), "tunnel error")

	// Expect an error when reading the client certificate.
	e := errors.New("read error")
	engine.fs = &testFS{err: e}
	_, err = engine.InitVPN(&apiv1.ServicePort{})
	require.Error(t, err)
	require.Equal(t, e, err)

	// Expect an error when parsing the host.
	engine.fs = &testFS{}
	engine.config.Host = ":"
	_, err = engine.InitVPN(&apiv1.ServicePort{})
	require.Error(t, err)

	// Expect an error when fetching the router pod.
	client := fake.NewSimpleClientset()
	engine.client = client
	_, err = engine.InitVPN(&apiv1.ServicePort{})
	require.Error(t, err)
	require.Equal(t, "missing router pod", err.Error())

	// Expect an error when fetching the list of pods.
	e = errors.New("fetch pod error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err = engine.InitVPN(&apiv1.ServicePort{})
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_Delete(t *testing.T) {
	srvice := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "simnet-router"},
	}

	client := fake.NewSimpleClientset(srvice)

	actions := make(chan testcore.Action, 1)
	// As the fake client does not implement the delete collection action, we
	// test differently by listening for the action.
	client.AddReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		actions <- action
		return true, nil, nil
	})

	engine := newKubeEngineTest(client, "", 0)

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

	client.PrependReactor("*", "services", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})

	e := errors.New("delete error")
	client.PrependReactor("*", "deployments", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.DeleteAll()
	require.Error(t, err)
	require.Equal(t, e, err)

	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("deployments", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.DeleteAll()
	require.Error(t, err)
	require.Equal(t, e, err)

	engine.client = fake.NewSimpleClientset()
	_, err = engine.DeleteAll()
	require.Error(t, err)
	require.IsType(t, (*apierrors.StatusError)(nil), err)

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

func TestEngine_ReadStats(t *testing.T) {
	engine := &kubeEngine{
		fs: &testFS{},
	}

	stats, err := engine.ReadStats("pod-name", time.Now(), time.Now())
	require.NoError(t, err)
	require.NotNil(t, stats)

	e := errors.New("oops")
	engine.fs = &testFS{err: e}
	_, err = engine.ReadStats("pod-name", time.Now(), time.Now())
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_ReadFile(t *testing.T) {
	engine := &kubeEngine{
		fs: &testFS{},
	}

	reader, err := engine.ReadFile("pod-name", "file-path")
	require.NoError(t, err)
	require.NotNil(t, reader)
}

func newKubeEngineTest(client kubernetes.Interface, ns string, n int) *kubeEngine {
	r, w := io.Pipe()

	return &kubeEngine{
		config: &rest.Config{
			Host: "https://127.0.0.1:333",
		},
		writer:    bytes.NewBuffer(nil),
		client:    client,
		namespace: ns,
		topology:  network.NewSimpleTopology(n, 50*time.Millisecond),
		fs:        &testFS{reader: r, writer: w},
	}
}

func makeEngine(n int) (*kubeEngine, *fake.Clientset) {
	client := fake.NewSimpleClientset()
	engine := newKubeEngineTest(client, "", n)

	return engine, client
}

type testFS struct {
	err    error
	reader io.ReadCloser
	writer io.WriteCloser
}

func (fs *testFS) Read(pod, container, path string) (io.ReadCloser, error) {
	r, w := io.Pipe()
	w.Close()
	return r, fs.err
}

func (fs *testFS) Write(pod, container string, cmd []string) (io.WriteCloser, <-chan error, error) {
	return fs.writer, nil, fs.err
}

func setMockTunnel() {
	newTunnel = func(opts ...sim.TunOption) (*sim.DefaultTunnel, error) {
		return &sim.DefaultTunnel{}, nil
	}
}

func setMockBadTunnel() {
	newTunnel = func(opts ...sim.TunOption) (*sim.DefaultTunnel, error) {
		return nil, errors.New("tunnel error")
	}
}

func setMockClient() {
	newClient = kubernetes.NewForConfig
}

func setMockBadClient() {
	newClient = func(*rest.Config) (*kubernetes.Clientset, error) {
		return nil, errors.New("client error")
	}
}
