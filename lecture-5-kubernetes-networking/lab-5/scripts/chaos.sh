#!/usr/bin/env bash
# Непрерывный хаос: раз в INTERVAL секунд удаляет случайный под api или worker.
# Остановка: Ctrl+C. Журнал убийств пишется в chaos.log рядом со скриптом.
set -u

NS="${NS:-shop}"
INTERVAL="${INTERVAL:-5}"
LOG="$(dirname "$0")/chaos.log"

echo "chaos: namespace=$NS interval=${INTERVAL}s, Ctrl+C для остановки" | tee -a "$LOG"
trap 'echo "chaos: остановлен, убито подов: $kills" | tee -a "$LOG"; exit 0' INT TERM

kills=0
while true; do
  pod=$(kubectl get pods -n "$NS" -l 'app in (api,worker)' \
        --field-selector=status.phase=Running -o name | sort -R | head -1)
  if [ -n "$pod" ]; then
    node=$(kubectl get -n "$NS" "$pod" -o jsonpath='{.spec.nodeName}' 2>/dev/null)
    kubectl delete -n "$NS" "$pod" --wait=false >/dev/null 2>&1 && kills=$((kills+1))
    echo "$(date -u +%H:%M:%S) убит $pod (нода $node), всего $kills" | tee -a "$LOG"
  fi
  sleep "$INTERVAL"
done
