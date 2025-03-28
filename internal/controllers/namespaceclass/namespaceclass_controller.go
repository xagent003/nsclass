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

package namespaceclass

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/xagent003/nsclass/api/v1alpha1"
	"github.com/xagent003/nsclass/pkg/scope"
)

const (
	NamespaceClassControllerName = "namespaceclass"
)

// NamespaceClassReconciler reconciles a NamespaceClass object
type NamespaceClassReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core.akuity.io,resources=namespaceclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.akuity.io,resources=namespaceclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.akuity.io,resources=namespaceclasses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch;create;update;patch;delete

// Reconcile handles changes to NamespaceClass resources and applies configuration to all matching namespaces
func (r *NamespaceClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := ctrllog.FromContext(ctx).WithName("controllers." + NamespaceClassControllerName + "." + req.Name)

	nsClass := new(v1alpha1.NamespaceClass)
	if err := r.Get(ctx, req.NamespacedName, nsClass); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	scope, err := scope.NewNamespaceClassScope(&scope.NamespaceClassScopeParams{
		Logger:         log,
		Client:         r.Client,
		NamespaceClass: nsClass,
	})
	if err != nil {
		log.Error(err, "failed to create NamespaceClass scope")
		return ctrl.Result{}, err
	}

	defer func() {
		r.reconcileStatus(ctx, scope)
		if err := scope.Close(ctx); err != nil {
			reterr = k8serrors.NewAggregate([]error{reterr, err})
		}
	}()

	if scope.IsDeleting() {
		return r.reconcileDelete(ctx, scope)
	}

	return r.reconcileNormal(ctx, scope)
}

func (r *NamespaceClassReconciler) reconcileNormal(ctx context.Context, classScope *scope.NamespaceClassScope) (ctrl.Result, error) {
	log := classScope.Logger

	if !controllerutil.ContainsFinalizer(classScope.NamespaceClass, v1alpha1.NamespaceClassFinalizer) {
		controllerutil.AddFinalizer(classScope.NamespaceClass, v1alpha1.NamespaceClassFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	nsList := new(corev1.NamespaceList)
	nsSelector := client.MatchingLabels{v1alpha1.NamespaceClassLabel: classScope.NamespaceClass.Name}
	if err := r.List(ctx, nsList, nsSelector); err != nil {
		log.Error(err, "failed to list namespaces", "selector", nsSelector)
		conditions.MarkFalse(classScope.NamespaceClass, v1alpha1.ConditionNamespaceClassResourcesReady,
			v1alpha1.ReasonNamespaceListFailed, clusterv1.ConditionSeverityError,
			"Failed to list namespaces: %v", err)
		return ctrl.Result{}, err
	}

	// Ordering is as follows:
	// First we reconcile any namespaces to purge (that we have status for, but dont request us anymore)
	// Then we compute diff of resource we had last successfully applied vs desired spec, and remove in current namespaces
	// Finally apply current spec of resources to current namespaces

	if err := r.reconcileRemovedNamespaces(ctx, classScope, nsList); err != nil {
		return ctrl.Result{}, err
	}

	// Handle removed resources (cleanup resources no longer in class spec from all namespaces)
	if err := r.reconcileRemovedResources(ctx, classScope, nsList); err != nil {
		log.Error(err, "failed to handle removed resources")
		return ctrl.Result{}, err
	}

	// Now, apply resources to all matching namespaces
	if err := r.reconcileMatchingNamespaces(ctx, classScope, nsList); err != nil {
		return ctrl.Result{}, err
	}

	// Finally, update our status with current resources and list of managed namespaces
	updateStatusAppliedResources(classScope)
	updateStatusManagedNamespaces(classScope, nsList)

	conditions.MarkTrue(classScope.NamespaceClass, v1alpha1.ConditionNamespaceClassResourcesReady)
	return ctrl.Result{}, nil
}

// handleRemovedResources finds and removes resources that were previously in the class spec but have been removed
func (r *NamespaceClassReconciler) reconcileRemovedResources(ctx context.Context, classScope *scope.NamespaceClassScope, nsList *corev1.NamespaceList) error {
	log := classScope.Logger

	// Skip this step if we have no Status.ResourceRefs to compare against (first reconciliation)
	if len(classScope.NamespaceClass.Status.ResourceRefs) == 0 {
		log.Info("No resources in status to compare, skipping removed resource handling")
		return nil
	}

	// Create map of current resources for efficient lookup
	currentResources := make(map[string]bool)
	for _, resource := range classScope.NamespaceClass.Spec.InheritedResources {
		key := getResourceKey(resource.APIVersion, resource.Kind, resource.ObjectMeta.Name)
		currentResources[key] = true
	}

	// Find resources that were removed from spec
	var removedResources []corev1.ObjectReference
	for _, ref := range classScope.NamespaceClass.Status.ResourceRefs {
		key := getResourceKey(ref.APIVersion, ref.Kind, ref.Name)
		if !currentResources[key] {
			log.Info("Resource was removed from class and will be cleaned up",
				"apiVersion", ref.APIVersion,
				"kind", ref.Kind,
				"name", ref.Name)
			removedResources = append(removedResources, ref)
		}
	}

	if len(removedResources) == 0 {
		log.Info("No resources were removed from class spec")
		return nil
	}

	// For each namespace using this class, remove the removed resources
	var errs []error
	for i := range nsList.Items {
		ns := &nsList.Items[i]

		if !ns.DeletionTimestamp.IsZero() {
			continue
		}

		// Since we're removing resource rely on the APPLIED class thats set only after succesful application
		// the label tells us what is desired. If Namespace was a CRD we would prob use spec/status rather
		// than relying on labels and/or annotations.
		appliedClass, ok := ns.Annotations[v1alpha1.NamespaceAppliedClassAnnotation]
		if !ok || appliedClass != classScope.NamespaceClass.Name {
			continue
		}

		nsLog := log.WithValues("namespace", ns.Name)
		nsLog.Info("Removing resources from namespace", "resourceCount", len(removedResources))

		for _, ref := range removedResources {
			nsLog.Info("Deleting resource",
				"apiVersion", ref.APIVersion,
				"kind", ref.Kind,
				"name", ref.Name)

			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
			obj.SetName(ref.Name)
			obj.SetNamespace(ns.Name)

			if err := r.Client.Delete(ctx, obj); err != nil {
				if !errors.IsNotFound(err) {
					nsLog.Error(err, "Failed to delete resource")
					errs = append(errs, err)
				}
			}
		}
	}

	return k8serrors.NewAggregate(errs)
}

func (r *NamespaceClassReconciler) reconcileMatchingNamespaces(ctx context.Context, classScope *scope.NamespaceClassScope, nsList *corev1.NamespaceList) error {
	log := classScope.Logger
	// Try to apply the NamespaceClass to each namespace best-effort
	// if anything error's out, compound the errors and move onto next namespace
	var errs []error
	for i := range nsList.Items {
		ns := &nsList.Items[i]

		nsScope, err := scope.NewNamespaceScope(&scope.NamespaceScopeParams{
			Logger:    log.WithValues("namespace", ns.Name),
			Client:    r.Client,
			Namespace: ns,
			Class:     classScope.NamespaceClass,
		})
		if err != nil {
			log.Error(err, "failed to create Namespace scope", "namespace", ns.Name)
			errs = append(errs, err)
			continue
		}

		if !ns.DeletionTimestamp.IsZero() {
			log.Info("namespace is deleting, not applying namespace class resources")
			continue
		}

		if err := r.reconcileNamespaceResources(ctx, nsScope); err != nil {
			log.Error(err, "failed to reconcile resources for namespace", "namespace", ns.Name)
			errs = append(errs, err)
		}

		if nsScope.Namespace.Annotations == nil {
			nsScope.Namespace.Annotations = make(map[string]string)
		}
		nsScope.Namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] = nsScope.Class.Name

		if err := nsScope.Close(ctx); err != nil {
			log.Error(err, "failed to close Namespace scope", "namespace", ns.Name)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		conditions.MarkFalse(classScope.NamespaceClass, v1alpha1.ConditionNamespaceClassResourcesReady,
			v1alpha1.ReasonResourcesApplyFailed, clusterv1.ConditionSeverityError,
			"Failed to apply resources: %v", k8serrors.NewAggregate(errs))

	}
	return k8serrors.NewAggregate(errs)
}

// reconcileNamespaceResources reconciles resources for a specific namespace
func (r *NamespaceClassReconciler) reconcileNamespaceResources(ctx context.Context, nsScope *scope.NamespaceScope) error {
	log := nsScope.Logger
	log.Info("Reconciling resources for namespace")

	if len(nsScope.Class.Spec.InheritedResources) == 0 {
		log.Info("Namespace class has no resources to deploy for namespace")
		return nil
	}

	var errs []error
	for _, resource := range nsScope.Class.Spec.InheritedResources {
		err := r.reconcileResource(ctx, nsScope, &resource)
		if err != nil {
			// Try to deploy all resources best-effort, and just log the error
			log.Error(err, "failed to deploy resource", "apiVersion", resource.APIVersion, "kind", resource.Kind, "name", resource.ObjectMeta.Name)
			errs = append(errs, err)
		}
	}

	return k8serrors.NewAggregate(errs)
}

// reconcileResource reconciles a single resource in a namespace
func (r *NamespaceClassReconciler) reconcileResource(ctx context.Context, nsScope *scope.NamespaceScope, resource *v1alpha1.NamespaceResource) error {
	log := nsScope.Logger
	log.Info("Reconciling resource", "apiVersion", resource.APIVersion, "kind", resource.Kind, "name", resource.ObjectMeta.Name)

	gvk := schema.FromAPIVersionAndKind(resource.APIVersion, resource.Kind)
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(resource.ObjectMeta.Name)
	obj.SetNamespace(nsScope.Namespace.Name)

	// Decided to move this here since resources may have immutable fields and a Update or Patch
	// will fail. Since we can support ANY resource, it is complex to figure out the merging logic
	// For example if we don't specify "spec.foo" we can't know whether we want to remove that field
	// or leave it untouched. Or if namespace class changes "spec.foo: 1" to "2"
	// For example Patch (or createOrPatch) even fails on a no-op reconciliation for a Pod since our template
	// only contains a few fields and k8s Pod controller adds a lot more.
	// As such, make the entire template immutable, we can only support adding or removing resources or changing metadata

	if resource.SpecTemplate != nil {
		var specMap map[string]interface{}
		if err := json.Unmarshal(resource.SpecTemplate.Raw, &specMap); err != nil {
			log.Error(err, "failed to unmarshal spec template", "resource", resource.ObjectMeta)
			return fmt.Errorf("failed to unmarshal spec template: %w", err)
		}

		/* does not work for resources without a spec
		log.Info("applying", "spec", specMap)
		if err := unstructured.SetNestedField(obj.Object, specMap, "spec"); err != nil {
			return fmt.Errorf("failed to set spec: %w", err)
		}
		*/
		for k, v := range specMap {
			// Skip apiVersion, kind, and metadata as they're handled separately
			if k != "apiVersion" && k != "kind" && k != "metadata" {
				log.Info("applying field", "field", k, "value", v)
				obj.Object[k] = v
			}
		}
	}

	op, err := controllerutil.CreateOrPatch(ctx, r.Client, obj, func() error {
		if resource.ObjectMeta.Labels != nil {
			existingLabels := obj.GetLabels()
			if existingLabels == nil {
				existingLabels = make(map[string]string)
			}
			for k, v := range resource.ObjectMeta.Labels {
				existingLabels[k] = v
			}
			obj.SetLabels(existingLabels)
		}

		if resource.ObjectMeta.Annotations != nil {
			existingAnnotations := obj.GetAnnotations()
			if existingAnnotations == nil {
				existingAnnotations = make(map[string]string)
			}
			for k, v := range resource.ObjectMeta.Annotations {
				existingAnnotations[k] = v
			}
			obj.SetAnnotations(existingAnnotations)
		}

		// Set controller owner ref, so if namespaceClass is deleted, the resource is garbage collected
		return controllerutil.SetControllerReference(nsScope.Class, obj, r.Scheme)
	})

	if err != nil {
		log.Error(err, "failed to CreateOrPatch resource")
		return err
	}

	log.Info("reconciled resource", "operation", op, "apiVersion", resource.APIVersion, "kind", resource.Kind, "name", resource.ObjectMeta.Name)

	return nil
}

func (r *NamespaceClassReconciler) reconcileRemovedNamespaces(ctx context.Context, scope *scope.NamespaceClassScope, nsList *corev1.NamespaceList) error {
	log := scope.Logger
	matchingNsNames := make(map[string]struct{})
	for _, ns := range nsList.Items {
		matchingNsNames[ns.Name] = struct{}{}
	}

	log.Info("Found namespaces referencing this NamespaceClass", "namespaces", matchingNsNames)

	currNsNames := make(map[string]struct{})
	for _, ns := range scope.NamespaceClass.Status.ManagedNamespaces {
		currNsNames[ns] = struct{}{}
	}

	removedNS := make(map[string]struct{})
	for ns := range currNsNames {
		if _, exists := matchingNsNames[ns]; !exists {
			removedNS[ns] = struct{}{}
		}
	}

	var errs []error
	for ns := range removedNS {
		log.Info("Cleaning up resources in namespace that no longer uses this class", "namespace", ns)
		if err := r.cleanupNSResources(ctx, scope, ns); err != nil {
			log.Error(err, "failed to cleanup resources", "namespace", ns)
			errs = append(errs, err)
		}
	}

	return k8serrors.NewAggregate(errs)
}

func (r *NamespaceClassReconciler) cleanupNSResources(ctx context.Context, scope *scope.NamespaceClassScope, nsName string) error {
	log := scope.Logger

	// For a given namespace, try deleting all resources in a class tempalte
	// for example, if "platform9-system" moved from "foo" to "bar" we can use this to remove
	// everything that "foo" Class owns. Ignore if not found, since we want it gone anyways
	var errs []error
	for _, ref := range scope.NamespaceClass.Status.ResourceRefs {
		obj := new(unstructured.Unstructured)
		obj.SetGroupVersionKind(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
		obj.SetName(ref.Name)
		obj.SetNamespace(nsName)
		if err := r.Client.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to delete resource", "obj", obj)
			errs = append(errs, err)
		}
	}

	// Only clear the annotation if it still references THIS class we just removed
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: nsName}, namespace); err == nil {
		if namespace.Annotations != nil &&
			namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] == scope.NamespaceClass.Name {
			log.Info("Removing applied class annotation", "namespace", nsName)
			patchHelper, err := patch.NewHelper(namespace, r.Client)
			if err == nil {
				delete(namespace.Annotations, v1alpha1.NamespaceAppliedClassAnnotation)
				if err := patchHelper.Patch(ctx, namespace); err != nil {
					log.Error(err, "Failed to patch namespace", "namespace", nsName)
					errs = append(errs, err)
				}
			} else {
				log.Error(err, "Failed to create patch helper", "namespace", nsName)
				errs = append(errs, err)
			}
		}
	} else if !errors.IsNotFound(err) {
		log.Error(err, "Failed to get namespace", "namespace", nsName)
		errs = append(errs, err)
	}

	return k8serrors.NewAggregate(errs)
}

// this just copies the spec over to status to track what was last succesfully applied
func updateStatusAppliedResources(classScope *scope.NamespaceClassScope) {
	refs := make([]corev1.ObjectReference, 0, len(classScope.NamespaceClass.Spec.InheritedResources))
	for _, resource := range classScope.NamespaceClass.Spec.InheritedResources {
		applied := corev1.ObjectReference{
			APIVersion: resource.APIVersion,
			Kind:       resource.Kind,
			Name:       resource.ObjectMeta.Name,
		}
		refs = append(refs, applied)
	}

	classScope.NamespaceClass.Status.ResourceRefs = refs
}

func updateStatusManagedNamespaces(classScope *scope.NamespaceClassScope, nsList *corev1.NamespaceList) {
	// Update ManagedNamespaces in status to track current namespaces using this class
	managedNamespaces := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		managedNamespaces = append(managedNamespaces, ns.Name)
	}
	classScope.NamespaceClass.Status.ManagedNamespaces = managedNamespaces
}

func getResourceKey(api, kind, name string) string {
	return fmt.Sprintf("%s/%s/%s", api, kind, name)
}

func (r *NamespaceClassReconciler) reconcileStatus(_ context.Context, classScope *scope.NamespaceClassScope) {
	// Set the summary based on all relevant conditions
	conditions.SetSummary(classScope.NamespaceClass,
		conditions.WithConditions(
			v1alpha1.ConditionNamespaceClassResourcesReady,
		),
	)

	readyCondition := conditions.Get(classScope.NamespaceClass, clusterv1.ReadyCondition)

	ready := false
	var reason string

	if !classScope.IsDeleting() {
		if readyCondition == nil {
			conditions.MarkUnknown(classScope.NamespaceClass, clusterv1.ReadyCondition, "", "")
			readyCondition = conditions.Get(classScope.NamespaceClass, clusterv1.ReadyCondition)
		}

		switch readyCondition.Status {
		case corev1.ConditionTrue:
			ready = true
		case corev1.ConditionFalse, corev1.ConditionUnknown:
			ready = false
		}
		reason = readyCondition.Reason
	} else {
		ready = false
		reason = "Deleting"
	}

	classScope.NamespaceClass.Status.Ready = ready
	classScope.NamespaceClass.Status.Reason = reason
}

// reconcileDelete handles NamespaceClass deletion
func (r *NamespaceClassReconciler) reconcileDelete(_ context.Context, classScope *scope.NamespaceClassScope) (ctrl.Result, error) {
	log := classScope.Logger
	log.Info("Reconciling NamespaceClass deletion")

	// TODO: anything to do here? with controllerownerRef, the resources created should be cleaned up
	conditions.MarkFalse(classScope.NamespaceClass, v1alpha1.ConditionNamespaceClassResourcesReady,
		"Deleting", clusterv1.ConditionSeverityInfo, "NamespaceClass is being deleted")

	controllerutil.RemoveFinalizer(classScope.NamespaceClass, v1alpha1.NamespaceClassFinalizer)
	return ctrl.Result{}, nil
}

func (r *NamespaceClassReconciler) namespaceToNamespaceClass(ctx context.Context, obj client.Object) []reconcile.Request {
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil
	}

	requests := make([]reconcile.Request, 0)

	// Get current class from label
	currentClass := namespace.Labels[v1alpha1.NamespaceClassLabel]

	// If last applied is different, enqueue the old class for cleanup
	if appliedClass, ok := namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation]; ok && appliedClass != "" && appliedClass != currentClass {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: appliedClass},
		})
	}
	if currentClass != "" {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: currentClass},
		})
	}

	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NamespaceClass{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.namespaceToNamespaceClass),
		).
		Named(NamespaceClassControllerName).
		Complete(r)
}
