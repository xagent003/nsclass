package namespaceclass

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/xagent003/nsclass/api/v1alpha1"
)

func setupScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return scheme, nil
}

func createNamespaceClass(name string, resources []v1alpha1.NamespaceResource) *v1alpha1.NamespaceClass {
	return &v1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.NamespaceClassSpec{
			InheritedResources: resources,
		},
	}
}

func createNamespace(name, className string) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if className != "" {
		ns.Labels = map[string]string{
			v1alpha1.NamespaceClassLabel: className,
		}
	}

	return ns
}

func createConfigMapResource(name string, data map[string]string) v1alpha1.NamespaceResource {
	dataBytes, _ := json.Marshal(map[string]interface{}{
		"data": data,
	})

	return v1alpha1.NamespaceResource{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		ObjectMeta: v1alpha1.ResourceMetadata{
			Name: name,
		},
		SpecTemplate: &runtime.RawExtension{
			Raw: dataBytes,
		},
	}
}

func TestReconcile(t *testing.T) {
	ctx := context.Background()
	scheme, err := setupScheme()
	require.NoError(t, err)

	t.Run("Adding finalizer to NamespaceClass", func(t *testing.T) {
		nsClass := createNamespaceClass("bodom", []v1alpha1.NamespaceResource{
			createConfigMapResource("bodom-config", map[string]string{"key": "value"}),
		})

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(nsClass).
			WithStatusSubresource(nsClass).
			Build()

		reconciler := &NamespaceClassReconciler{
			Client: fakeClient,
			Scheme: scheme,
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: nsClass.Name,
			},
		}

		result, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.True(t, result.Requeue)

		updatedClass := &v1alpha1.NamespaceClass{}
		err = fakeClient.Get(ctx, req.NamespacedName, updatedClass)
		require.NoError(t, err)
		assert.Contains(t, updatedClass.Finalizers, v1alpha1.NamespaceClassFinalizer)
	})
}

func TestNamespaceToNamespaceClass(t *testing.T) {
	ctx := context.Background()
	scheme, err := setupScheme()
	require.NoError(t, err)

	reconciler := &NamespaceClassReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}

	t.Run("Namespace with class label - enqueues new class", func(t *testing.T) {
		ns := createNamespace("mastodon", "class-a")
		requests := reconciler.namespaceToNamespaceClass(ctx, ns)
		assert.Len(t, requests, 1, "Should generate one request for the class")
		assert.Equal(t, "class-a", requests[0].Name, "Request should be for class-a")
	})

	t.Run("Namespace with applied annotation - enqueus old class", func(t *testing.T) {
		ns := createNamespace("gojira", "")
		ns.Annotations = map[string]string{
			v1alpha1.NamespaceAppliedClassAnnotation: "class-b",
		}
		requests := reconciler.namespaceToNamespaceClass(ctx, ns)
		assert.Len(t, requests, 1, "Should generate one request for the old class")
		assert.Equal(t, "class-b", requests[0].Name, "Request should be for class-b")
	})

	t.Run("Namespace with class label and different applied annotation enqueues both", func(t *testing.T) {
		ns := createNamespace("converge", "class-a")
		ns.Annotations = map[string]string{
			v1alpha1.NamespaceAppliedClassAnnotation: "class-b",
		}
		requests := reconciler.namespaceToNamespaceClass(ctx, ns)
		assert.Len(t, requests, 2, "Should generate two requests, for both classes")

		classes := make(map[string]bool)
		for _, req := range requests {
			classes[req.Name] = true
		}
		assert.True(t, classes["class-a"], "Should generate request for class-a")
		assert.True(t, classes["class-b"], "Should generate request for class-b")
	})

	t.Run("Namespace with no class enqueus nothing", func(t *testing.T) {
		ns := createNamespace("test-ns-4", "")
		requests := reconciler.namespaceToNamespaceClass(ctx, ns)
		assert.Len(t, requests, 0, "Should generate no requests")
	})
}

func TestGetResourceKey(t *testing.T) {
	testCases := []struct {
		apiVersion string
		kind       string
		name       string
		expected   string
	}{
		{
			apiVersion: "v1",
			kind:       "ConfigMap",
			name:       "cryptopsy",
			expected:   "v1/ConfigMap/cryptopsy",
		},
		{
			apiVersion: "apps/v1",
			kind:       "Deployment",
			name:       "gorod",
			expected:   "apps/v1/Deployment/gorod",
		},
		{
			apiVersion: "networking.k8s.io/v1",
			kind:       "Ingress",
			name:       "emperor",
			expected:   "networking.k8s.io/v1/Ingress/emperor",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := getResourceKey(tc.apiVersion, tc.kind, tc.name)
			assert.Equal(t, tc.expected, result)
		})
	}
}
