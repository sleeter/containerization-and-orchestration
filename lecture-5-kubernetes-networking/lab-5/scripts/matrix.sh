#!/usr/bin/env bash
# Матрица доступа "ожидаемо / фактически".
# Источники: loadgen (клиент), api, worker и посторонний под net-a.
# Из api/worker/loadgen нет оболочки (образ scratch), поэтому подключаемся эфемерным
# контейнером busybox в тот же под: он делит сеть пода, и политика видит ровно его метки.
set -u
NS="${NS:-shop}"
IMG="${IMG:-busybox:1.36}"

SOURCES="loadgen api worker stranger"
DESTS="api worker postgres dns internet"

# ожидаемый результат: 1 - должно проходить, 0 - не должно
expected() {
  case "$2:$1" in
    api:loadgen) echo 1 ;;
    postgres:api|postgres:worker) echo 1 ;;
    dns:*) echo 1 ;;
    internet:stranger) echo 1 ;;
    *) echo 0 ;;
  esac
}

# хост api/worker подставляется отдельно (см. target_host): если источник сам является подом
# api или worker, запрос по Service мог бы попасть в него же, а трафик пода к самому себе
# политика не режет, и результат был бы случайным
CHECKS='
api|nc -z -w 2 @API@ 8080
worker|nc -z -w 2 @WORKER@ 8080
postgres|nc -z -w 2 shop-db-rw.shop.svc.cluster.local 5432
dns|nslookup kubernetes.default.svc.cluster.local
internet|nc -z -w 2 1.1.1.1 443
'

# для своего приложения берём IP другого пода, для чужого достаточно имени Service
target_host() {
  local src="$1" app="$2" me="$3"
  if [ "$src" = "$app" ]; then
    kubectl get pod -n "$NS" -l "app=$app" --field-selector=status.phase=Running \
      -o jsonpath="{range .items[?(@.metadata.name!='$me')]}{.status.podIP}{\"\\n\"}{end}" | head -1
  else
    echo "$app.shop.svc.cluster.local"
  fi
}

# скрипт, который печатает "<имя>=OK|FAIL" по каждой проверке
build_script() {
  local api_host="$1" worker_host="$2"
  echo "$CHECKS" | sed -e "s/@API@/$api_host/" -e "s/@WORKER@/$worker_host/" | while IFS='|' read -r name cmd; do
    [ -z "$name" ] && continue
    echo "if $cmd >/dev/null 2>&1; then echo $name=OK; else echo $name=FAIL; fi"
  done
}

run_from() {
  local src="$1" script pod out try api_host worker_host
  if [ "$src" = "stranger" ]; then pod=net-a; else
    pod=$(kubectl get pod -n "$NS" -l "app=$src" --field-selector=status.phase=Running \
          -o jsonpath='{.items[0].metadata.name}')
  fi
  api_host=$(target_host "$src" api "$pod")
  worker_host=$(target_host "$src" worker "$pod")
  # sleep в начале: kubectl debug присоединяется к контейнеру не мгновенно, и вывод
  # слишком быстрой команды теряется (в матрице это выглядело как "no-data")
  script="sleep 2; $(build_script "$api_host" "$worker_host")"
  for try in 1 2 3; do
    if [ "$src" = "stranger" ]; then
      out=$(kubectl exec -n "$NS" net-a -- sh -c "$script" 2>/dev/null)
    else
      out=$(kubectl debug -n "$NS" "$pod" -q -i --profile=general --image="$IMG" --target="$src" \
            -- sh -c "$script" 2>/dev/null)
    fi
    [ -n "$out" ] && break
  done
  echo "$out"
}

bad=0
# клетка: pass - прошло как и ожидалось, block - заблокировано как и ожидалось,
#         !pass - прошло, а не должно было, !block - заблокировано, а должно было пройти
echo "pass = разрешено и работает | block = запрещено и не проходит | !pass / !block = РАСХОЖДЕНИЕ"
echo
printf '%-12s' "from\\to"; for d in $DESTS; do printf '%-11s' "$d"; done; echo
for s in $SOURCES; do
  out="$(run_from "$s")"
  printf '%-12s' "$s"
  for d in $DESTS; do
    got=$(echo "$out" | grep "^$d=" | head -1 | cut -d= -f2)
    exp=$(expected "$s" "$d")
    [ "$got" = "OK" ] && g=1 || g=0
    if [ -z "$got" ]; then cell="no-data"; bad=$((bad+1))
    elif [ "$g" = "$exp" ]; then
      [ "$g" = 1 ] && cell="pass" || cell="block"
    else
      [ "$g" = 1 ] && cell="!pass" || cell="!block"
      bad=$((bad+1))
    fi
    printf '%-11s' "$cell"
  done
  echo
done
echo
[ "$bad" -eq 0 ] && echo "МАТРИЦА ЗЕЛЁНАЯ: фактическое совпало с ожидаемым везде" || echo "РАСХОЖДЕНИЙ: $bad"
exit "$bad"
