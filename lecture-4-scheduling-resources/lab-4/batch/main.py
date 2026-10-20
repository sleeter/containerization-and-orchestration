import os
import time

memory_mib = int(os.getenv("MEMORY_MIB", "32"))
cpu_percent = int(os.getenv("CPU_PERCENT", "10"))

if memory_mib < 1 or not 0 <= cpu_percent <= 100:
    raise ValueError("MEMORY_MIB >= 1; CPU_PERCENT must be 0..100")

data = bytearray(memory_mib * 1024 * 1024)

# Записываем в каждую страницу, чтобы память действительно выделилась.
for offset in range(0, len(data), 4096):
    data[offset] = 1

print(f"Allocated {memory_mib} MiB; CPU duty cycle {cpu_percent}%", flush=True)

while True:
    start = time.monotonic()
    busy_until = start + cpu_percent / 100
    while time.monotonic() < busy_until:
        pass
    time.sleep(max(0, start + 1 - time.monotonic()))
