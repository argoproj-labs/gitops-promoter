# Debugging

This section covers operational debugging for GitOps Promoter.

- **[Finalizers](finalizers.md)** — What each finalizer does, [promotion history git notes](finalizers.md#promotion-history-git-notes), when it is safe to intervene, and how to report stuck finalizers.
- **[Git Trailers](git-trailers.md)** — What each promoter commit trailer records, where it is written, and [which values can differ between the git note and the commit message](git-trailers.md#note-versus-commit-message).
- **[Labels](labels.md)** — Promoter label keys and values, parent-gate labels on CommitStatus, and queries for troubleshooting gating.
