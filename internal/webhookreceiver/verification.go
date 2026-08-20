package webhookreceiver

import (
	"context"
	"fmt"
	"net/http"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type verificationCandidate struct {
	header string
	secret []byte
}

// verifyInboundWebhook checks whether the delivery satisfies VerificationRequired ScmProviders
// resolved from payload repository identity and/or a matched ChangeTransferPolicy.
// Returns (0, "") when authorized, or an HTTP status and message to reject.
func (wr *WebhookReceiver) verifyInboundWebhook(ctx context.Context, provider, owner, name string, headers http.Header, body []byte, ctp *promoterv1alpha1.ChangeTransferPolicy) (status int, msg string) {
	if wr.k8sClient == nil {
		return 0, ""
	}
	logger := log.FromContext(ctx)

	gitRepos, err := wr.collectGitRepositoriesForVerification(ctx, provider, owner, name, ctp)
	if err != nil {
		logger.Error(err, "failed to collect GitRepositories for webhook verification")
		return http.StatusInternalServerError, "error verifying webhook"
	}
	if len(gitRepos) == 0 {
		return 0, ""
	}

	var candidates []verificationCandidate
	seenSecrets := map[string]struct{}{}
	verificationRequired := false

	for i := range gitRepos {
		gr := &gitRepos[i]
		if gr.Spec.ScmProviderRef.Name == "" {
			continue
		}

		scmProvider, err := utils.GetScmProviderFromGitRepository(ctx, wr.k8sClient, gr, gr)
		if err != nil {
			logger.V(4).Info("skipping GitRepository for webhook verification; could not resolve ScmProvider",
				"namespace", gr.Namespace, "name", gr.Name, "error", err.Error())
			continue
		}
		if scmProvider.GetSpec().InboundWebhookVerificationOrDefault() != promoterv1alpha1.InboundWebhookVerificationVerificationRequired {
			continue
		}
		verificationRequired = true

		_, secret, getErr := utils.GetScmProviderAndSecretFromGitRepository(ctx, wr.k8sClient, wr.controllerNamespace, gr)
		if getErr != nil {
			logger.Error(getErr, "could not resolve ScmProvider Secret for VerificationRequired provider",
				"namespace", gr.Namespace, "name", gr.Name)
			return http.StatusInternalServerError, "error verifying webhook"
		}

		secretBytes, headerName, ok := webhookSecretFromSecret(secret, provider)
		if !ok {
			logger.V(4).Info("VerificationRequired ScmProvider missing webhookSecret",
				"namespace", gr.Namespace, "gitRepository", gr.Name)
			return http.StatusUnauthorized, unauthorizedMessage
		}

		secretKey := secret.Namespace + "/" + secret.Name
		if _, seen := seenSecrets[secretKey]; seen {
			continue
		}
		seenSecrets[secretKey] = struct{}{}
		candidates = append(candidates, verificationCandidate{secret: secretBytes, header: headerName})
	}

	if !verificationRequired {
		return 0, ""
	}
	if len(candidates) == 0 {
		return http.StatusUnauthorized, unauthorizedMessage
	}

	for _, c := range candidates {
		headerValue := []byte(headers.Get(c.header))
		if verifyWebhookSignature(c.secret, headerValue, body) {
			return 0, ""
		}
	}

	logger.V(4).Info("webhook signature verification failed")
	return http.StatusUnauthorized, unauthorizedMessage
}

func (wr *WebhookReceiver) collectGitRepositoriesForVerification(ctx context.Context, provider, owner, name string, ctp *promoterv1alpha1.ChangeTransferPolicy) ([]promoterv1alpha1.GitRepository, error) {
	logger := log.FromContext(ctx)
	seen := map[string]struct{}{}
	var repos []promoterv1alpha1.GitRepository

	add := func(gr *promoterv1alpha1.GitRepository) {
		if gr == nil {
			return
		}
		key := gr.Namespace + "/" + gr.Name
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		repos = append(repos, *gr)
	}

	if owner != "" && name != "" {
		repoKey := utils.RepoKey(provider, owner, name)
		var list promoterv1alpha1.GitRepositoryList
		if err := wr.k8sClient.List(ctx, &list, client.MatchingFields{gitRepositoryRepoKeyField: repoKey}); err != nil {
			return nil, fmt.Errorf("list GitRepositories for webhook verification: %w", err)
		}
		for i := range list.Items {
			add(&list.Items[i])
		}
	}

	if ctp != nil && ctp.Spec.RepositoryReference.Name != "" {
		gr, err := utils.GetGitRepositoryFromObjectKey(ctx, wr.k8sClient, client.ObjectKey{
			Namespace: ctp.Namespace,
			Name:      ctp.Spec.RepositoryReference.Name,
		})
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				logger.V(4).Info("CTP gitRepositoryRef not found for webhook verification",
					"namespace", ctp.Namespace, "name", ctp.Spec.RepositoryReference.Name)
			} else {
				return nil, fmt.Errorf("get GitRepository for CTP webhook verification: %w", err)
			}
		} else {
			add(gr)
		}
	}

	return repos, nil
}

func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	c := make(http.Header, len(h))
	for k, vv := range h {
		vv2 := make([]string, len(vv))
		copy(vv2, vv)
		c[k] = vv2
	}
	return c
}
