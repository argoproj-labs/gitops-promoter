/*
Copyright 2026.

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

// Package webhook holds the admission webhooks served by the controller.
package webhook

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
)

// +kubebuilder:webhook:path=/validate-promoter-argoproj-io-v1alpha1-kubernetesresourcecommitstatus,mutating=false,failurePolicy=fail,sideEffects=None,groups=promoter.argoproj.io,resources=kubernetesresourcecommitstatuses,verbs=create;update,versions=v1alpha1,name=vkubernetesresourcecommitstatus.promoter.argoproj.io,admissionReviewVersions=v1,timeoutSeconds=10

// KubernetesResourceCommitStatusValidator rejects gates whose template or expressions cannot work before they are
// stored: the template's kind must be one the controller is configured to create and one the cluster serves, every
// ${ } expression must compile against its variables, expressions and literals must match the types of the fields
// they populate, and template fields must exist in the kind's schema. It evaluates nothing, so it needs no
// PromotionStrategy and makes no changes.
//
// The same checks run again in the controller, which reports them in the gate's status; the webhook only moves
// them to admission time, where kubectl and GitOps tools show them.
type KubernetesResourceCommitStatusValidator struct {
	// Compiler resolves kind schemas and compiles expressions. Share the controller's so both see the same
	// schemas.
	Compiler *resourcetemplate.Compiler
	// Configured is the set of kinds gates may create: the namespaced kinds listed in
	// ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources that the cluster served at startup.
	Configured map[schema.GroupVersionKind]struct{}
}

var _ admission.Validator[*promoterv1alpha1.KubernetesResourceCommitStatus] = (*KubernetesResourceCommitStatusValidator)(nil)

// SetupWithManager registers the webhook on mgr's webhook server.
func (v *KubernetesResourceCommitStatusValidator) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewWebhookManagedBy(mgr, &promoterv1alpha1.KubernetesResourceCommitStatus{}).WithValidator(v).Complete(); err != nil {
		return fmt.Errorf("failed to register the KubernetesResourceCommitStatus validating webhook: %w", err)
	}
	return nil
}

// ValidateCreate implements admission.Validator.
func (v *KubernetesResourceCommitStatusValidator) ValidateCreate(_ context.Context, krcs *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	return v.validate(krcs)
}

// ValidateUpdate implements admission.Validator. An update that leaves spec unchanged (labels, finalizers,
// ownership) is accepted without looking at the cluster, so a gate stays manageable even when its kind's CRD is
// gone.
func (v *KubernetesResourceCommitStatusValidator) ValidateUpdate(_ context.Context, oldKRCS, newKRCS *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	if equality.Semantic.DeepEqual(oldKRCS.Spec, newKRCS.Spec) {
		return nil, nil
	}
	return v.validate(newKRCS)
}

// ValidateDelete implements admission.Validator; deletions are never rejected.
func (v *KubernetesResourceCommitStatusValidator) ValidateDelete(context.Context, *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	return nil, nil
}

func (v *KubernetesResourceCommitStatusValidator) validate(krcs *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	specPath := field.NewPath("spec")
	templatePath := specPath.Child("template")
	invalid := func(errs field.ErrorList) error {
		return apierrors.NewInvalid(promoterv1alpha1.GroupVersion.WithKind("KubernetesResourceCommitStatus").GroupKind(), krcs.Name, errs)
	}

	template, gvk, err := resourcetemplate.ParseTemplate(krcs.Spec.Template)
	if err != nil {
		return nil, invalid(field.ErrorList{field.Invalid(templatePath, "", err.Error())})
	}
	if _, ok := v.Configured[gvk]; !ok {
		return nil, invalid(field.ErrorList{field.Invalid(templatePath.Child("kind"), gvk.String(),
			"the kind is not listed in ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources, is cluster-scoped, or was not served when the controller started; the controller reads the list at startup")})
	}

	ks, err := v.Compiler.ResolveKindSchema(gvk)
	switch {
	case errors.Is(err, resolver.ErrSchemaNotFound):
		return nil, invalid(field.ErrorList{field.Invalid(templatePath.Child("kind"), gvk.String(), "the cluster does not serve this kind")})
	case err != nil:
		return nil, apierrors.NewInternalError(fmt.Errorf("failed to resolve the schema of %s: %w", gvk, err)) //nolint:wrapcheck // a StatusError sets the admission response code
	}

	warnings, errs := resourcetemplate.ValidateSpec(v.Compiler, gvk, ks, template, &krcs.Spec, specPath)
	if len(errs) > 0 {
		return warnings, invalid(errs)
	}
	return warnings, nil
}
