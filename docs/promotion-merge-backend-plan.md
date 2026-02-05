# Backend plan: basic “merge proposed into active” for environment PR

This plan describes a minimal, working backend for merging a proposed PR into the active environment. It intentionally skips RBAC and other hardening for the first iteration.

## Goals (iteration 1)

- Provide a single backend endpoint to merge a promotion PR.
- Support GitHub at minimum (existing SCM integrations).
- Update the Promotion/PullRequest status with merge result (success or error).
- UI can call the endpoint and receive a concise response.

## Non-goals (iteration 1)

- RBAC / fine-grained authorization
- Multiple SCMs beyond the existing GitHub path
- Async job queues or retries
- Webhook-driven status refresh

## Proposed backend design (minimal)

### 1) Add an API endpoint

- **Endpoint:** POST `/api/v1/promotions/{namespace}/{name}/merge` (or `/api/v1/pullrequests/{namespace}/{name}/merge` if that maps better to existing CRs)
- **Payload:**
  - `provider` (optional, default from ScmProvider on the CR)
  - `pullRequestNumber`
  - `repoURL`
  - `baseBranch`
  - `headBranch`
  - `mergeMethod` (optional, default `merge`)

**Response:**
- `state: "merged" | "failed"`
- `message` (error text or summary)
- `mergedAt` (timestamp)

### 2) Minimal API handler flow

1. **Load the Promotion/PullRequest CR** by name/namespace.
2. **Resolve SCM provider config** (ScmProvider/ClusterScmProvider reference + Secret).
3. **Build SCM client** using existing `internal/scms/` code paths (GitHub implementation).
4. **Perform merge call** to SCM:
   - `MergePullRequest(repo, prNumber, mergeMethod)`
5. **Update status** on the CR:
   - `status.state = Merged/Failed`
   - `status.message = error summary`
   - `status.mergedAt = now` on success
6. **Return response** to UI.

### 3) SCM client interface (minimal)

Create or extend a small interface in `internal/scms/` to support merges if it does not already exist:

- `MergePullRequest(ctx, repo, prNumber, method) (mergeCommitSHA, error)`

Implementation for GitHub should use the existing GitHub client or add a minimal wrapper for the merge API.

### 4) Status update contract

- For iteration 1, reuse existing status fields if present (e.g., `PullRequest.status.state`).
- If no field exists, add a minimal `status.merge` sub-structure:
  - `state` (`Merged` | `Failed` | `Pending`)
  - `message`
  - `mergedAt`
  - `mergeCommitSHA`

### 5) Controller involvement (optional for v1)

- **Option A (simpler):** API handler directly updates status (no controller changes).
- **Option B (cleaner):** API handler annotates/specs a “merge requested” flag; controller performs merge and updates status.

For iteration 1, **Option A** is acceptable and lowest effort.

## Implementation steps

1. **Add API route + handler** in the webserver layer:
   - Parse request payload.
   - Fetch the CR.
   - Resolve SCM provider config + Secret.
2. **SCM client merge method**:
   - Add `MergePullRequest` to interface.
   - Implement GitHub merge using existing GitHub client helpers.
3. **Wire handler to SCM client**:
   - Call merge, capture errors, set status accordingly.
4. **Update CR status**:
   - On success, write `Merged` state and timestamps.
   - On error, write `Failed` state and error message.
5. **Return response** to UI.
6. **Add unit tests** for handler + SCM mock.

## Minimal testing checklist

- Merge succeeds and updates status.
- Merge fails and sets status to failed with message.
- Missing PR number or repo URL returns 400.
- Provider resolution fails returns 500 with details.

## Future enhancements

- RBAC enforcement per environment + project
- Async merge jobs + retries
- GitLab/Forgejo support
- Webhook-based status updates
- Audit events and metrics
