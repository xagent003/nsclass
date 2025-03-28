package namespace

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/xagent003/nsclass/internal/test/envtest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	timeout         = 30 * time.Second
	pollingInterval = 500 * time.Millisecond
)

var (
	env *envtest.Environment
	ctx = ctrl.SetupSignalHandler()
)

func TestMain(m *testing.M) {
	RegisterFailHandler(Fail)
	log.SetLogger(zap.New(zap.WriteTo(os.Stdout)))

	setupReconcilers := func(ctx context.Context, mgr ctrl.Manager) {
		var err error
		if err = (&NamespaceReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			panic(fmt.Errorf("failed to set up StorageConfiguratorReconciler reconciler: %w", err))
		}
	}

	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
	SetDefaultEventuallyTimeout(timeout)

	os.Exit(envtest.Run(ctx, envtest.RunInput{
		M:                m,
		SetupEnv:         func(e *envtest.Environment) { env = e },
		SetupReconcilers: setupReconcilers,
	}))
}
