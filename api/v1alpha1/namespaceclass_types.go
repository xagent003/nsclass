/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// NamespaceClassSpec defines the desired state of NamespaceClass.
type NamespaceClassSpec struct {
	// K8sAPIAction defines the target K8s API resource to create.
	// +optional
	InheritedResources []NamespaceResource `json:"inheritedResources,omitempty"`

	// TODO: ALTERNATIVE IMPLEMENTAION
	// We can pre-create each resource in a reference namespace and just have ObjectRef's
	// to each, then we copy over into the desired namespace.
	// This may make sense if the objects we're dealing with are simple *Template* CRDs.
	// Similar to how CAPI uses template references. But then we'd need <XXX>Template CRDs
	// but since the guideline is *any* resource - which may include daemonsets, RBAC, PVCs,
	// or ever other CRs, we don't want to create these actual resources(they consume phys resources)
	// But leaving this here as an alternative
	TemplateNamespace string                    `json:"templateNamespace,omitempty"`
	ResourceRefs      []*corev1.ObjectReference `json:"resourceRefs,omitempty"`
}

type NamespaceResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Metadata contains the metadata for the resource (only name is required)
	// Namespace will be set by the controller to match the parent namespace
	ObjectMeta ResourceMetadata `json:"metadata"`

	// SpecTemplate contains the spec of the CR to create, of type GVK in this same struct.
	// +kubebuilder:pruning:PreserveUnknownFields
	SpecTemplate *runtime.RawExtension `json:"template,omitempty"`
}

// ResourceMetadata contains the metadata fields that should be applied to the resource
type ResourceMetadata struct {
	// Name of the resource
	Name string `json:"name"`

	// Labels to apply to the resource
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations to apply to the resource
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// NamespaceClassStatus defines the observed state of NamespaceClass.
type NamespaceClassStatus struct {
	// Ready indicates overall Ready status from conditions summary.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Reason indicates the reason why NamespaceClass is in ready or not state.
	Reason string `json:"reason,omitempty"`

	// Conditions defines the state of the NamespaceClass.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// ManagedNamespaces is a list of namespaces that had all resources successfully aplied
	// +optional
	ManagedNamespaces []string `json:"managedNamespaces,omitempty"`

	// ResourceRefs lists the resources defined by this NamespaceClass - only used by namespaceclass controller
	// Used to track what needs to be cleaned up when resources are removed
	// +optional
	ResourceRefs []corev1.ObjectReference `json:"resourceRefs,omitempty"`

	// NamespaceResources tracks resources applied to each namespace - only used by namespace controller
	// +optional
	NamespaceResources map[string][]corev1.ObjectReference `json:"namespaceResources,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=nsclass

// NamespaceClass is the Schema for the namespaceclasses API.
type NamespaceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NamespaceClassSpec   `json:"spec,omitempty"`
	Status NamespaceClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NamespaceClassList contains a list of NamespaceClass.
type NamespaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespaceClass `json:"items"`
}

// GetConditions returns the list of conditions for a NamespaceClass object.
func (c *NamespaceClass) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

// SetConditions will set the given conditions on a NamespaceClas object.
func (c *NamespaceClass) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&NamespaceClass{}, &NamespaceClassList{})
}
