#!/usr/bin/env bash
# Показывает, какую поломку внёс scripts/break.sh. Запускай только после своей диагностики.
cd "$(dirname "$0")"
[ -f .answer ] && { printf 'Была внесена поломка: '; base64 -d < .answer; echo; } || echo "поломка ещё не вносилась"
