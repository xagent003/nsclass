package v1alpha1

import clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

const (
	// NamespaceClassLabel is a label on the namespace indicating the NamespaceClass it inherits resources from.
	NamespaceClassLabel = "namespaceclass.akuity.io/name"

	// NamespaceAppliedClassAnnotation tracks which class was last successfully applied to a namespace
	NamespaceAppliedClassAnnotation = "namespaceclass.akuity.io/last-applied"
)

const (
	// NamespaceWithClassFinalizer is a finalizer on the namespace when using a NamespaceClass.
	NamespaceWithClassFinalizer = "akuity.io/namespace-class-managed"

	// NamespaceClassFinalizer is the finalizer for a NamespaceClass.
	NamespaceClassFinalizer = "akuity.io/namespaceclass"
)

const (
	ConditionNamespaceClassResourcesReady clusterv1.ConditionType = "ResourcesReady"

	ReasonNamespaceListFailed  string = "NamespaceListFailed"
	ReasonResourcesApplying    string = "ApplyingResources"
	ReasonResourcesApplied     string = "ResourcesApplied"
	ReasonResourcesApplyFailed string = "FailedToApplyResources"
)
