// Package scmserver implements a test SCM server that impersonates the HTTP APIs of every SCM provider supported by
// GitOps Promoter: GitHub (Enterprise), GitLab, Azure DevOps, Bitbucket Cloud, Gitea and Forgejo.
//
// Each provider is served over HTTPS on its own listener so that the unmodified provider implementations in
// internal/scms can be pointed at it through the ScmProvider "domain" field. Repositories are real bare git
// repositories served over smart HTTP by git-http-backend; pull requests are merged with real git merges; pushes and
// merges emit provider-shaped webhooks. Provider behavior that matters to the controller is reproduced, including
// asynchronous pull request completion on Azure DevOps, mergeability checks, list endpoint lag, pagination, native
// error bodies and rate limits. A plain HTTP admin API controls the server from tests.
//
// The server is intended for the e2e suite and local development only. It is not hardened and must never be
// exposed outside a test environment.
package scmserver
