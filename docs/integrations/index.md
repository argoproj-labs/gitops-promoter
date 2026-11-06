# Integrations

GitOps Promoter promotes hydrated commits through environment branches. It does not render those
commits and it does not deploy them: a **hydrator** produces them from your DRY source, and an
**applier** (Argo CD, Terraform, or anything else) makes the active branch real and reports back
through a [CommitStatus](../gating-promotions/index.md).

The [`integrations/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/integrations)
directory of the repository holds community-maintained reference implementations of both halves.
Each one follows the [custom hydrator contract](../advanced-usage/custom-hydrator.md) and ships
with example workflows, Promoter manifests, and offline tests.

| Integration | What it promotes | Hydrator | Applier |
|---|---|---|---|
| [Terraform](terraform.md) | Terraform / OpenTofu root modules | Renders per-environment root modules with a committed `plan.txt` | Runs `terraform apply` on the active branch; the check run becomes the `terraform-apply` gate |

For Kubernetes manifests rendered by Argo CD's Source Hydrator, see
[Integrating with Argo CD](../integrating-with-argocd/index.md).

## Support

Integrations are maintained by the community and reviewed by the GitOps Promoter maintainers.
They are versioned with the repository but are not part of the controller release or its images.
Pin them to a commit SHA. To contribute one, follow the checklist in
[`integrations/README.md`](https://github.com/argoproj-labs/gitops-promoter/blob/main/integrations/README.md).
