package scope

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/xagent003/nsclass/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NamespaceScopeParams struct {
	logr.Logger
	Client    client.Client
	Namespace *corev1.Namespace
	Class     *v1alpha1.NamespaceClass
}

type NamespaceScope struct {
	NamespaceScopeParams
	patchHelper *patch.Helper
}

func NewNamespaceScope(params *NamespaceScopeParams) (*NamespaceScope, error) {
	patchHelper, err := patch.NewHelper(params.Namespace, params.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to init patch helper: %w", err)
	}
	return &NamespaceScope{
		NamespaceScopeParams: *params,
		patchHelper:          patchHelper,
	}, nil
}

func (s *NamespaceScope) Subject() client.Object {
	return s.Namespace
}

func (s *NamespaceScope) IsDeleting() bool {
	return !s.Subject().GetDeletionTimestamp().IsZero()
}

func (s *NamespaceScope) Close(ctx context.Context) error {
	s.Logger.Info("Closing Namespace scope", "Status", s.Namespace.Status)
	return s.patchHelper.Patch(ctx, s.Namespace)
}
