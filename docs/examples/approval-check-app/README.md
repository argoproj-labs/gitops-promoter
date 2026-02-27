# PR Approval Check (GitHub App)

Complete example: a **check** appears on every pull request with **Approve** and **Reject** buttons. Clicking **Approve** sets the check to **success**; clicking **Reject** sets it to **cancelled**.

This uses a **GitHub App** because only Apps can create check runs that trigger `requested_action` when a button is clicked (Actions-created checks do not).

## 1. Create the GitHub App

1. **GitHub** → **Settings** (your profile or org) → **Developer settings** → **GitHub Apps** → **New GitHub App**.
2. **Name:** e.g. `PR Approval Check`.
3. **Homepage URL:** any (e.g. your repo URL).
4. **Webhook:**  
   - **Active:** yes.  
   - **Webhook URL:** your server URL, e.g. `https://your-host.example.com/webhook`.  
   - **Webhook secret:** generate a random string and save it (e.g. `WEBHOOK_SECRET`).
5. **Permissions** → **Repository permissions:**
   - **Checks:** Read and write.
   - **Pull requests:** Read (optional; needed if you want to read PR body/title).
6. **Subscribe to events:** `Pull request`, `Check run`.
7. **Where can this app be installed?** Only on this account (or Any account).
8. Create the app. Note the **App ID**.
9. **Generate a private key** and download the `.pem` file. Keep it secret.

## 2. Install the App on your repo

- In the App’s **Install App**, choose the account/org and install it on the repo (or all repos) you want.

## 3. Run the server

- Clone this directory, then:

```bash
cd docs/examples/approval-check-app
npm install
```

- Set environment variables (use the values from step 1):

```bash
export APP_ID=123456
export WEBHOOK_SECRET=your_webhook_secret
export PRIVATE_KEY="$(cat /path/to/your-app.private-key.pem)"
# If you store the key in one line with \n: export PRIVATE_KEY='-----BEGIN RSA PRIVATE KEY-----\n...'
```

- Start the server:

```bash
npm start
```

- Expose the server to the internet (ngrok, your host’s URL, etc.) and use that URL as the **Webhook URL** in the App settings (e.g. `https://your-host.example.com/webhook`).

## 4. Optional: workflow for a “build” check

You can keep using a normal Actions workflow for a **build** check on the same PR. The App adds a separate **Deployment approval** check with the buttons. Example workflow (copy from `docs/examples/github-action-pr-with-approval-check.example.yaml` to `.github/workflows/build.yml`):

```yaml
name: Build

on:
  pull_request:
    types: [opened, synchronize, reopened]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: echo "Build..." && echo "Build done."
```

Result on the PR:

- **Build** – from this workflow (success when the job succeeds).
- **Deployment approval** – from the App (pending until someone clicks Approve or Reject; Approve → success, Reject → cancelled).

## Flow

1. A PR is opened or updated → GitHub sends `pull_request` to your webhook.
2. The App creates a check run **Deployment approval** with status **in_progress** and **Approve** / **Reject** buttons.
3. User clicks **Approve** → GitHub sends `check_run` with `action: requested_action`, `identifier: approve` → App updates the check to **completed**, **success**.
4. User clicks **Reject** → same with `identifier: reject` → App updates the check to **completed**, **cancelled**.

No extra workflow is required for the approval check; the App creates and updates it.
