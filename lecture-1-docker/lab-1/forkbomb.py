# Fork-бомба для проверки pids.max (часть 3): форкается, пока не получит EAGAIN.
import os
import sys
import time

children = []
try:
    while True:
        pid = os.fork()
        if pid == 0:
            time.sleep(3600)
            os._exit(0)
        children.append(pid)
        time.sleep(0.02)  # пауза, чтобы успеть положить родителя в cgroup
except OSError as e:
    print(f"fork failed after {len(children)} children: {e}", file=sys.stderr)

time.sleep(3600)
