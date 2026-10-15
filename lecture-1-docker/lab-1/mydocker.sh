#!/bin/bash
# mydocker.sh — минимальный "Docker" на namespaces + cgroups + capabilities + seccomp
#
# Использование:
#   sudo ./mydocker.sh [--memory 100M] [--cpus "50000 100000"] [--pids 50] -- <command>
#
# Пример:
#   sudo ./mydocker.sh --memory 100M --cpus "50000 100000" --pids 50 -- /usr/local/bin/api
#
# Нужны: /usr/bin/setpriv и /usr/local/bin/seccomp-exec (сборка: seccomp-exec.c).

set -euo pipefail

MEMORY="100M"
CPU_QUOTA="50000 100000"   # 50 мс на каждые 100 мс = пол-ядра
PIDS_MAX="50"
CMD=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --memory) MEMORY="$2"; shift 2 ;;
        --cpus)   CPU_QUOTA="$2"; shift 2 ;;
        --pids)   PIDS_MAX="$2"; shift 2 ;;
        --)       shift; CMD=("$@"); break ;;
        *)        echo "Unknown option: $1" >&2
                  echo "Usage: sudo $0 [--memory 100M] [--cpus '50000 100000'] [--pids 50] -- <command>" >&2
                  exit 1 ;;
    esac
done

if [[ ${#CMD[@]} -eq 0 ]]; then
    echo "Usage: sudo $0 [--memory 100M] [--cpus '50000 100000'] [--pids 50] -- <command>" >&2
    exit 1
fi

if [[ $EUID -ne 0 ]]; then
    echo "This script must be run as root (use sudo)" >&2
    exit 1
fi

# 1. cgroup
CGROUP_NAME="mydocker-$$"
CGROUP_PATH="/sys/fs/cgroup/${CGROUP_NAME}"

echo "[mydocker] creating cgroup: ${CGROUP_PATH}"
mkdir -p "${CGROUP_PATH}"

if ! grep -q "memory" /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null; then
    echo "+cpu +memory +pids" > /sys/fs/cgroup/cgroup.subtree_control
fi

# 2. лимиты
echo "[mydocker] memory.max    = ${MEMORY}"
echo "${MEMORY}" > "${CGROUP_PATH}/memory.max"

echo "[mydocker] memory.swap.max = 0"
echo "0" > "${CGROUP_PATH}/memory.swap.max"

echo "[mydocker] cpu.max       = ${CPU_QUOTA}"
echo "${CPU_QUOTA}" > "${CGROUP_PATH}/cpu.max"

echo "[mydocker] pids.max      = ${PIDS_MAX}"
echo "${PIDS_MAX}" > "${CGROUP_PATH}/pids.max"

# 3. сам shell в cgroup до запуска: все потомки наследуют её
echo "[mydocker] adding shell (PID $$) to cgroup"
echo $$ > "${CGROUP_PATH}/cgroup.procs"

# 4. namespaces -> сброс capabilities -> seccomp -> exec
echo "[mydocker] namespaces: pid, mount, net, uts, ipc"
echo "[mydocker] capabilities: dropping all, no-new-privs; seccomp: mkdir/mkdirat blocked"
echo "[mydocker] exec: ${CMD[*]}"
echo "[mydocker] ---"

exec unshare \
    --pid --mount --net --uts --ipc \
    --fork \
    --mount-proc \
    -- \
    /usr/bin/setpriv \
        --bounding-set=-all \
        --inh-caps=-all \
        --no-new-privs \
        -- \
        /usr/local/bin/seccomp-exec "${CMD[@]}"
