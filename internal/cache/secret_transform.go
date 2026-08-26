package cache

import (
	corev1 "k8s.io/api/core/v1"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpauth"
)

// githubAppPrivateKeySecretKey matches internal/scms/github/git_operations.go.
const githubAppPrivateKeySecretKey = "githubAppPrivateKey"

// promoterSecretDataKeys lists Secret data keys read by promoter controllers (SCM, HTTP auth,
// kubeconfig). The informer transform retains only these keys to limit cache memory for unrelated
// Secrets that share the instance-id label partition.
func promoterSecretDataKeys() map[string]struct{} {
	return map[string]struct{}{
		httpauth.UsernameKey:          {},
		httpauth.PasswordKey:          {},
		httpauth.TokenKey:             {},
		httpauth.ClientIDKey:          {},
		httpauth.ClientSecretKey:      {},
		httpauth.TLSCertKey:           {},
		httpauth.TLSKeyKey:            {},
		httpauth.TLSCAKey:             {},
		constants.KubeconfigSecretKey: {},
		githubAppPrivateKeySecretKey:  {},
	}
}

// secretDataTransform returns a cache transform that strips managedFields and retains only promoter
// credential keys in Secret data before objects are stored in the informer cache.
func secretDataTransform() toolscache.TransformFunc {
	allowed := promoterSecretDataKeys()
	stripManaged := ctrlcache.TransformStripManagedFields()
	return func(in any) (any, error) {
		out, err := stripManaged(in)
		if err != nil {
			return out, err
		}
		secret, ok := out.(*corev1.Secret)
		if !ok {
			return out, nil
		}
		if len(secret.Data) == 0 && len(secret.StringData) == 0 {
			return secret, nil
		}
		filtered := make(map[string][]byte, len(secret.Data))
		for k, v := range secret.Data {
			if _, keep := allowed[k]; keep {
				filtered[k] = v
			}
		}
		if len(filtered) == 0 {
			secret.Data = nil
		} else {
			secret.Data = filtered
		}
		secret.StringData = nil
		return secret, nil
	}
}

// SecretDataTransformForTest exposes secretDataTransform for unit tests.
func SecretDataTransformForTest() toolscache.TransformFunc {
	return secretDataTransform()
}
