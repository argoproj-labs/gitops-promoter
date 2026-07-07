import type { Environment, PromotionStrategy } from '../types/promotion';
import type { ChangeTransferPolicy, PromotionStrategyBundle } from '../types/bundle';

// Reconstruct the per-environment status the UI renders from ChangeTransferPolicies
// in a PromotionStrategyDetails bundle (one CTP per environment, keyed by activeBranch).
export function environmentsFromCTPs(
  ps: PromotionStrategy,
  ctps: ChangeTransferPolicy[],
): Environment[] {
  const byBranch = new Map<string, ChangeTransferPolicy>();
  for (const ctp of ctps) {
    const branch = ctp.spec?.activeBranch;
    if (branch) byBranch.set(branch, ctp);
  }

  const specEnvironments = ps.spec?.environments ?? [];
  return specEnvironments.map((env) => {
    const status = byBranch.get(env.branch)?.status ?? {};
    return {
      branch: env.branch,
      active: status.active ?? {},
      proposed: status.proposed ?? {},
      pullRequest: status.pullRequest,
      history: status.history,
    };
  });
}

function normalizeRepoWebURL(url: string): string {
  if (!url) return '';
  return url.replace(/\/$/, '').replace(/\.git$/, '');
}

type GitRepoSpec = Record<string, Record<string, string> | undefined>;
type ScmProviderSpec = Record<string, { domain?: string; organization?: string } | undefined>;

function gitRepoSpec(bundle: PromotionStrategyBundle): GitRepoSpec | undefined {
  return (bundle.gitRepository as { spec?: GitRepoSpec } | undefined)?.spec;
}

function scmSpec(bundle: PromotionStrategyBundle): ScmProviderSpec | undefined {
  return (
    (bundle.scmProvider as { spec?: ScmProviderSpec } | undefined)?.spec ??
    (bundle.clusterScmProvider as { spec?: ScmProviderSpec } | undefined)?.spec
  );
}

// Deployment-repo HTTPS base for hydrated commit links, derived from GitRepository + SCM provider.
export function repoURLFromBundle(bundle: PromotionStrategyBundle): string {
  const gr = gitRepoSpec(bundle);
  const spec = scmSpec(bundle);
  if (!gr) return '';

  const github = gr.github;
  if (github?.owner && github?.name) {
    const host = spec?.github?.domain || 'github.com';
    return normalizeRepoWebURL(`https://${host}/${github.owner}/${github.name}`);
  }

  const gitlab = gr.gitlab;
  if (gitlab?.namespace && gitlab?.name) {
    const host = spec?.gitlab?.domain || 'gitlab.com';
    return normalizeRepoWebURL(`https://${host}/${gitlab.namespace}/${gitlab.name}`);
  }

  const gitea = gr.gitea;
  if (gitea?.owner && gitea?.name && spec?.gitea?.domain) {
    return normalizeRepoWebURL(`https://${spec.gitea.domain}/${gitea.owner}/${gitea.name}`);
  }

  const forgejo = gr.forgejo;
  if (forgejo?.owner && forgejo?.name && spec?.forgejo?.domain) {
    return normalizeRepoWebURL(`https://${spec.forgejo.domain}/${forgejo.owner}/${forgejo.name}`);
  }

  const bitbucket = gr.bitbucketCloud;
  if (bitbucket?.owner && bitbucket?.name) {
    return normalizeRepoWebURL(`https://bitbucket.org/${bitbucket.owner}/${bitbucket.name}`);
  }

  const azure = gr.azureDevOps;
  if (azure?.project && azure?.name && spec?.azureDevOps) {
    const az = spec.azureDevOps;
    if (az.domain) {
      return normalizeRepoWebURL(
        `https://${az.domain}/${az.organization}/${azure.project}/_git/${azure.name}`,
      );
    }
    if (az.organization) {
      return normalizeRepoWebURL(
        `https://dev.azure.com/${az.organization}/${azure.project}/_git/${azure.name}`,
      );
    }
  }

  const fake = gr.fake;
  if (fake?.owner && fake?.name) {
    const host = spec?.fake?.domain || 'localhost';
    return normalizeRepoWebURL(`http://${host}/${fake.owner}/${fake.name}`);
  }

  return '';
}
