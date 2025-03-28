// Stripped down version of https://github.com/kubernetes-sigs/cluster-api/blob/main/internal/test/envtest/environment.go.

package envtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/pkg/errors"
	"github.com/xagent003/nsclass/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func init() {
	utilruntime.Must(scheme.AddToScheme(scheme.Scheme))
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
}

// RunInput is the input for Run.
type RunInput struct {
	M                   *testing.M
	ManagerUncachedObjs []client.Object
	SetupReconcilers    func(ctx context.Context, mgr ctrl.Manager)
	SetupEnv            func(e *Environment)
}

// Run executes the tests of the given testing.M in a test environment.
func Run(ctx context.Context, input RunInput) int {
	// Bootstrapping a new test environment
	env := newEnvironment()

	if input.SetupReconcilers != nil {
		input.SetupReconcilers(ctx, env.Manager)
	}

	// Start the environment.
	env.start(ctx)

	// Expose the environment.
	input.SetupEnv(env)

	go func() {
		defer ginkgo.GinkgoRecover()
	}()

	// Run tests and collect the exit code.
	exitCode := input.M.Run()

	// Tearing down the test environment
	if err := env.stop(); err != nil {
		panic(fmt.Sprintf("Failed to stop the test environment: %v", err))
	}
	return exitCode
}

var (
	cacheSyncBackoff = wait.Backoff{
		Duration: 100 * time.Millisecond,
		Factor:   1.5,
		Steps:    8,
		Jitter:   0.4,
	}
)

// Environment encapsulates a Kubernetes local test environment that is backed by
// envtest (https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest).
type Environment struct {
	manager.Manager
	client.Client
	Config        *rest.Config
	env           *envtest.Environment
	cancelManager context.CancelFunc
}

func envOr(envKey, defaultValue string) string {
	if value, ok := os.LookupEnv(envKey); ok {
		return value
	}
	return defaultValue
}

func findRoot() string {
	currentDir := "."
	for {
		// Check if the "bin" directory exists in the current directory
		binPath := filepath.Join(currentDir, "bin")
		if _, err := os.Stat(binPath); err == nil {
			// bin directory found
			return currentDir
		}

		// Move one level up in the directory structure
		parentDir := filepath.Join(currentDir, "..")
		if parentDir == currentDir {
			// If the parent directory is the same as the current directory, we've reached the root
			break
		}
		currentDir = parentDir
	}
	return ""
}

func newEnvironment() *Environment {
	// Get the root of the current file to use in CRD paths.
	root := findRoot()

	crdPaths := []string{
		filepath.Join(root, "config", "crd", "bases"),
	}

	env := &envtest.Environment{
		CRDDirectoryPaths:     crdPaths,
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join(findRoot(), "bin", "k8s", fmt.Sprintf("1.32.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	if _, err := env.Start(); err != nil {
		panic(err)
	}

	mgr, err := ctrl.NewManager(env.Config, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		panic(err)
	}

	return &Environment{
		Manager: mgr,
		Client:  mgr.GetClient(),
		Config:  mgr.GetConfig(),
		env:     env,
	}
}

// start starts the environment.
func (e *Environment) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	e.cancelManager = cancel

	go func() {
		fmt.Println("Starting the test environment manager")
		if err := e.Manager.Start(ctx); err != nil {
			panic(fmt.Sprintf("Failed to start the test environment manager: %v", err))
		}
	}()

	<-e.Manager.Elected()
}

// stop stops the environment.
func (e *Environment) stop() error {
	fmt.Println("Stopping the test environment")
	e.cancelManager()
	return e.env.Stop()
}

// Cleanup deletes all the given objects.
func (e *Environment) Cleanup(ctx context.Context, objs ...client.Object) error {
	errs := []error{}
	for _, o := range objs {
		err := e.Client.Delete(ctx, o)
		if apierrors.IsNotFound(err) {
			continue
		}
		errs = append(errs, err)
	}
	return kerrors.NewAggregate(errs)
}

// CreateNamespace creates a new namespace with a generated name.
func (e *Environment) CreateNamespace(ctx context.Context, generateName string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", generateName),
			Labels: map[string]string{
				"testenv/original-name": generateName,
			},
		},
	}
	if err := e.Client.Create(ctx, ns); err != nil {
		return nil, err
	}

	return ns, nil
}

// CreateNamespaceExplicitName creates a new namespace with the given name.
func (e *Environment) CreateNamespaceExplicitName(ctx context.Context, name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := e.Client.Create(ctx, ns); err != nil {
		return nil, err
	}

	return ns, nil
}

// CreateNamespaceExplicitNameIgnoreAlreadyExists creates a new namespace with the given name.
func (e *Environment) CreateNamespaceExplicitNameIgnoreAlreadyExists(ctx context.Context, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := e.Client.Create(ctx, ns); err != nil {
		return client.IgnoreAlreadyExists(err)
	}

	return nil
}

// CreateAndWait creates the given object and waits for the cache to be updated accordingly.
//
// NOTE: Waiting for the cache to be updated helps in preventing test flakes due to the cache sync delays.
func (e *Environment) CreateAndWait(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := e.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}

	// Makes sure the cache is updated with the new object
	objCopy := obj.DeepCopyObject().(client.Object)
	key := client.ObjectKeyFromObject(obj)
	if err := wait.ExponentialBackoff(
		cacheSyncBackoff,
		func() (done bool, err error) {
			if err := e.Client.Get(ctx, key, objCopy); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}); err != nil {
		return errors.Wrapf(err, "object %s, %s is not being added to the testenv client cache", obj.GetObjectKind().GroupVersionKind().String(), key)
	}
	return nil
}

// CreateKubeconfigSecret generates a new Kubeconfig secret from the envtest config.
func (e *Environment) CreateKubeconfigSecret(ctx context.Context, cluster *clusterv1.Cluster) error {
	return e.Create(ctx, kubeconfig.GenerateSecret(cluster, kubeconfig.FromEnvTestConfig(e.Config, cluster)))
}

// GetGenericObject returns any object that implements client.Object.
// Used a generic and reflect so we dont have to write one of these for each CR.
func GetGenericObject[T client.Object](ctx context.Context, e *Environment, key types.NamespacedName, opts ...client.GetOption) (T, error) {
	var obj T
	obj = reflect.New(reflect.TypeOf(obj).Elem()).Interface().(T)

	if err := wait.ExponentialBackoff(
		cacheSyncBackoff,
		func() (done bool, err error) {
			if err := e.Client.Get(ctx, key, obj, opts...); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}); err != nil {
		return obj, errors.Wrapf(err, "object %s is not being added to the testenv client cache", key)
	}
	return obj, nil
}

// UpdateNSClassStatusAndWait updates the given object and waits for the cache to be updated accordingly.
//
// NOTE: Waiting for the cache to be updated helps in preventing test flakes due to the cache sync delays.
func (e *Environment) UpdateNSClassStatusAndWait(ctx context.Context, obj *v1alpha1.NamespaceClass, opts ...client.UpdateOption) error {
	if err := e.Client.Status().Update(ctx, obj); err != nil {
		return err
	}

	// Makes sure the cache is updated with the new object
	objCopy := new(v1alpha1.NamespaceClass)
	key := client.ObjectKeyFromObject(obj)
	if err := wait.ExponentialBackoff(
		cacheSyncBackoff,
		func() (done bool, err error) {
			if err := e.Client.Get(ctx, key, objCopy); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}

			if !equality.Semantic.DeepEqual(obj.Status, objCopy.Status) {
				return false, nil
			}

			return true, nil
		}); err != nil {
		return errors.Wrapf(err, "object %s, %s is not being added to the testenv client cache", obj.GetObjectKind().GroupVersionKind().String(), key)
	}
	return nil
}

// UpdateGenericStatusAndWait updates a Status on a client.Object.
func UpdateGenericStatusAndWait(ctx context.Context, e *Environment, obj client.Object, opts ...client.UpdateOption) error {
	if err := e.Client.Status().Update(ctx, obj); err != nil {
		return err
	}
	return nil
}
