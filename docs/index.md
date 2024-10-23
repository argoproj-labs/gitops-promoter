# Welcome to GitOps Promotions

GitOps Promoter is a tool designed to facilitate environment promotion in a GitOps fashion. It automates the process of promoting changes from one environment to the next by leveraging Git operations. The tool can help ensures that your environments are always in sync with the desired state defined in your Git repository when used with a CD tool like ArogCD.

## Key Features

- **Automated Environment Promotion**: Seamlessly promote changes across environments.
- **GitOps First**: Adheres to GitOps principles, ensuring that all changes are version-controlled.
- **YAML Diff Detection**: Detects changes in YAML files to determine if a pull request is required.
- **Integration with CI/CD**: Easily integrates with your existing CI/CD pipelines.
- **ArgoCD Integration**: Supports ArgoCD when managing Kubernetes resources.

## What is GitOps promotion.

GitOps promotion is the process of promoting changes across environments in a GitOps fashion. It involves using Git operations to manage the promotion of changes from one environment to the next. This ensures that all changes are version-controlled and that environments are always in sync with the desired state defined in your Git repository.
