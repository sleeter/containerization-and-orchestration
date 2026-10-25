#!/usr/bin/env bash
# Вносит в релиз shop одну из скрытых поломок сетевых политик. Какую именно, скрипт не говорит:
# выбор случайный, ответ лежит в scripts/.answer (base64) и открывается командой scripts/reveal.sh.
# Поломка применяется через values (helm upgrade), файл chart/shop/values.yaml не меняется;
# починить можно командой `helm upgrade shop chart/shop -n shop --reset-values`, когда найдёшь причину.
# Флаг --reset-values обязателен: в Helm 4 без него значения прошлой ревизии (с поломкой) остаются.
set -eu
cd "$(dirname "$0")/.."
NS="${NS:-shop}"

i=$((RANDOM % 4))
tmp="$(mktemp)"
case "$i" in
  0) name="неверная метка клиента в правиле api"
     printf 'networkPolicy:\n  apiClients:\n    - {app: loadgen-client}\n' > "$tmp" ;;
  1) name="неверный порт PostgreSQL в правилах"
     printf 'networkPolicy:\n  ports:\n    postgres: 5433\n' > "$tmp" ;;
  2) name="правило DNS не находит kube-dns"
     printf 'networkPolicy:\n  dns:\n    podLabels: {k8s-app: coredns}\n' > "$tmp" ;;
  3) name="порт api в правилах не совпадает с портом сервиса"
     printf 'networkPolicy:\n  ports:\n    api: 8081\n' > "$tmp" ;;
esac
printf '%s' "$name" | base64 > scripts/.answer

helm upgrade shop chart/shop -n "$NS" -f chart/shop/values.yaml -f "$tmp" >/dev/null
rm -f "$tmp"
# пересоздаём поды, чтобы они открыли новые соединения под новыми правилами
kubectl rollout restart deploy/api deploy/worker -n "$NS" >/dev/null
kubectl rollout status deploy/api -n "$NS" --timeout=120s >/dev/null
kubectl rollout status deploy/worker -n "$NS" --timeout=120s >/dev/null
echo "Поломка внесена. Что именно сломано, не говорим: ищи по симптомам и по Hubble."
