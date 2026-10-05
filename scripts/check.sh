#!/usr/bin/env bash
# 仅验证隔离测试库；任何失败立即退出。
set -euo pipefail
cd "$(dirname "$0")/.."
: "${ALERTOPS_TEST_DSN:?请设置本任务独占的 MySQL 测试库 DSN}"
(cd backend && go vet ./... && go test -race -tags=integration ./... -count=1 -timeout=180s)
(cd frontend && npm run build)
python3 -m py_compile runner/run.py runner/alertops_action.py runner/git_askpass.py cards/build_templates.py
