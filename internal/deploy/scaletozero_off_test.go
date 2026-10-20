package deploy

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/kube"
)

// sleeper is the scale-to-zero add-on's two objects for an app, as it leaves
// them: its own, and the KEDA one it made from it, which does the scaling.
func sleeper(namespace, name string) []runtime.Object {
	add := func(apiVersion, kind string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(apiVersion)
		u.SetKind(kind)
		u.SetNamespace(namespace)
		u.SetName(name)
		return u
	}
	return []runtime.Object{
		add("http.keda.sh/v1alpha1", "HTTPScaledObject"),
		add("keda.sh/v1alpha1", "ScaledObject"),
	}
}

func sleeperDynamic(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "http.keda.sh", Version: "v1alpha1", Resource: "httpscaledobjects"}: "HTTPScaledObjectList",
		{Group: "keda.sh", Version: "v1alpha1", Resource: "scaledobjects"}:          "ScaledObjectList",
		{Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"}:         "MiddlewareList",
	}, objects...)
}

func sized(namespace, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
}

// An app that stops scaling to zero is let go of by the add-on, and then has
// the instances it is configured for, whatever the add-on left it at.
//
// The apply that gave the app its size back came first, and the add-on went
// on scaling the app until its objects were gone: an app with no visitors in
// that moment was scaled to none again, and nothing scaled it up after. The
// two-server crosscheck found its app at no instances for the rest of the run.
func TestAnAppThatStopsSleepingHasItsInstancesBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		spec      kube.AppSpec
		left, now int32
	}{
		{"a fixed size", kube.AppSpec{Replicas: 2}, 0, 2},
		{"a fixed size the add-on left alone", kube.AppSpec{Replicas: 1}, 1, 1},
		// An autoscaler leaves a Deployment of none alone, which to it means
		// switched off, so it is given its least to start from.
		{"an autoscaler's", kube.AppSpec{Autoscale: true, MinReplicas: 2, MaxReplicas: 5}, 0, 2},
		{"an autoscaler's that is running", kube.AppSpec{Autoscale: true, MinReplicas: 2, MaxReplicas: 5}, 4, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, db, app, env := testDeployer(t)
			spec := tc.spec
			spec.Name, spec.Namespace = app.Slug, env.Namespace
			clientset := fake.NewSimpleClientset(sized(spec.Namespace, spec.Name, tc.left))
			dynamic := sleeperDynamic(sleeper(spec.Namespace, spec.Name)...)
			withCluster(t, d, db, clientset, dynamic)

			if !d.letGoOfScaleToZero(t.Context(), spec, app) {
				t.Fatal("an app the add-on was scaling was not let go of")
			}
			for _, kind := range []schema.GroupVersionResource{
				{Group: "http.keda.sh", Version: "v1alpha1", Resource: "httpscaledobjects"},
				{Group: "keda.sh", Version: "v1alpha1", Resource: "scaledobjects"},
			} {
				if _, err := dynamic.Resource(kind).Namespace(spec.Namespace).Get(t.Context(), spec.Name, metav1.GetOptions{}); err == nil {
					t.Errorf("the add-on's %s is still there", kind.Resource)
				}
			}
			d.giveBackSize(t.Context(), spec, app)
			live, err := clientset.AppsV1().Deployments(spec.Namespace).Get(t.Context(), spec.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if *live.Spec.Replicas != tc.now {
				t.Errorf("the app has %d instances, want %d", *live.Spec.Replicas, tc.now)
			}
		})
	}
}

// An app the add-on never scaled is not its to give back: one stopped at no
// instances on purpose stays stopped.
func TestAnAppThatNeverSleptIsLeftAsItIs(t *testing.T) {
	d, db, app, env := testDeployer(t)
	spec := kube.AppSpec{Name: app.Slug, Namespace: env.Namespace, Replicas: 0}
	clientset := fake.NewSimpleClientset(sized(spec.Namespace, spec.Name, 0))
	withCluster(t, d, db, clientset, sleeperDynamic())

	if d.letGoOfScaleToZero(t.Context(), spec, app) {
		t.Error("an app with no scale-to-zero objects was taken for one the add-on was scaling")
	}
}
