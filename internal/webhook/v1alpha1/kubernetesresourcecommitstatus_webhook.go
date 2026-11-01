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

// Package v1alpha1 holds the admission webhooks for promoter.argoproj.io/v1alpha1 kinds.
package v1alpha1

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
)

// +kubebuilder:webhook:path=/validate-promoter-argoproj-io-v1alpha1-kubernetesresourcecommitstatus,mutating=false,failurePolicy=fail,sideEffects=None,groups=promoter.argoproj.io,resources=kubernetesresourcecommitstatuses,verbs=create;update,versions=v1alpha1,name=vkubernetesresourcecommitstatus.promoter.argoproj.io,admissionReviewVersions=v1,timeoutSeconds=10

// KubernetesResourceCommitStatusValidator rejects a KubernetesResourceCommitStatus whose spec.template the
// controller would refuse: a template without a literal apiVersion and kind, a kind the controller was not
// configured to create, or a template that does not match the kind's published schema (see
// resourcetemplate.Validator). The controller runs the same checks on every reconcile.
type KubernetesResourceCommitStatusValidator struct {
	// Templates validates spec.template against the kind's schema.
	Templates *resourcetemplate.Validator
	// IsConfiguredKind reports whether the controller creates gvk
	// (ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources, as read at startup).
	IsConfiguredKind func(gvk schema.GroupVersionKind) bool
}

var _ admission.Validator[*promoterv1alpha1.KubernetesResourceCommitStatus] = &KubernetesResourceCommitStatusValidator{}

// SetupWithManager registers the validating webhook with mgr's webhook server.
func (v *KubernetesResourceCommitStatusValidator) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewWebhookManagedBy(mgr, &promoterv1alpha1.KubernetesResourceCommitStatus{}).
		WithValidator(v).
		Complete(); err != nil {
		return fmt.Errorf("failed to register the KubernetesResourceCommitStatus webhook: %w", err)
	}
	return nil
}

// ValidateCreate validates a new gate.
func (v *KubernetesResourceCommitStatusValidator) ValidateCreate(ctx context.Context, krcs *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	return nil, v.validate(ctx, krcs)
}

// ValidateUpdate validates an updated gate. Status and metadata-only updates do not change spec.template, so they
// are not re-validated: a schema change of the created kind must not block the controller's own status writes
// or a finalizer removal.
func (v *KubernetesResourceCommitStatusValidator) ValidateUpdate(ctx context.Context, oldKRCS, newKRCS *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	if oldKRCS.Generation == newKRCS.Generation && string(oldKRCS.Spec.Template.Raw) == string(newKRCS.Spec.Template.Raw) {
		return nil, nil
	}
	return nil, v.validate(ctx, newKRCS)
}

// ValidateDelete allows every deletion.
func (v *KubernetesResourceCommitStatusValidator) ValidateDelete(context.Context, *promoterv1alpha1.KubernetesResourceCommitStatus) (admission.Warnings, error) {
	return nil, nil
}

// validate returns an API StatusError, unwrapped, so the API server reports its code and field causes to the client.
//
//nolint:wrapcheck // StatusErrors must reach the webhook response unwrapped.
func (v *KubernetesResourceCommitStatusValidator) validate(ctx context.Context, krcs *promoterv1alpha1.KubernetesResourceCommitStatus) error {
	gk := promoterv1alpha1.GroupVersion.WithKind("KubernetesResourceCommitStatus").GroupKind()
	template, gvk, err := resourcetemplate.ParseTemplate(krcs.Spec.Template)
	if err != nil {
		return k8serrors.NewInvalid(gk, krcs.Name, field.ErrorList{field.Invalid(resourcetemplate.TemplatePath, field.OmitValueType{}, err.Error())})
	}
	if v.IsConfiguredKind != nil && !v.IsConfiguredKind(gvk) {
		return k8serrors.NewInvalid(gk, krcs.Name, field.ErrorList{field.Invalid(resourcetemplate.TemplatePath.Child("kind"), gvk.String(),
			"the controller does not create this kind: it must be a namespaced kind listed in ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources (changes there apply after a controller restart)")})
	}
	errs, err := v.Templates.Validate(ctx, template, gvk)
	if err != nil {
		return k8serrors.NewInternalError(fmt.Errorf("failed to validate spec.template: %w", err))
	}
	if len(errs) > 0 {
		return k8serrors.NewInvalid(gk, krcs.Name, errs)
	}
	return nil
}
