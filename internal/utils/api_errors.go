package utils

import (
	"errors"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NotFoundInErrorChain walks err and reports whether any link is NotFound, and Status.Details from
// the innermost NotFound StatusError that includes kind and name. Details.Kind is the API Kind
// (for example GitRepository) as returned by the API server and client.
func NotFoundInErrorChain(err error) (details *metav1.StatusDetails, isNotFound bool) {
	for unwrapped := err; unwrapped != nil; unwrapped = errors.Unwrap(unwrapped) {
		if !k8serrors.IsNotFound(unwrapped) {
			continue
		}
		isNotFound = true

		var statusErr *k8serrors.StatusError
		if !errors.As(unwrapped, &statusErr) {
			continue
		}
		d := statusErr.Status().Details
		if d == nil || d.Kind == "" || d.Name == "" {
			continue
		}
		details = d
	}
	return details, isNotFound
}
