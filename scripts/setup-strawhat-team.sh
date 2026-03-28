#!/usr/bin/env bash
# setup-strawhat-team.sh — Bootstrap the Straw Hat Pirates agent team
#
# Creates 5 predefined agents (Luffy, Zoro, Sanji, Nami, Chopper) with
# role-specific IDENTITY.md system prompts for VPS-based team orchestration.
#
# Usage:
#   export GOCLAW_URL="https://goclaw.example.com"
#   export GOCLAW_TOKEN="your-gateway-token"
#   bash scripts/setup-strawhat-team.sh
#   bash scripts/setup-strawhat-team.sh --delete   # tear down all agents
#
# Prerequisites:
#   - GoClaw running with at least 1 LLM provider configured
#   - curl and jq installed
#   - Gateway token or admin API key

set -euo pipefail

GOCLAW_URL="${GOCLAW_URL:?Set GOCLAW_URL (e.g., https://goclaw.example.com)}"
GOCLAW_TOKEN="${GOCLAW_TOKEN:?Set GOCLAW_TOKEN (gateway token or admin API key)}"

MODEL="${GOCLAW_MODEL:-claude-sonnet-4-20250514}"
TEAM_NAME="strawhat-pirates"

AGENT_KEYS=(luffy zoro sanji nami chopper)

# ── Colors ──
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[✗]${NC} $*"; }
step()  { echo -e "${CYAN}[→]${NC} $*"; }
header(){ echo -e "\n${BOLD}$*${NC}"; }

# ── Dependency check ──
for cmd in curl jq; do
    command -v "$cmd" >/dev/null 2>&1 || { error "$cmd is required but not installed"; exit 1; }
done

# ── API helper ──
api() {
    local method=$1 path=$2
    shift 2
    local response http_code
    response=$(curl -s -w "\n%{http_code}" -X "$method" \
        -H "Authorization: Bearer $GOCLAW_TOKEN" \
        -H "Content-Type: application/json" \
        "$GOCLAW_URL$path" \
        "$@") || { error "curl failed for $method $path"; return 1; }

    http_code=$(echo "$response" | tail -n1)
    local body
    body=$(echo "$response" | sed '$d')

    if [[ "$http_code" -ge 200 && "$http_code" -lt 300 ]]; then
        echo "$body"
        return 0
    elif [[ "$http_code" == "409" ]]; then
        # Conflict — already exists
        echo "$body"
        return 2
    else
        error "API $method $path returned HTTP $http_code"
        echo "$body" | jq . 2>/dev/null || echo "$body"
        return 1
    fi
}

# ── Get agent by key (returns UUID or empty) ──
get_agent_id() {
    local key=$1
    local resp
    resp=$(api GET "/v1/agents/$key" 2>/dev/null) || return 1
    echo "$resp" | jq -r '.id // empty'
}

# ── Delete mode ──
delete_all() {
    header "🗑️  Tearing down Straw Hat agents..."
    local any_deleted=false
    for key in "${AGENT_KEYS[@]}"; do
        local agent_id
        agent_id=$(get_agent_id "$key" 2>/dev/null) || true
        if [[ -n "$agent_id" ]]; then
            step "Deleting $key ($agent_id)..."
            api DELETE "/v1/agents/$agent_id" >/dev/null && info "Deleted $key" || warn "Failed to delete $key"
            any_deleted=true
        else
            warn "$key not found, skipping"
        fi
    done
    if $any_deleted; then
        info "Teardown complete"
    else
        warn "No agents found to delete"
    fi
    exit 0
}

if [[ "${1:-}" == "--delete" ]]; then
    delete_all
fi

# ── IDENTITY.md content per agent ──

read -r -d '' IDENTITY_LUFFY << 'LUFFY_EOF' || true
# Luffy — Team Lead / Router / Summarizer

You are the captain of the Straw Hat team. Your job is to receive requests,
decompose them into tasks, route them to the right crew member, and track
progress to completion.

## Routing Protocol

1. **Always create/search native team tasks first** before starting work.
2. For **coding or test tasks**, route to the responsible agent with metadata:
   - `execution_target: windows-local`
   - `repo_key: goclaw`
3. **Never tell VPS agents to edit product code directly** — they produce
   execution briefs; the Windows worker applies changes.

## Task Tracking

- Track progress milestones at **25% / 50% / 75%** completion.
- Summarize results when all subtasks complete.
- Aggregate outputs from crew members into a coherent final report.

## Failure Handling

- **3-strike circuit breaker**: after 3 failures on the same task, stop
  auto-retrying and escalate to the user with a summary of attempts.
- **Circular dependency detection**: if task A blocks B blocks A, flag it
  immediately and propose a resolution.
- **Worker offline**: if a crew member hasn't responded within their timeout
  window, explicitly reassign the task to another capable member or report
  the blockage.

## Crew Roster

| Agent   | Role                          | Routes to for                    |
|---------|-------------------------------|----------------------------------|
| Zoro    | Backend debug & implementation| Go code, API, DB, agent loop     |
| Sanji   | Frontend / UX                 | React, TypeScript, UI components |
| Nami    | Research & planning           | Specs, architecture, analysis    |
| Chopper | QA & testing                  | Test plans, validation, review   |
LUFFY_EOF

read -r -d '' IDENTITY_ZORO << 'ZORO_EOF' || true
# Zoro — Backend Debug & Implementation

You are the backend specialist. You analyze requests, diagnose issues, and
produce detailed execution briefs for the Windows worker to apply.

## Operating Rules

1. **Analyze** the request: identify root cause, affected files, and fix strategy.
2. **Produce an execution brief** with:
   - Affected file paths (relative to repo root)
   - Exact changes (diffs, new code, or clear instructions)
   - Build/test commands to verify the fix
3. **Review returned diffs/results** from the worker and decide:
   - ✅ Complete — mark task done
   - 🔄 Needs revision — provide specific feedback
   - 🚫 Blocked — identify the dependency and report to Luffy
4. **No direct shell/file access** to the product repo from VPS.

## Timeout Expectations

| Task type    | Timeout |
|-------------|---------|
| Debug       | 900s    |
| Implement   | 1800s   |

## Status Reporting

Distinguish clearly between:
- `task_failed` — the work was attempted but the result is incorrect
- `worker_offline` — no response within timeout window
- `blocked_by_dependency` — cannot proceed until another task completes
ZORO_EOF

read -r -d '' IDENTITY_SANJI << 'SANJI_EOF' || true
# Sanji — Frontend / UX Implementation

You are the frontend specialist. You prepare UI implementation briefs
for the Windows worker to execute against the local repo.

## Operating Rules

1. **Prepare UI implementation briefs** specifying:
   - Affected component files (paths relative to `ui/web/src/`)
   - Exact JSX/TSX changes, new components, or style modifications
   - Required imports and dependency additions (if any)
   - Acceptance criteria: what the UI should look/behave like after changes
2. **List all affected files** explicitly — no "and similar files" hand-waving.
3. **Require verification commands** in every brief:
   - `pnpm typecheck` must pass
   - `pnpm build` must succeed
   - Visual verification steps (what to check in browser)
4. Follow the project's mobile UI/UX rules (h-dvh, text-base for inputs,
   safe areas, 44px touch targets).

## Timeout Expectations

| Task type    | Timeout |
|-------------|---------|
| Implement   | 1800s   |
SANJI_EOF

read -r -d '' IDENTITY_NAMI << 'NAMI_EOF' || true
# Nami — Research & Planning

You are the research and planning specialist. You investigate problems,
evaluate approaches, and produce implementation-ready specs.

## Operating Rules

1. **Write specs to VPS team workspace ONLY** — never edit product code.
2. **No product-code edits** under any circumstances.
3. Produce **implementation-ready briefs** that Zoro (backend) or Sanji
   (frontend) can execute without further research:
   - Clear problem statement
   - Evaluated alternatives with trade-offs
   - Recommended approach with rationale
   - Step-by-step implementation plan
   - Acceptance criteria
4. **Check dependency chain** before starting research that depends on
   output from another task — don't duplicate work or build on stale data.

## Output Format

Specs should be markdown with YAML frontmatter:
```yaml
---
title: "Feature/Fix Title"
status: draft | ready | approved
targets: [zoro, sanji]  # who executes this
depends_on: []           # task IDs this blocks on
---
```
NAMI_EOF

read -r -d '' IDENTITY_CHOPPER << 'CHOPPER_EOF' || true
# Chopper — QA & Testing

You are the QA and validation specialist. You define what "done" looks like,
write verification plans, and judge pass/fail on returned results.

## Operating Rules

1. **Define verification commands** for the Windows worker to execute:
   - Go: `go build ./...`, `go vet ./...`, `go test -race ./tests/integration/`
   - Frontend: `pnpm typecheck`, `pnpm build`, `pnpm test`
   - SQLite: `go build -tags sqliteonly ./...`
2. **Review returned build/test output** and post a verdict:
   - ✅ **PASS** — all checks green, acceptance criteria met
   - ❌ **FAIL** — specify which checks failed and why
   - ⏱️ **TIMEOUT** — worker didn't respond within window
   - Include rationale for every verdict.
3. **Regression awareness**: check if the fix/feature might break other
   areas based on the dependency graph.

## Timeout Expectations

| Task type | Timeout |
|-----------|---------|
| Test      | 900s    |
| Review    | 600s    |
CHOPPER_EOF

# ── Agent definitions (JSON payloads) ──

agent_payload() {
    local key=$1 display=$2 emoji=$3 description=$4
    jq -n \
        --arg key "$key" \
        --arg display "$display" \
        --arg model "$MODEL" \
        --arg emoji "$emoji" \
        --arg desc "$description" \
        '{
            agent_key: $key,
            display_name: $display,
            agent_type: "predefined",
            model: $model,
            frontmatter: ("---\nrole: " + $key + "\nteam: strawhat-pirates\nexecution_target: vps\n---"),
            other_config: {
                description: $desc,
                emoji: $emoji
            }
        }'
}

# ── Create a single agent ──
create_agent() {
    local key=$1 display=$2 emoji=$3 description=$4 identity=$5

    step "Creating agent: $emoji $display ($key)..."

    # Check if already exists
    local existing_id
    existing_id=$(get_agent_id "$key" 2>/dev/null) || true

    if [[ -n "$existing_id" ]]; then
        warn "$key already exists ($existing_id), skipping creation"
        echo "$existing_id"
        return 0
    fi

    local payload
    payload=$(agent_payload "$key" "$display" "$emoji" "$description")

    local resp
    resp=$(api POST "/v1/agents" -d "$payload") || {
        # Check for conflict (idempotent)
        if [[ $? -eq 2 ]]; then
            warn "$key already exists (conflict), fetching ID..."
            existing_id=$(get_agent_id "$key")
            echo "$existing_id"
            return 0
        fi
        error "Failed to create agent $key"
        return 1
    }

    local agent_id
    agent_id=$(echo "$resp" | jq -r '.id')
    info "Created $key → $agent_id"
    echo "$agent_id"
}

# ── Set IDENTITY.md via SQL (psql) or skip with instructions ──
set_identity() {
    local agent_id=$1 key=$2 content=$3

    if [[ -z "$agent_id" ]]; then
        warn "No agent ID for $key, skipping IDENTITY.md"
        return 0
    fi

    step "Setting IDENTITY.md for $key..."

    # agent_context_files is seeded by the API on creation (SOUL.md, IDENTITY.md).
    # We update via psql if DATABASE_URL is available, otherwise print instructions.
    if [[ -n "${DATABASE_URL:-}" ]]; then
        # Escape single quotes for SQL
        local escaped
        escaped=$(echo "$content" | sed "s/'/''/g")
        psql "$DATABASE_URL" -q -c "
            INSERT INTO agent_context_files (id, agent_id, file_name, content, updated_at, tenant_id)
            SELECT gen_random_uuid(), '$agent_id', 'IDENTITY.md', E'$escaped', NOW(), tenant_id
            FROM agents WHERE id = '$agent_id'
            ON CONFLICT (agent_id, file_name) DO UPDATE SET content = EXCLUDED.content, updated_at = NOW();
        " && info "IDENTITY.md set for $key" || warn "SQL failed for $key — set manually via dashboard"
    else
        # No DB access — write to temp file for reference
        local tmpdir="${TMPDIR:-/tmp}/strawhat-identities"
        mkdir -p "$tmpdir"
        echo "$content" > "$tmpdir/${key}-IDENTITY.md"
        warn "No DATABASE_URL — IDENTITY.md saved to $tmpdir/${key}-IDENTITY.md"
        warn "Set via dashboard: Agents → $key → IDENTITY.md"
    fi
}

# ══════════════════════════════════════════════════════════════════════
#  Main
# ══════════════════════════════════════════════════════════════════════

header "🏴‍☠️  Setting up Straw Hat Pirates team"
echo "   URL:   $GOCLAW_URL"
echo "   Model: $MODEL"
echo ""

# Verify connectivity
step "Checking GoClaw connectivity..."
api GET "/v1/agents" >/dev/null || { error "Cannot reach GoClaw API at $GOCLAW_URL"; exit 1; }
info "Connected to GoClaw"

# ── Create agents ──

declare -A AGENT_IDS

AGENT_IDS[luffy]=$(create_agent \
    "luffy" "Luffy" "🏴‍☠️" \
    "Team lead, task router, and summarizer. Decomposes requests, routes to crew, tracks milestones, handles failures with circuit breaker." \
    "$IDENTITY_LUFFY")

AGENT_IDS[zoro]=$(create_agent \
    "zoro" "Zoro" "⚔️" \
    "Backend debug and implementation brief owner. Analyzes Go code issues, produces execution briefs, reviews diffs." \
    "$IDENTITY_ZORO")

AGENT_IDS[sanji]=$(create_agent \
    "sanji" "Sanji" "🍳" \
    "Frontend and UX implementation brief owner. Prepares React/TypeScript UI briefs with acceptance criteria." \
    "$IDENTITY_SANJI")

AGENT_IDS[nami]=$(create_agent \
    "nami" "Nami" "🗺️" \
    "Research, planning, and spec author. Investigates problems, evaluates approaches, writes implementation-ready specs." \
    "$IDENTITY_NAMI")

AGENT_IDS[chopper]=$(create_agent \
    "chopper" "Chopper" "🩺" \
    "QA, validation, and test brief owner. Defines verification commands, reviews build/test output, posts pass/fail verdicts." \
    "$IDENTITY_CHOPPER")

echo ""

# ── Set IDENTITY.md files ──

header "📝 Setting IDENTITY.md context files"

set_identity "${AGENT_IDS[luffy]}"  "luffy"  "$IDENTITY_LUFFY"
set_identity "${AGENT_IDS[zoro]}"   "zoro"   "$IDENTITY_ZORO"
set_identity "${AGENT_IDS[sanji]}"  "sanji"  "$IDENTITY_SANJI"
set_identity "${AGENT_IDS[nami]}"   "nami"   "$IDENTITY_NAMI"
set_identity "${AGENT_IDS[chopper]}" "chopper" "$IDENTITY_CHOPPER"

echo ""

# ── Team creation instructions ──

header "👥 Team Setup"
echo ""
echo "Team creation is done via the GoClaw dashboard or WebSocket RPC."
echo ""
echo "Option 1 — Dashboard:"
echo "  1. Open ${GOCLAW_URL} → Teams → Create Team"
echo "  2. Team name: ${TEAM_NAME}"
echo "  3. Lead: Luffy (${AGENT_IDS[luffy]:-?})"
echo "  4. Add members: Zoro, Sanji, Nami, Chopper"
echo ""

if [[ -n "${DATABASE_URL:-}" ]]; then
    echo "Option 2 — SQL (since DATABASE_URL is set):"
    echo ""
    # Get the tenant_id from one of the created agents
    TENANT_ID=$(psql "$DATABASE_URL" -tAc "SELECT tenant_id FROM agents WHERE agent_key = 'luffy' LIMIT 1" 2>/dev/null || echo "")
    if [[ -n "$TENANT_ID" ]]; then
        cat << SQL_EOF
  psql "\$DATABASE_URL" -c "
    INSERT INTO teams (id, tenant_id, name, display_name, lead_agent_id, status, created_at, updated_at)
    VALUES (
      gen_random_uuid(),
      '${TENANT_ID}',
      '${TEAM_NAME}',
      'Straw Hat Pirates',
      '${AGENT_IDS[luffy]:-}',
      'active',
      NOW(), NOW()
    ) ON CONFLICT (tenant_id, name) DO NOTHING;
  "

SQL_EOF
        for key in zoro sanji nami chopper; do
            echo "  -- Add $key as member"
            cat << SQL_MEMBER
  psql "\$DATABASE_URL" -c "
    INSERT INTO team_members (id, team_id, agent_id, role, created_at)
    SELECT gen_random_uuid(), t.id, '${AGENT_IDS[$key]:-}', 'member', NOW()
    FROM teams t WHERE t.name = '${TEAM_NAME}' AND t.tenant_id = '${TENANT_ID}'
    ON CONFLICT DO NOTHING;
  "

SQL_MEMBER
        done
    fi
fi

# ── Summary ──

header "✅ Setup Summary"
echo ""
printf "  %-10s %-8s %s\n" "Agent" "Emoji" "ID"
printf "  %-10s %-8s %s\n" "─────" "─────" "──────────────────────────────────────"
for key in "${AGENT_KEYS[@]}"; do
    local_emoji=""
    case "$key" in
        luffy)  local_emoji="🏴‍☠️" ;;
        zoro)   local_emoji="⚔️" ;;
        sanji)  local_emoji="🍳" ;;
        nami)   local_emoji="🗺️" ;;
        chopper) local_emoji="🩺" ;;
    esac
    printf "  %-10s %-8s %s\n" "$key" "$local_emoji" "${AGENT_IDS[$key]:-not created}"
done
echo ""
info "Straw Hat Pirates crew is ready! 🏴‍☠️"
