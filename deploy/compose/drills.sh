#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/../../scripts/lib-paths.sh"
resolve_urgentry_paths "$0"

usage() {
  cat <<'EOF'
usage: drills.sh [all|worker-redelivery|scheduler-handoff|role-restart|active-active|valkey-outage|backup-restore]

Commands:
  all                Run every serious self-hosted async runtime drill.
  worker-redelivery  Verify JetStream redelivery and backlog recovery.
  scheduler-handoff  Verify Valkey lease handoff and backfill scheduling.
  role-restart       Verify worker backlog recovery, rebuild conflict guards, and scheduler restart handoff on the split-role bundle.
  active-active      Verify multi-node API and query reads stay consistent across two live API nodes.
  valkey-outage      Verify serious-mode rate-limit and query-guard outage behavior.
  backup-restore     Verify full-environment backup capture and restore from the Compose bundle.
EOF
}

run_go_test() {
  local pattern="$1"
  shift
  (
    cd "$APP_DIR"
    go test "$@" -run "$pattern" -count=1
  )
}

worker_redelivery() {
  run_go_test 'TestJetStreamQueueRequeuesAndAcknowledges|TestJetStreamQueueSharesOneConsumerAcrossWorkers|TestPipeline_DurableWorkerSkipsDuplicateCompletedRedelivery|TestPipeline_JetStreamBacklogRecovery' \
    ./internal/runtimeasync ./internal/pipeline
}

scheduler_handoff() {
  run_go_test 'TestValkeyLeaseStoreHandsOffAfterExpiry|TestSchedulerRunOnceEnqueuesBackfillTick|TestBackfillControllerQueuedModeMarksJobDone' \
    ./internal/runtimeasync ./internal/pipeline
}

valkey_outage() {
  run_go_test 'TestValkeyRateLimiterSharesWindowAndFailsClosedOnOutage|TestValkeyQueryGuardFailsClosedOnOutage|TestValkeyQueryGuardSharesQuotaAndRecoversAfterKeyExpiry' \
    ./internal/auth ./internal/sqlite
}

backup_restore() {
  bash "$SCRIPT_DIR/backup-restore-drill.sh"
}

role_restart() {
  bash "$SCRIPT_DIR/role-restart-drill.sh"
}

active_active() {
  bash "$SCRIPT_DIR/active-active-drill.sh"
}

main() {
  local command="${1:-all}"
  case "$command" in
    all)
      worker_redelivery
      scheduler_handoff
      role_restart
      active_active
      valkey_outage
      backup_restore
      ;;
    worker-redelivery)
      worker_redelivery
      ;;
    scheduler-handoff)
      scheduler_handoff
      ;;
    role-restart)
      role_restart
      ;;
    active-active)
      active_active
      ;;
    valkey-outage)
      valkey_outage
      ;;
    backup-restore)
      backup_restore
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
