# Tekton Commit Status Controller

The TektonCommitStatus controller creates Tekton Tasks or pipelines and creates CommitStatus resources based on the results of that
Tekton resource. This allows you to run various checks on either the proposed or active commit or integrate with already existing
Tekton workflows.

## How It Works

When a new dry commit is detected, a Tekton PipelineRun or TaskRun will be created based on the configured resource reference in the
TektonCommitStatus. Once this Tekton resource is finished running the controller will evaluate the completion and the results provided from it
to decide whether to allow the promotion.

## Configuration

This section covers details about configuration of the resource.

### Tekton Resource Reference

Tekton resources that are referenced by their kind and name. The kind must be either Pipeline or Task and live in the same namespace
as the TektonCommitStatus. A service account can also be provided here. This service account is passed to the Task or Pipeline and
is used during the execution of it.

Parameters can also be passed into the created resource. Parameters must be specified with a name, value, and type (string, object, or array). If
the type of a parameter is an array or object they must be provided as a JSON formatted string. Templating is also supported for these and the following
two values can be provided.

- `.Branch` will inject the environments name into that parameter
- `.TargetedSha` will inject the SHA of the targeted commit that resource is running of off

The created Tekton resources can also be pruned when the checks pass. See below example configurations for how to do that.

### Modes

The TektonCommitStatus can work as both an active or proposed commit status. Which one it is is based on where the key is placed in the PromotionStrategy.
The `target` field controls what commit the Tekton resource is run against. This will give you the below modes.

#### Proposed Commit Status with a target of proposed

Gate the proposed commit against a Tekton resource on the new commit coming into the environment.

**Use Cases:**

- Security Scans
- Linting

#### Proposed Commit Status with a target of active

Gate the proposed commit against a Tekton resource on the currently deployed commit in the environment.

**Use Cases:**

- Check the current state of the running environment

#### Active Commit Status with a target of active

Gate downstream promotions against a Tekton resource on the currently deployed commit in the environment.

**Use Cases:**

- Smoke Tests
- Performance Tests
- Really any form of integration testing

#### Active Commit Status with a target of proposed

This is a not supported configuration and will error if configured.

### Results Evaluation

The TektonCommitStatus controller also evaluates results from Tekton resources. If provided and results do not match the given [expr](https://expr-lang.org/)
expression, the promotion will be blocked. Expressions can be written for individual results or generally with access to every result from the resource.

When writing expressions, the available variables depend on the expression type:

- General Expression
  - `Results` - a map in which the keys are each individual result.
- Individual Expressions
  - `Result` - The value of the result that was provided as the key.

If no expressions are provided, gating is done based on whether the Tekton resource was successful or not. If there is a missing expected result the
gate will fail.

## Example Configurations

### Basic Task - Proposed Commit Status

Below is a basic example of running a task on the proposed commit and gating on successful completion:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TektonCommitStatus
metadata:
  name: lint-check
spec:
  promotionStrategyRef:
    name: my-app-strategy
  key: lint
  target: proposed
  tektonResourceRef:
    kind: Task
    name: lint-task
    serviceAccount: default
```

### Security Scan with Parameters and Results

This example runs a security scan pipeline on the proposed commit, passes the commit SHA as a parameter, and evaluates the results to ensure no vulnerabilities are found:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TektonCommitStatus
metadata:
  name: security-scan
spec:
  promotionStrategyRef:
    name: my-app-strategy
  key: security-scan
  target: proposed
  tektonResourceRef:
    kind: Pipeline
    name: security-scan-pipeline
    serviceAccount: tekton-runner
    params:
      - name: gitUrl
        value: "https://github.com/myorg/myrepo/commit/{{ .TargetedSha }}"
        type: string
      - name: branch
        value: "{{ .Branch }}"
        type: string
  expectedResults:
    - key: vulnerabilities
      expression: "Result == '0'"
    - key: severity
      expression: "Result == 'none' || Result == 'low'"
  pruneRun: true
```

### Active Commit Status for Smoke Tests

This example runs smoke tests against the currently active commit in the environment. This gates downstream promotions:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TektonCommitStatus
metadata:
  name: smoke-tests
spec:
  promotionStrategyRef:
    name: my-app-strategy
  key: smoke-tests
  target: active
  tektonResourceRef:
    kind: Pipeline
    name: smoke-test-pipeline
    serviceAccount: test-runner
    params:
      - name: environment
        value: "{{ .Branch }}"
        type: string
      - name: testSuite
        value: "smoke"
        type: string
```

### Performance Test with Complex Results

This example runs performance tests and evaluates multiple results to ensure performance expectations are met:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TektonCommitStatus
metadata:
  name: performance-test
spec:
  promotionStrategyRef:
    name: my-app-strategy
  key: perf-test
  target: active
  tektonResourceRef:
    kind: Task
    name: performance-test-task
    serviceAccount: test-runner
    params:
      - name: targetUrl
        value: "https://{{ .Branch }}.example.com"
        type: string
      - name: duration
        value: "300"
        type: string
  expectedResults:
    - key: avgResponseTime
      expression: "Result < '200'"
    - key: errorRate
      expression: "Result < '0.01'"
```

### Task with Array and Object Parameters

This example shows passing complex parameter types (array and object) as JSON strings:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TektonCommitStatus
metadata:
  name: integration-test
spec:
  promotionStrategyRef:
    name: my-app-strategy
  key: integration-test
  target: proposed
  tektonResourceRef:
    kind: Task
    name: integration-test-task
    serviceAccount: test-runner
    params:
      - name: commit
        value: "{{ .TargetedSha }}"
        type: string
      - name: testEndpoints
        value: '["api/health", "api/users", "api/orders"]'
        type: array
      - name: testConfig
        value: '{"timeout": 30, "retries": 3, "parallel": true}'
        type: object
  pruneRun: true
```

### Evaluating All Results with General Expression

This example uses a general expression that evaluates all results together:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TektonCommitStatus
metadata:
  name: comprehensive-check
spec:
  promotionStrategyRef:
    name: my-app-strategy
  key: comprehensive-check
  target: proposed
  tektonResourceRef:
    kind: Pipeline
    name: comprehensive-check-pipeline
    serviceAccount: default
  expression: 'Results["tests-passed"] == true && float(Results["coverage"]) > 80 && float(Results["quality-score"]) > 7.5'
```

## Rerunning On A Failure

If a created Tekton resource does not progress the environment to successful, the Tekton resource must be deleted to rerun it. This is so the operators
have the opportunity to review what went wrong without spamming retries which may consume a lot of resources.
