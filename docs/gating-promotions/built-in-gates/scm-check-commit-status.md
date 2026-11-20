# SCM Check Commit Status

The SCMCheckCommitStatus controller pulls check data from the PR open on the SCM and aggregates the check statuses to a
CommitStatus. This is useful for getting information about workflows that run inside the SCM visible inside Kubernetes.

## How it Works

For each environment that is configured to use this check, the controller will do the following.

1. Check if the Proposed Hydrated SHA is set for the environment.
2. Poll the SCM for checks on the PR that is open.
3. If any filters are set select which checks match.
4. Aggregated the checks into a single phase for the CommitStatus.
    - Any failures will set the phase to failure.
    - If there are no failures and any check is running the phase will be pending.
5. Create or Update the CommitStatus for the environment.

Even though some checks might be filtered out, all checks are visible inside of the Status of the SCMCheckCommitStatus.
For each entry for the SCM check in the status the following information is available about the check: it's name, status, raw status,
URL, and whether it was matched with the filters. The status is what is used in the aggregation to a single commit status being succeeded, failed, and
pending. The raw status is the status directly from the SCM so it will be more descriptive. The URL will link you to the check in the SCM.

## Supported SCMs

At the moment, all SCMs supported by the GitOps Promoter work with the SCMCheckCommitStatus. This list includes:

- GitHub
- GitLab
- Gitea
- Forgejo 
- Azure DevOps
- Bitbucket Cloud
- Bitbucket DataCenter

### Note on BitBucket DataCenter

BitBucket DataCenter's modern API does not allow for getting all checks on a commit. Because of this an endpoint that
returns a summary of all the check statuses is used instead to easily support both modern and legacy.

The behavior in creating the CommitStatus is still the same just there will only be that check that is retrieved from the SCM.
The check will be named "Bitbucket DataCenter Builds Combined Status." It's raw status will contain information about the number of
checks in each state.

## Filtering

Filtering checks pulled from the SCM is supported and done through the field `.spec.filters`. This field is a list of strings
where each string is an [expr](https://expr-lang.org/docs/getting-started) expression. These filters will determine which checks
are used in the aggregated phase. If a check's name matches then it is used. 

When writing the expr expressions, the name can be accessed by the variable `CheckName`. For example on how to filter checks,
see the examples section below.

## Refreshing Checks

Checks are refreshed when the SCMCheckCommitStatus reconciles. The default ControllerConfiguration requeues SCMCheckCommitStatus's every
five minutes. This time can be altered by altering the `.spec.scmCheckCommitStatus.workQueue.requeueDuration`. Do be warned that lowering the
time may hit rate limits on the SCM.

In the future, we would like refreshes to be able to be triggered through webhooks.

## Example Configurations

### Most Basic Usage

This example configuration is the most basic way to use this. It will pull the SCM Checks and create a CommitStatus based on the aggregated checks of
the status.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: SCMCheckCommitStatus
metadata:
  name: most-basic-example
spec:
  promotionStrategyRef:
    name: "promotion-strategy"
  key: "most-basic-example"
```

### Filtering Checks

The below example shows how to filter checks based on their name. The checks that match these filters are included within the aggregated checks.
In the example, checks that would match are one named "ci/lint" and any that contain e2e in its name.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: SCMCheckCommitStatus
metadata:
  name: filtering-checks
spec:
  promotionStrategyRef:
    name: "promotion-strategy"
  key: "filtering-checks"
  filters:
    - 'CheckName == "ci/lint"'
    - 'CheckName contains "e2e"'
```

### Publishing CommitStatus to the SCM

The below example will publish the phase of the child CommitStatus to the SCM.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: SCMCheckCommitStatus
metadata:
  name: publish-to-scm
spec:
  promotionStrategyRef:
    name: "promotion-strategy"
  key: "publish-to-scm"
  publishTo:
   - scm
```