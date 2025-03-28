package namespace

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"slices"

	"github.com/xagent003/nsclass/api/v1alpha1"
)

func createRawExtension(t testing.TB, obj interface{}) *runtime.RawExtension {
	data, err := json.Marshal(obj)
	if t != nil {
		require.NoError(t, err)
	} else if err != nil {
		panic(fmt.Sprintf("Failed to marshal template: %v", err))
	}
	return &runtime.RawExtension{Raw: data}
}

func createTestNamespace(name string, classLabel string) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if classLabel != "" {
		ns.ObjectMeta.Labels = map[string]string{
			v1alpha1.NamespaceClassLabel: classLabel,
		}
	}

	return ns
}

// Foo class resources
func getFooConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo-config",
		},
		Data: map[string]string{
			"key1":        "valueNEWNEWNEW",
			"key2":        "valueNEWNEWNEW",
			"environment": "production",
		},
	}
}

func getFooRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo-viewer-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods", "services"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

func getFooPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo-busybox",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "busybox",
					Image:   "busybox:latest",
					Command: []string{"sleep", "3600"},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("64Mi"),
							corev1.ResourceCPU:    resource.MustParse("100m"),
						},
					},
				},
			},
		},
	}
}

// Bar class resources
func getBarConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar-config",
		},
		Data: map[string]string{
			"app":         "bar-application",
			"environment": "development",
			"log-level":   "debug",
		},
	}
}

func getBarRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar-admin-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "statefulsets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		},
	}
}

func getBarServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar-service-account",
			Annotations: map[string]string{
				"description": "Service account for the bar system",
				"environment": "development",
			},
		},
	}
}

// Test class resources
func getTestConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Data: map[string]string{
			"key": "value",
		},
	}
}

func getTestServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-service-account",
			Annotations: map[string]string{
				"description": "Test service account",
			},
		},
	}
}

func configMapToNamespaceResource(cm *corev1.ConfigMap) v1alpha1.NamespaceResource {
	return v1alpha1.NamespaceResource{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		ObjectMeta: v1alpha1.ResourceMetadata{
			Name: cm.Name,
		},
		SpecTemplate: createRawExtension(nil, map[string]interface{}{
			"data": cm.Data,
		}),
	}
}

func podToNamespaceResource(pod *corev1.Pod) v1alpha1.NamespaceResource {
	return v1alpha1.NamespaceResource{
		APIVersion: "v1",
		Kind:       "Pod",
		ObjectMeta: v1alpha1.ResourceMetadata{
			Name: pod.Name,
		},
		SpecTemplate: createRawExtension(nil, map[string]interface{}{
			"spec": pod.Spec,
		}),
	}
}

func roleToNamespaceResource(role *rbacv1.Role) v1alpha1.NamespaceResource {
	return v1alpha1.NamespaceResource{
		APIVersion: "rbac.authorization.k8s.io/v1",
		Kind:       "Role",
		ObjectMeta: v1alpha1.ResourceMetadata{
			Name: role.Name,
		},
		SpecTemplate: createRawExtension(nil, map[string]interface{}{
			"rules": role.Rules,
		}),
	}
}

func serviceAccountToNamespaceResource(sa *corev1.ServiceAccount) v1alpha1.NamespaceResource {
	return v1alpha1.NamespaceResource{
		APIVersion: "v1",
		Kind:       "ServiceAccount",
		ObjectMeta: v1alpha1.ResourceMetadata{
			Name:        sa.Name,
			Annotations: sa.Annotations,
		},
		SpecTemplate: createRawExtension(nil, map[string]interface{}{}),
	}
}

func createFooNamespaceClass() *v1alpha1.NamespaceClass {
	return &v1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Spec: v1alpha1.NamespaceClassSpec{
			InheritedResources: []v1alpha1.NamespaceResource{
				configMapToNamespaceResource(getFooConfigMap()),
				roleToNamespaceResource(getFooRole()),
				podToNamespaceResource(getFooPod()),
			},
		},
	}
}

func createBarNamespaceClass() *v1alpha1.NamespaceClass {
	return &v1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar",
		},
		Spec: v1alpha1.NamespaceClassSpec{
			InheritedResources: []v1alpha1.NamespaceResource{
				configMapToNamespaceResource(getBarConfigMap()),
				roleToNamespaceResource(getBarRole()),
				serviceAccountToNamespaceResource(getBarServiceAccount()),
			},
		},
	}
}

func verifyConfigMapExists(t *testing.T, namespaceName string, expected *corev1.ConfigMap) {
	t.Helper()
	assert.Eventually(t, func() bool {
		configMap := &corev1.ConfigMap{}
		err := env.Client.Get(ctx, types.NamespacedName{
			Namespace: namespaceName,
			Name:      expected.Name,
		}, configMap)
		if err != nil {
			return false
		}

		for key, expectedValue := range expected.Data {
			if configMap.Data[key] != expectedValue {
				return false
			}
		}
		return true
	}, timeout, pollingInterval, "ConfigMap %s not found or with incorrect values", expected.Name)
}

func verifyServiceAccountExists(t *testing.T, namespaceName string, expected *corev1.ServiceAccount) {
	t.Helper()
	assert.Eventually(t, func() bool {
		sa := &corev1.ServiceAccount{}
		err := env.Client.Get(ctx, types.NamespacedName{
			Namespace: namespaceName,
			Name:      expected.Name,
		}, sa)
		if err != nil {
			return false
		}

		for key, expectedValue := range expected.Annotations {
			if sa.Annotations[key] != expectedValue {
				return false
			}
		}
		return true
	}, timeout, pollingInterval, "ServiceAccount %s not found or with incorrect annotations", expected.Name)
}

func verifyRoleExists(t *testing.T, namespaceName string, expected *rbacv1.Role) {
	t.Helper()
	assert.Eventually(t, func() bool {
		role := &rbacv1.Role{}
		err := env.Client.Get(ctx, types.NamespacedName{
			Namespace: namespaceName,
			Name:      expected.Name,
		}, role)
		if err != nil {
			return false
		}

		if len(role.Rules) == 0 {
			return false
		}

		// Simple validation - just check that each API group in the expected role
		// is also in the actual role
		expectedGroups := make(map[string]bool)
		for _, rule := range expected.Rules {
			for _, group := range rule.APIGroups {
				expectedGroups[group] = true
			}
		}

		for _, rule := range role.Rules {
			for _, group := range rule.APIGroups {
				delete(expectedGroups, group)
			}
		}

		return len(expectedGroups) == 0 // All expected groups were found
	}, timeout, pollingInterval, "Role %s not found or with incorrect API groups", expected.Name)
}

func verifyPodExists(t *testing.T, namespaceName string, expected *corev1.Pod) {
	t.Helper()
	assert.Eventually(t, func() bool {
		pod := &corev1.Pod{}
		err := env.Client.Get(ctx, types.NamespacedName{
			Namespace: namespaceName,
			Name:      expected.Name,
		}, pod)
		if err != nil {
			return false
		}

		if len(pod.Spec.Containers) == 0 {
			return false
		}

		// Validate the container image
		return pod.Spec.Containers[0].Image == expected.Spec.Containers[0].Image
	}, timeout, pollingInterval, "Pod %s not found or with incorrect image", expected.Name)
}

func verifyResourceNotExists(t *testing.T, obj client.Object) {
	t.Helper()
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		return errors.IsNotFound(err)
	}, timeout, pollingInterval, "Resource %T %s was not deleted", obj, obj.GetName())
}

func TestNamespaceCreatedAfterNamespaceClass(t *testing.T) {
	fooClass := createFooNamespaceClass()
	require.NoError(t, env.CreateAndWait(ctx, fooClass))
	defer env.Cleanup(ctx, fooClass)

	namespace := createTestNamespace("nero", "foo")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	verifyConfigMapExists(t, namespace.Name, getFooConfigMap())
	verifyRoleExists(t, namespace.Name, getFooRole())
	verifyPodExists(t, namespace.Name, getFooPod())

	updatedNS := &corev1.Namespace{}
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: namespace.Name}, updatedNS)
		if err != nil {
			return false
		}
		hasFinalizer := slices.Contains(updatedNS.Finalizers, v1alpha1.NamespaceWithClassFinalizer)
		return hasFinalizer && updatedNS.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] == "foo"
	}, timeout, pollingInterval, "Namespace should have finalizer and annotation")

	updatedClass := &v1alpha1.NamespaceClass{}
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: fooClass.Name}, updatedClass)
		if err != nil {
			return false
		}

		nsResources, exists := updatedClass.Status.NamespaceResources[namespace.Name]
		return exists && len(nsResources) == 3 // ConfigMap, Role, Pod
	}, timeout, pollingInterval, "NamespaceClass status should track resources")
}

func TestNamespaceClassCreatedAfterNamespace(t *testing.T) {
	namespace := createTestNamespace("caligula", "bar")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	barClass := createBarNamespaceClass()
	require.NoError(t, env.CreateAndWait(ctx, barClass))
	defer env.Cleanup(ctx, barClass)

	verifyConfigMapExists(t, namespace.Name, getBarConfigMap())
	verifyRoleExists(t, namespace.Name, getBarRole())
	verifyServiceAccountExists(t, namespace.Name, getBarServiceAccount())

	updatedNS := &corev1.Namespace{}
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: namespace.Name}, updatedNS)
		if err != nil {
			return false
		}
		hasFinalizer := slices.Contains(updatedNS.Finalizers, v1alpha1.NamespaceWithClassFinalizer)
		return hasFinalizer && updatedNS.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] == "bar"
	}, timeout, pollingInterval, "Namespace should have finalizer and annotation")
}

func TestNamespaceSwitchClass(t *testing.T) {
	fooClass := createFooNamespaceClass()
	barClass := createBarNamespaceClass()

	require.NoError(t, env.CreateAndWait(ctx, fooClass))
	require.NoError(t, env.CreateAndWait(ctx, barClass))
	defer env.Cleanup(ctx, fooClass, barClass)

	namespace := createTestNamespace("caracalla", "foo")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	verifyConfigMapExists(t, namespace.Name, getFooConfigMap())
	verifyRoleExists(t, namespace.Name, getFooRole())
	verifyPodExists(t, namespace.Name, getFooPod())

	updatedNS := &corev1.Namespace{}
	require.NoError(t, env.Client.Get(ctx, types.NamespacedName{Name: namespace.Name}, updatedNS))

	patchedNS := updatedNS.DeepCopy()
	if patchedNS.Labels == nil {
		patchedNS.Labels = make(map[string]string)
	}
	patchedNS.Labels[v1alpha1.NamespaceClassLabel] = "bar"

	require.NoError(t, env.Client.Patch(ctx, patchedNS, client.MergeFrom(updatedNS)))

	// first verify foo resources are removed
	fooConfigMap := getFooConfigMap()
	fooConfigMap.Namespace = namespace.Name

	fooRole := getFooRole()
	fooRole.Namespace = namespace.Name

	fooPod := getFooPod()
	fooPod.Namespace = namespace.Name

	verifyResourceNotExists(t, fooConfigMap)
	verifyResourceNotExists(t, fooRole)
	verifyResourceNotExists(t, fooPod)

	// next verify bar resources are created
	verifyConfigMapExists(t, namespace.Name, getBarConfigMap())
	verifyRoleExists(t, namespace.Name, getBarRole())
	verifyServiceAccountExists(t, namespace.Name, getBarServiceAccount())

	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: namespace.Name}, updatedNS)
		if err != nil {
			return false
		}
		return updatedNS.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] == "bar"
	}, timeout, pollingInterval, "Namespace should have updated annotation")

	updatedFooClass := &v1alpha1.NamespaceClass{}
	updatedBarClass := &v1alpha1.NamespaceClass{}

	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: "foo"}, updatedFooClass)
		if err != nil {
			return false
		}

		_, hasNamespaceFoo := updatedFooClass.Status.NamespaceResources[namespace.Name]
		return !hasNamespaceFoo
	}, timeout, pollingInterval, "Namespace should be removed from foo class status")

	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: "bar"}, updatedBarClass)
		if err != nil {
			return false
		}

		refs, hasNamespaceBar := updatedBarClass.Status.NamespaceResources[namespace.Name]
		return hasNamespaceBar && len(refs) == 3
	}, timeout, pollingInterval, "Namespace should be added to bar class status")
}

func TestNamespaceRemovesClass(t *testing.T) {
	barClass := createBarNamespaceClass()
	require.NoError(t, env.CreateAndWait(ctx, barClass))
	defer env.Cleanup(ctx, barClass)

	namespace := createTestNamespace("ivan", "bar")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	verifyConfigMapExists(t, namespace.Name, getBarConfigMap())
	verifyServiceAccountExists(t, namespace.Name, getBarServiceAccount())

	updatedNS := &corev1.Namespace{}
	require.NoError(t, env.Client.Get(ctx, types.NamespacedName{Name: namespace.Name}, updatedNS))

	patchedNS := updatedNS.DeepCopy()
	patchedNS.Labels = map[string]string{}

	require.NoError(t, env.Client.Patch(ctx, patchedNS, client.MergeFrom(updatedNS)))

	barConfig := getBarConfigMap()
	barConfig.Namespace = namespace.Name

	barRole := getBarRole()
	barRole.Namespace = namespace.Name

	barSA := getBarServiceAccount()
	barSA.Namespace = namespace.Name

	verifyResourceNotExists(t, barConfig)
	verifyResourceNotExists(t, barRole)
	verifyResourceNotExists(t, barSA)

	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: namespace.Name}, updatedNS)
		if err != nil {
			return false
		}
		_, hasAnnotation := updatedNS.Annotations[v1alpha1.NamespaceAppliedClassAnnotation]
		return !hasAnnotation
	}, timeout, pollingInterval, "Namespace should have annotation removed")

	updatedClass := &v1alpha1.NamespaceClass{}
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: barClass.Name}, updatedClass)
		if err != nil {
			return false
		}

		_, hasNamespace := updatedClass.Status.NamespaceResources[namespace.Name]
		return !hasNamespace
	}, timeout, pollingInterval, "Namespace should be removed from class status")
}

func TestNamespaceClassAddsResource(t *testing.T) {
	configMap := getTestConfigMap()

	nsClass := &v1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-class",
		},
		Spec: v1alpha1.NamespaceClassSpec{
			InheritedResources: []v1alpha1.NamespaceResource{
				configMapToNamespaceResource(configMap),
			},
		},
	}

	require.NoError(t, env.CreateAndWait(ctx, nsClass))
	defer env.Cleanup(ctx, nsClass)

	namespace := createTestNamespace("genghis", "test-class")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	verifyConfigMapExists(t, namespace.Name, configMap)

	updatedClass := &v1alpha1.NamespaceClass{}
	require.NoError(t, env.Client.Get(ctx, types.NamespacedName{Name: nsClass.Name}, updatedClass))

	serviceAccount := getTestServiceAccount()

	updatedClass.Spec.InheritedResources = append(updatedClass.Spec.InheritedResources,
		serviceAccountToNamespaceResource(serviceAccount),
	)

	require.NoError(t, env.Client.Update(ctx, updatedClass))

	verifyConfigMapExists(t, namespace.Name, configMap)
	verifyServiceAccountExists(t, namespace.Name, serviceAccount)

	finalClass := &v1alpha1.NamespaceClass{}
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: nsClass.Name}, finalClass)
		if err != nil {
			return false
		}

		refs, hasNamespace := finalClass.Status.NamespaceResources[namespace.Name]
		return hasNamespace && len(refs) == 2
	}, timeout, pollingInterval, "Status should contain both resources")
}

func TestNamespaceClassRemovesResource(t *testing.T) {
	fooClass := createFooNamespaceClass()
	require.NoError(t, env.CreateAndWait(ctx, fooClass))
	defer env.Cleanup(ctx, fooClass)

	namespace := createTestNamespace("attila", "foo")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	verifyConfigMapExists(t, namespace.Name, getFooConfigMap())
	verifyRoleExists(t, namespace.Name, getFooRole())
	verifyPodExists(t, namespace.Name, getFooPod())

	updatedClass := &v1alpha1.NamespaceClass{}
	require.NoError(t, env.Client.Get(ctx, types.NamespacedName{Name: fooClass.Name}, updatedClass))

	// Keep only the first two resources (ConfigMap and Role)
	updatedClass.Spec.InheritedResources = updatedClass.Spec.InheritedResources[:2]
	require.NoError(t, env.Client.Update(ctx, updatedClass))

	verifyConfigMapExists(t, namespace.Name, getFooConfigMap())
	verifyRoleExists(t, namespace.Name, getFooRole())

	fooPod := getFooPod()
	fooPod.Namespace = namespace.Name
	verifyResourceNotExists(t, fooPod)

	finalClass := &v1alpha1.NamespaceClass{}
	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: fooClass.Name}, finalClass)
		if err != nil {
			return false
		}

		refs, hasNamespace := finalClass.Status.NamespaceResources[namespace.Name]
		if !hasNamespace || len(refs) != 2 {
			return false
		}

		for _, ref := range refs {
			if ref.Kind == "Pod" && ref.Name == "foo-busybox" {
				return false
			}
		}
		return true
	}, timeout, pollingInterval, "Pod should be removed from status references")
}

func TestNamespaceClassDeleted(t *testing.T) {
	barClass := createBarNamespaceClass()
	require.NoError(t, env.CreateAndWait(ctx, barClass))

	namespace := createTestNamespace("diocletian", "bar")
	require.NoError(t, env.CreateAndWait(ctx, namespace))
	defer env.Cleanup(ctx, namespace)

	verifyConfigMapExists(t, namespace.Name, getBarConfigMap())
	verifyServiceAccountExists(t, namespace.Name, getBarServiceAccount())

	require.NoError(t, env.Client.Delete(ctx, barClass))

	assert.Eventually(t, func() bool {
		err := env.Client.Get(ctx, types.NamespacedName{Name: barClass.Name}, &v1alpha1.NamespaceClass{})
		return errors.IsNotFound(err)
	}, timeout, pollingInterval, "NamespaceClass should be deleted")
}
