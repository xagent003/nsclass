package namespace

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xagent003/nsclass/api/v1alpha1"
	"github.com/xagent003/nsclass/pkg/scope"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	NamespaceControllerName = "namespace"
)

// NamespaceReconciler reconciles a Namespace object
type NamespaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.akuity.io,resources=namespaceclasses,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.akuity.io,resources=namespaceclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch;create;update;patch;delete

// Reconcile handles changes to Namespace objects and applies NamespaceClass resources
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := ctrllog.FromContext(ctx).WithName("controllers." + NamespaceControllerName + "." + req.Name)

	namespace := new(corev1.Namespace)
	if err := r.Get(ctx, req.NamespacedName, namespace); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	currentClassName := namespace.Labels[v1alpha1.NamespaceClassLabel]

	var nsClass *v1alpha1.NamespaceClass
	if currentClassName == "" {
		nsClass = nil
	} else {
		nsClass = new(v1alpha1.NamespaceClass)
		nsn := types.NamespacedName{Name: currentClassName}
		if err := r.Get(ctx, nsn, nsClass); err != nil {
			if errors.IsNotFound(err) {
				log.Info("NamespaceClass not found, treating as no class", "name", currentClassName)
				nsClass = nil
			} else {
				log.Error(err, "Failed to get NamespaceClass", "name", currentClassName)
				return ctrl.Result{}, err
			}
		}

		if nsClass != nil {
			log = log.WithValues("namespaceClass", nsClass.Name)
		}
	}

	nsScope, err := scope.NewNamespaceScope(&scope.NamespaceScopeParams{
		Logger:    log,
		Client:    r.Client,
		Namespace: namespace,
		Class:     nsClass,
	})
	if err != nil {
		log.Error(err, "Failed to create namespace scope")
		return ctrl.Result{}, err
	}

	defer func() {
		if err := nsScope.Close(ctx); err != nil {
			log.Error(err, "Failed to close namespace scope")
			reterr = k8serrors.NewAggregate([]error{reterr, err})
		}
	}()

	if nsScope.IsDeleting() {
		return r.reconcileDelete(ctx, nsScope)
	}

	if !controllerutil.ContainsFinalizer(nsScope.Namespace, v1alpha1.NamespaceWithClassFinalizer) {
		controllerutil.AddFinalizer(nsScope.Namespace, v1alpha1.NamespaceWithClassFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileNormal(ctx, nsScope)
}

func (r *NamespaceReconciler) reconcileDelete(ctx context.Context, nsScope *scope.NamespaceScope) (ctrl.Result, error) {
	log := nsScope.Logger
	log.Info("Reconciling Namespace deletion")

	// Check if a class was previously applied
	appliedClassName, applied := nsScope.Namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation]
	if applied && appliedClassName != "" {
		// Remove this namespace from the class's status
		if err := r.removeNamespaceFromClassStatus(ctx, nsScope.Namespace.Name, appliedClassName); err != nil {
			log.Error(err, "Failed to remove namespace from class status during deletion")
			// Continue with finalizer removal even if this fails
		}
	}

	// Resources owned by the class should be cleaned up by garbage collection due to
	// the owner references set during creation, so we don't need to explicitly delete them

	// Remove the finalizer to allow the namespace to be deleted
	controllerutil.RemoveFinalizer(nsScope.Namespace, v1alpha1.NamespaceWithClassFinalizer)
	return ctrl.Result{}, nil
}

func (r *NamespaceReconciler) reconcileNormal(ctx context.Context, nsScope *scope.NamespaceScope) (ctrl.Result, error) {
	log := nsScope.Logger

	if nsScope.Class == nil {
		return r.reconcileNoClass(ctx, nsScope)
	}

	// The benefit of namespace controller vs namepaceclass is we can strictly control order
	// For example when a namespace switches classes, we remove all old resources FIRST
	// Then we reconcile current (or new, if it switched) resources. If current class removed resources
	// we always do cleanup first, before applying new
	//
	// With th namespaceclass controller implementation, we can't guarantee ordering of which NSClass
	// is reconciled first. For example in the case of a class switch, if the new class iss
	// reconciled first, the new resources would be applied before removal of the old class's
	if res, err := r.handleClassSwitching(ctx, nsScope); !res.IsZero() || err != nil {
		return res, err
	}

	if err := r.reconcileClassResources(ctx, nsScope); err != nil {
		log.Error(err, "Failed to reconcile resources")
		return ctrl.Result{}, err
	}

	// Use an annotation to track applied status here since this is a core type, not CRD we own
	if nsScope.Namespace.Annotations == nil {
		nsScope.Namespace.Annotations = make(map[string]string)
	}
	nsScope.Namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] = nsScope.Class.Name

	// Update the NamespaceClass status with resources applied to this namespace
	if err := r.updateNamespaceClassStatus(ctx, nsScope); err != nil {
		log.Error(err, "Failed to update NamespaceClass status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled namespace with NamespaceClass")
	return ctrl.Result{}, nil
}

func (r *NamespaceReconciler) reconcileNoClass(ctx context.Context, nsScope *scope.NamespaceScope) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithValues("namespace", nsScope.Namespace.Name)

	// Check if a class was previously applied
	appliedClassName, applied := nsScope.Namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation]
	if !applied || appliedClassName == "" {
		log.Info("Namespace does not have a previously applied class")
		return ctrl.Result{}, nil
	}

	log.Info("Namespace no longer has a NamespaceClass label, cleaning up resources")
	if err := r.cleanupClassResources(ctx, nsScope, appliedClassName); err != nil {
		log.Error(err, "Failed to clean up resources")
		return ctrl.Result{}, err
	}

	if err := r.removeAppliedClassAnnotation(ctx, nsScope.Namespace); err != nil {
		log.Error(err, "Failed to remove applied class annotation")
		return ctrl.Result{}, err
	}

	if err := r.removeNamespaceFromClassStatus(ctx, nsScope.Namespace.Name, appliedClassName); err != nil {
		log.Error(err, "Failed to remove namespace from class status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NamespaceReconciler) handleClassSwitching(ctx context.Context, nsScope *scope.NamespaceScope) (ctrl.Result, error) {
	log := nsScope.Logger
	namespace := nsScope.Namespace

	appliedClassName, applied := namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation]
	if !applied || appliedClassName == "" || appliedClassName == nsScope.Class.Name {
		return ctrl.Result{}, nil
	}

	log.Info("Namespace changed NamespaceClass", "oldClass", appliedClassName, "newClass", nsScope.Class.Name)
	if err := r.cleanupClassResources(ctx, nsScope, appliedClassName); err != nil {
		log.Error(err, "Failed to clean up resources from old class")
		return ctrl.Result{}, err
	}

	if err := r.removeNamespaceFromClassStatus(ctx, nsScope.Namespace.Name, appliedClassName); err != nil {
		log.Error(err, "Failed to remove namespace from old class status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NamespaceReconciler) reconcileClassResources(ctx context.Context, nsScope *scope.NamespaceScope) error {
	log := nsScope.Logger
	log.Info("Applying NamespaceClass resources")

	if err := r.reconcileRemovedResources(ctx, nsScope); err != nil {
		log.Error(err, "Failed to handle removed resources")
		return err
	}

	if len(nsScope.Class.Spec.InheritedResources) == 0 {
		log.Info("NamespaceClass has no resources to deploy")
		return nil
	}

	var errs []error
	for _, resource := range nsScope.Class.Spec.InheritedResources {
		if err := r.reconcileResource(ctx, nsScope, &resource); err != nil {
			log.Error(err, "Failed to apply resource", "apiVersion", resource.APIVersion, "kind", resource.Kind, "name", resource.ObjectMeta.Name)
			errs = append(errs, err)
		}
	}

	return k8serrors.NewAggregate(errs)
}

func (r *NamespaceReconciler) reconcileResource(ctx context.Context, nsScope *scope.NamespaceScope, resource *v1alpha1.NamespaceResource) error {
	log := nsScope.Logger
	log.Info("Applying resource", "apiVersion", resource.APIVersion, "kind", resource.Kind, "name", resource.ObjectMeta.Name)

	gvk := schema.FromAPIVersionAndKind(resource.APIVersion, resource.Kind)
	obj := new(unstructured.Unstructured)
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

		return controllerutil.SetControllerReference(nsScope.Class, obj, r.Scheme)
	})

	if err != nil {
		log.Error(err, "Failed to create or update resource")
		return err
	}

	log.Info("Resource reconciled", "operation", op)
	return nil
}

func (r *NamespaceReconciler) cleanupClassResources(ctx context.Context, nsScope *scope.NamespaceScope, className string) error {
	log := nsScope.Logger
	log.Info("Cleaning up resources applied by NamespaceClass", "class", className)

	nsClass := new(v1alpha1.NamespaceClass)
	if err := r.Get(ctx, types.NamespacedName{Name: className}, nsClass); err != nil {
		if errors.IsNotFound(err) {
			// Resources owned by the class should be cleaned up by k8s GC due to controller ref
			log.Info("NamespaceClass is gone - resources owned by it will be cleaned up by garbage collection")
			return nil
		}
		return err
	}

	var resourcesToDelete []corev1.ObjectReference
	if nsClass.Status.NamespaceResources != nil {
		if refs, exists := nsClass.Status.NamespaceResources[nsScope.Namespace.Name]; exists {
			resourcesToDelete = refs
		}
	}

	if len(resourcesToDelete) == 0 && len(nsClass.Spec.InheritedResources) > 0 {
		for _, res := range nsClass.Spec.InheritedResources {
			resourcesToDelete = append(resourcesToDelete, corev1.ObjectReference{
				APIVersion: res.APIVersion,
				Kind:       res.Kind,
				Name:       res.ObjectMeta.Name,
			})
		}
	}

	if len(resourcesToDelete) == 0 {
		log.Info("No resources to clean up for this namespace")
		return nil
	}

	var errs []error
	for _, ref := range resourcesToDelete {
		log.Info("Deleting resource", "apiVersion", ref.APIVersion, "kind", ref.Kind, "name", ref.Name)

		obj := new(unstructured.Unstructured)
		obj.SetGroupVersionKind(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
		obj.SetName(ref.Name)
		obj.SetNamespace(nsScope.Namespace.Name)

		if err := r.Client.Delete(ctx, obj); err != nil {
			if !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete resource")
				errs = append(errs, err)
			}
		}
	}

	return k8serrors.NewAggregate(errs)
}

func (r *NamespaceReconciler) removeAppliedClassAnnotation(ctx context.Context, namespace *corev1.Namespace) error {
	if namespace.Annotations == nil || namespace.Annotations[v1alpha1.NamespaceAppliedClassAnnotation] == "" {
		return nil
	}

	patchHelper, err := patch.NewHelper(namespace, r.Client)
	if err != nil {
		return err
	}

	delete(namespace.Annotations, v1alpha1.NamespaceAppliedClassAnnotation)
	return patchHelper.Patch(ctx, namespace)
}

func (r *NamespaceReconciler) updateNamespaceClassStatus(ctx context.Context, nsScope *scope.NamespaceScope) error {
	log := nsScope.Logger

	nsClass := nsScope.Class.DeepCopy()

	if nsClass.Status.NamespaceResources == nil {
		nsClass.Status.NamespaceResources = make(map[string][]corev1.ObjectReference)
	}

	resourceRefs := make([]corev1.ObjectReference, 0, len(nsClass.Spec.InheritedResources))
	for _, resource := range nsClass.Spec.InheritedResources {
		resourceRefs = append(resourceRefs, corev1.ObjectReference{
			APIVersion: resource.APIVersion,
			Kind:       resource.Kind,
			Name:       resource.ObjectMeta.Name,
		})
	}

	nsClass.Status.NamespaceResources[nsScope.Namespace.Name] = resourceRefs

	if err := r.Status().Update(ctx, nsClass); err != nil {
		log.Error(err, "Failed to update NamespaceClass status")
		return err
	}

	log.Info("Updated NamespaceClass status with applied resources for this namespace")
	return nil
}

// removeNamespaceFromClassStatus removes a namespace from a NamespaceClass's status
func (r *NamespaceReconciler) removeNamespaceFromClassStatus(ctx context.Context, namespaceName, className string) error {
	nsClass := new(v1alpha1.NamespaceClass)
	if err := r.Get(ctx, types.NamespacedName{Name: className}, nsClass); err != nil {
		if errors.IsNotFound(err) {
			// Class is gone, nothing to update
			return nil
		}
		return err
	}

	nsClass = nsClass.DeepCopy()

	if nsClass.Status.NamespaceResources != nil {
		delete(nsClass.Status.NamespaceResources, namespaceName)
	}

	return r.Status().Update(ctx, nsClass)
}

// reconcileRemovedResources handles cleaning up resources that have been removed from the NamespaceClass
func (r *NamespaceReconciler) reconcileRemovedResources(ctx context.Context, nsScope *scope.NamespaceScope) error {
	log := nsScope.Logger
	namespace := nsScope.Namespace
	nsClass := nsScope.Class

	if nsClass.Status.NamespaceResources == nil {
		log.Info("No namespaces tracked in class status yet, skipping removed resource handling")
		return nil
	}

	prevResources, exists := nsClass.Status.NamespaceResources[namespace.Name]
	if !exists {
		log.Info("Namespace not yet tracked in class status, skipping removed resource handling")
		return nil
	}

	currentResources := make(map[string]bool)
	for _, resource := range nsClass.Spec.InheritedResources {
		key := getResourceKey(resource.APIVersion, resource.Kind, resource.ObjectMeta.Name)
		currentResources[key] = true
	}

	var removedResources []corev1.ObjectReference
	for _, ref := range prevResources {
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

	var errs []error
	for _, ref := range removedResources {
		log.Info("Deleting resource that was removed from class",
			"apiVersion", ref.APIVersion,
			"kind", ref.Kind,
			"name", ref.Name)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
		obj.SetName(ref.Name)
		obj.SetNamespace(namespace.Name)

		if err := r.Client.Delete(ctx, obj); err != nil {
			if !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete resource")
				errs = append(errs, err)
			}
		}
	}

	return k8serrors.NewAggregate(errs)
}

// each resource in a namespace is keyd by the GVK+name (within a given namespace only!)
func getResourceKey(apiVersion, kind, name string) string {
	return fmt.Sprintf("%s/%s/%s", apiVersion, kind, name)
}

func (r *NamespaceReconciler) namespaceClassToNamespaces(ctx context.Context, obj client.Object) []reconcile.Request {
	nsClass, ok := obj.(*v1alpha1.NamespaceClass)
	if !ok {
		return nil
	}

	log := ctrllog.FromContext(ctx).WithValues("namespaceClass", nsClass.Name)
	log.Info("NamespaceClass change detected, finding affected namespaces")

	// Find all namespaces using this class
	nsList := &corev1.NamespaceList{}
	if err := r.List(ctx, nsList, client.MatchingLabels{
		v1alpha1.NamespaceClassLabel: nsClass.Name,
	}); err != nil {
		log.Error(err, "Failed to list namespaces using this class")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		if !ns.DeletionTimestamp.IsZero() {
			continue
		}

		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: ns.Name,
			},
		})
	}

	log.Info("Enqueuing namespaces for reconciliation", "count", len(requests))
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Watches(
			&v1alpha1.NamespaceClass{},
			handler.EnqueueRequestsFromMapFunc(r.namespaceClassToNamespaces),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Named(NamespaceControllerName).
		Complete(r)
}
