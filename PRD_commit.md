# PRD: Commit / Branch / Upstream Sync Plan

## Goal

Safely move the current large dirty working tree off `main`, split it into atomic commits, and prepare the repository for future upstream sync against the original repository.

This document is a **planning artifact only**. It does not assume anything has been committed yet.

---

## Current Situation

- Current branch: `main`
- Tracking remote: `origin -> https://github.com/kmss1258/CLIProxyAPI`
- No `upstream` remote configured yet
- Local tree contains many unrelated but connected changes across:
  - API key quota enforcement
  - SQLite prompt/response logging
  - management quota dashboard and same-port HTML
  - API key alias / policy management
  - concurrency controls and management APIs
  - docs / config / docker updates

### Files that should **not** be committed as product changes

These should be excluded from the commit plan unless explicitly needed later:

- `.cache/`
- `.playwright-mcp/`
- `.tmp_ulw/`
- `.tmp_weekly_quota/`

---

## High-Level Strategy

### 1. Get off `main` immediately

Do not continue working on `main`.

Recommended branch:

```bash
GIT_MASTER=1 git switch -c work/quota-concurrency-ui
```

### 2. Add the original repository as `upstream`

If the original repository is `router-for-me/CLIProxyAPI`, configure it as:

```bash
GIT_MASTER=1 git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
GIT_MASTER=1 git fetch upstream
```

### 3. Commit first, sync second

Because the working tree is large, the safer order is:

1. create working branch
2. exclude temp/unwanted paths
3. split work into atomic commits
4. fetch upstream
5. merge or rebase onto `upstream/main`

### 4. Prefer merge first, rebase later if needed

For a large long-lived patch stack, the safer first sync is:

```bash
GIT_MASTER=1 git fetch upstream
GIT_MASTER=1 git merge upstream/main
```

If later a cleaner history is desired, do a controlled rebase after the commit stack is organized.

---

## Commit Grouping Principles

This working tree is too large for a single commit.

### Mandatory rules

- keep implementation and tests together when directly paired
- do not mix docs-only changes into runtime commits unless the docs are inseparable from the feature
- keep dashboard/UI work separate from runtime quota enforcement when possible
- keep concurrency runtime changes separate from quota runtime changes when possible
- keep config/schema changes with the feature they enable

### Explicit non-commit scope

Before staging anything, ensure the temporary folders above stay out of the commit plan.

---

## Proposed Commit Plan

This is the recommended first-pass split based on the current file layout.

> Note: exact file placement may be adjusted slightly during staging, but the intent should remain the same.

### Commit 1 — Per-client quota runtime and middleware

**Intent:** introduce the core API key quota data model, store, and admission middleware.

**Primary files**

- `internal/quota/manager.go`
- `internal/quota/sqlite_store.go`
- `internal/quota/plugin.go`
- `internal/quota/manager_test.go`
- `internal/api/middleware/client_quota.go`
- `internal/api/middleware/client_quota_test.go`
- `internal/config/config.go`
- `internal/config/sdk_config.go`
- `internal/config/vertex_compat.go`
- `internal/config/client_api_key_policies_test.go`
- `config.example.yaml`

**Why together**

These files establish the quota feature’s runtime behavior and the config/schema needed to activate it.

**Suggested message direction**

- `Add client API key quota runtime enforcement`

---

### Commit 2 — SQLite prompt/response logging

**Intent:** add SQLite-backed request logging for prompts/responses and related wiring.

**Primary files**

- `internal/logging/sqlite_request_logger.go`
- `internal/logging/sqlite_request_logger_test.go`
- `go.mod`
- `go.sum`

**Possible companion files if directly touched for wiring**

- any minimal logging integration files if they are required to make the logger live

**Why together**

These files introduce one distinct capability: persisted SQLite request logging.

**Suggested message direction**

- `Add SQLite prompt and response request logging`

---

### Commit 3 — Management API for quota status and API key policy management

**Intent:** expose operator management surfaces for quota status, policy CRUD, aliasing, and usage visibility.

**Primary files**

- `internal/api/handlers/management/client_api_key_policies.go`
- `internal/api/handlers/management/client_api_key_policies_test.go`
- `internal/api/handlers/management/quota_access.go`
- `internal/api/handlers/management/quota_status.go`
- `internal/api/handlers/management/quota_status_test.go`
- `internal/api/handlers/management/handler.go`
- `internal/api/handlers/management/usage.go`
- `internal/api/handlers/management/usage_test.go`
- `internal/api/server.go`
- `internal/api/server_test.go`
- `internal/api/quota_page.go`

**Why together**

These files define the server-side management surface that the quota dashboard consumes.

**Suggested message direction**

- `Add management APIs for quota status and API key policies`

---

### Commit 4 — Quota dashboard / same-port HTML UI

**Intent:** add the operator-facing quota dashboard page and the API key management UX.

**Primary files**

- `internal/api/assets/quota.html`
- `internal/api/quota_page.go` *(only if not already fully consumed in commit 3; otherwise keep it with commit 3)*
- `internal/api/server.go` *(only if dashboard serving changes must stay with the page)*
- `internal/api/server_test.go` *(only the parts that validate dashboard HTML if split is cleanly possible)*

**Why together**

These files are the frontend/operator presentation layer for quota and API key management.

**Suggested message direction**

- `Add same-port quota dashboard and API key controls`

---

### Commit 5 — Concurrency runtime and management APIs

**Intent:** add request concurrency controls across global, endpoint, client-key, and upstream-account levels.

**Primary files**

- `internal/concurrency/manager.go`
- `internal/concurrency/manager_test.go`
- `internal/api/middleware/client_concurrency.go`
- `internal/api/middleware/client_concurrency_test.go`
- `internal/api/handlers/management/concurrency_config.go`
- `internal/api/handlers/management/concurrency_config_test.go`
- `internal/api/handlers/management/concurrency_status.go`
- `internal/api/handlers/management/concurrency_status_test.go`
- `internal/api/handlers/management/client_api_key_concurrency_policies.go`
- `internal/api/handlers/management/client_api_key_concurrency_policies_test.go`
- `internal/config/concurrency_config.go`
- `internal/config/concurrency_config_test.go`
- `sdk/cliproxy/auth/conductor.go`
- `internal/api/modules/amp/routes.go`
- `internal/api/modules/amp/routes_test.go`

**Why together**

These files are one feature family: concurrency control runtime + operator surfaces + route enforcement.

**Suggested message direction**

- `Add concurrency controls for client keys endpoints and upstream accounts`

---

### Commit 6 — Documentation / docker / operator guidance

**Intent:** document the new capabilities and update deployment/operator guidance.

**Primary files**

- `README.md`
- `README_CN.md`
- `README_JA.md`
- `docs/sdk-usage.md`
- `docker-compose.yml`
- `Dockerfile`
- `AGENTS.md`
- `.gitignore`

**Why together**

These files are operator/developer guidance and packaging updates rather than runtime feature logic.

**Suggested message direction**

- `Update docs and container setup for quota dashboard features`

---

## Staging Order

Recommended order for creating the branch history:

1. quota runtime + middleware
2. SQLite logging
3. management API
4. quota dashboard UI
5. concurrency runtime + APIs
6. docs / docker / guidance

This keeps lower-level runtime pieces below higher-level management UI and docs.

---

## Rebase / Sync Plan After Commit Split

Once the commits above exist on `work/quota-concurrency-ui`, use this flow.

### Safe first sync

```bash
GIT_MASTER=1 git fetch upstream
GIT_MASTER=1 git merge upstream/main
```

### Cleaner-history sync later

Use only after the branch is organized and conflicts are manageable:

```bash
GIT_MASTER=1 git fetch upstream
GIT_MASTER=1 git rebase upstream/main
```

### Why merge is recommended first here

- the patch stack is large
- the repository changes often upstream
- the current work started directly on `main`
- conflict recovery is easier with merge than with a large first rebase

---

## Practical Execution Checklist

### Before any commit

- [ ] create working branch from current dirty state
- [ ] ensure temp folders are ignored / not staged
- [ ] review `git diff --stat` per commit group

### Before upstream sync

- [ ] all local work split into atomic commits
- [ ] branch no longer dirty except intentional leftovers
- [ ] `upstream` remote added and fetched

### Before push

- [ ] verify tests/build for the final branch
- [ ] choose merge vs rebase consciously
- [ ] push branch, not `main`

---

## Suggested Commands (Sequence)

```bash
# 1. move off main
GIT_MASTER=1 git switch -c work/quota-concurrency-ui

# 2. add original upstream
GIT_MASTER=1 git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
GIT_MASTER=1 git fetch upstream

# 3. stage/commit by groups from this document
#    (do not include temp folders)

# 4. after commit split is complete
GIT_MASTER=1 git fetch upstream
GIT_MASTER=1 git merge upstream/main

# 5. later, if you want a cleaner branch history instead
# GIT_MASTER=1 git rebase upstream/main
```

---

## Decision

For the current repository state, the best path is:

1. branch off now
2. split commits first
3. add upstream
4. merge upstream/main first
5. only use rebase later if there is a strong reason to clean history further
