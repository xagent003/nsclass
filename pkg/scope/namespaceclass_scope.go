package scope

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/xagent003/nsclass/api/v1alpha1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NamespaceClassScopeParams struct {
	logr.Logger
	Client         client.Client
	NamespaceClass *v1alpha1.NamespaceClass
}

type NamespaceClassScope struct {
	NamespaceClassScopeParams
	patchHelper *patch.Helper
}

func NewNamespaceClassScope(params *NamespaceClassScopeParams) (*NamespaceClassScope, error) {
	patchHelper, err := patch.NewHelper(params.NamespaceClass, params.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to init patch helper: %w", err)
	}
	return &NamespaceClassScope{
		NamespaceClassScopeParams: *params,
		patchHelper:               patchHelper,
	}, nil
}

func (s *NamespaceClassScope) Subject() client.Object {
	return s.NamespaceClass
}

func (s *NamespaceClassScope) IsDeleting() bool {
	return !s.Subject().GetDeletionTimestamp().IsZero()
}

func (s *NamespaceClassScope) Close(ctx context.Context) error {
	s.Logger.Info("Closing NamespaceClass scope", "Status", s.NamespaceClass.Status)
	return s.patchHelper.Patch(ctx, s.NamespaceClass)
}
