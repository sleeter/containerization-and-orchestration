# Лабораторная работа 2 — Мониторинг сервиса

В этой лабе я разбирался с мониторингом в Kubernetes. Поднимал для своего Go-сервиса Prometheus, Grafana, Loki, Jaeger и алерты. Хотелось не только установить все эти штуки, но и проверить, что они вообще работают вместе. Вообще круто уметь такое делать и полезно, когда можно проблему на графике увидеть, найти ошибку в логах и посмотреть, что происходило с запросом 

Язык сервиса: **Go**.

Окружение:

- хост — macOS (arm64), кластер — minikube v1.37.0 (Docker-драйвер), Kubernetes v1.34.0, **2 CPU и 4000 МБ памяти** у ноды: хотел 4 CPU и 6144 МБ, но minikube флаги проигнорировал (почему — ниже, в части 0);
- Docker Desktop выдаёт виртуалке около 7,6 ГБ (настройки по умолчанию, лимит я не менял);
- Helm 4.3.1, чарты: `kube-prometheus-stack` 92.9.0 (Prometheus v3.15.0), `grafana/loki` 7.3.0, `grafana/alloy` 1.13.1, `jaegertracing/jaeger` 4.16.1 (Jaeger v2), `wiremind/karma` 2.13.1;
- все конфиги лежат в репозитории: сервис в [api/](./api/), чарт сервиса в [chart/api/](./chart/api/), values и манифесты стека в [monitoring/](./monitoring/).

---

## Часть 0. Сервис и кластер

Сервис сгенерен, код лежит в [api/main.go](./api/main.go). Ручки: `/health` отдаёт `ok`, `/fail` возвращает 500 и увеличивает счётчик ошибок, `/slow` спит от одной до трёх секунд, `/load?n=N` делает N запросов к самому себе, `/metrics` отдаёт Prometheus.

Что внутри:

- метрики RED: `http_requests_total{method,path,status}`, `http_request_errors_total{path}` и гистограмма `http_request_duration_seconds{path}`;
- логи через `slog` в JSON, в каждой строке запроса есть `trace_id` текущего спана;
- OpenTelemetry: `otelhttp` создаёт корневой спан на запрос, в `/slow` медленная часть обёрнута в вложенный спан `slow-op`, в `/fail` спан получает статус error. Адрес экспортёра берётся из `OTEL_EXPORTER_OTLP_ENDPOINT`;
- `/metrics` вынесен из-под `otelhttp`, иначе скрейп Prometheus каждые 15 секунд засорял бы Jaeger пустыми трейсами.

Версии OTel пришлось закрепить на 1.24: локальный Go стоит 1.21, а более новые версии библиотеки его уже не принимают. Dockerfile многостадийный (`golang:1.21-alpine` → `scratch`), зависимости скачиваются отдельным слоем, чтобы правка `main.go` не тянула их заново.

### Кластер

Перед стартом я заглянул в остальные лабы, узнал, что стек из этой лабы переиспользуется в 3–6, а там добавятся ещё оператор БД, CNI и service mesh. Docker Desktop выдаёт виртуалке около 7,6 ГБ, и под стек с запасом я решил взять 4 CPU и 6144 МБ:

```
minikube start --cpus=4 --memory=6144
minikube image build -t api:0.1 .
```

<img src="./pics/minikube-start.png" alt="" width="700"/>

Но на скрине видно, что получилось не то
Оказалось, что профиль `minikube` уже существовал и просто так поменять ему ресурсы нельзя. Нода пересоздалась, но со старыми настройками, то есть с 2 CPU и 4000 МБ. Проверил через `docker inspect minikube`, там всё совпало. Для новых лимитов пришлось бы сначала удалять кластер через `minikube delete`, но я решил оставить как есть. В итоге всего хватило.

Образ API собирал через `minikube image build`, потому что обычный `docker build` складывает образ в Docker хоста, а внутри minikube своё хранилище. Чтобы Kubernetes не пытался скачать несуществующий образ из реестра, в Deployment оставил `imagePullPolicy: IfNotPresent`.

## Часть 1. Метрики (Prometheus + Grafana)

Начал с метрик. Поставил `kube-prometheus-stack`, там уже есть Prometheus, Grafana, Alertmanager, оператор и экспортеры. Конфигурация лежит в [monitoring/values-prometheus.yaml](./monitoring/values-prometheus.yaml).

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install kps prometheus-community/kube-prometheus-stack -n monitoring --create-namespace -f monitoring/values-prometheus.yaml
```

С первого раза установка, конечно, не прошла. Helm жаловался на `no such file or directory`. Я просто запускал команду не из той папки, поэтому он не мог найти values-файл. Перешёл в `lab-2` и всё заработало.

Для самого API сделал Helm-чарт [chart/api](./chart/api/) с двумя репликами, Service и ServiceMonitor. Последний нужен, чтобы Prometheus находил метрики сервиса.

```yaml
spec:
  selector:
    matchLabels: {app: api}
  endpoints:
    - port: http
      interval: 15s
```

Здесь `http` это имя порта в Service, а не номер. В Prometheus открыл Targets и увидел два `/metrics`, по одному на каждый под. Оба были в состоянии `UP`, значит, метрики собираются.

<img src="./pics/targets.png" alt="" width="700"/>

Prometheus ходит к каждому поду отдельно, а не к общему ClusterIP, поэтому метрики разных реплик не смешиваются.

С ServiceMonitor тоже пришлось немного повозиться. По умолчанию оператор искал объекты с лейблом `release: kps`, а у моего такого лейбла не было. Самое неприятное, что никакой явной ошибки при этом нет, просто нужная цель не появляется. В values добавил настройки, чтобы он не ограничивался лейблом релиза.

```yaml
prometheus:
  prometheusSpec:
    serviceMonitorSelectorNilUsesHelmValues: false
    ruleSelectorNilUsesHelmValues: false
```

После этого `ServiceMonitor` начал подхватываться. Заодно выяснил, что у Prometheus отдельно выбираются namespace с самими ServiceMonitor, а у ServiceMonitor отдельно задаётся, где искать Service.

### Дашборд

Дальше собрал RED-дашборд в Grafana, чтобы смотреть количество запросов, ошибки и задержки. JSON сохранил в [monitoring/dashboards/red.json](./monitoring/dashboards/red.json). Grafana подхватывает его через ConfigMap с лейблом `grafana_dashboard=1`. Сначала создал панели руками, потом поправил их в интерфейсе и сохранил экспорт в репозиторий.

Запросы получились такие

```promql
# Запросы в секунду
sum by (path) (rate(http_requests_total{namespace="demo"}[1m]))

# Доля ошибок
sum(rate(http_request_errors_total{namespace="demo"}[1m]))
  / sum(rate(http_requests_total{namespace="demo"}[1m]))

# p95 времени ответа
histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{namespace="demo"}[1m])))
```

Скрейп идёт раз в 15 секунд, поэтому для `rate` взял окно в минуту. А в запросе для p95 нужен `le`, потому что это границы бакетов гистограммы. Если их убрать, квантиль нормально не посчитать.

Но сначала графики были пустые. Я уже начал искать проблему в Prometheus, а оказалось, что в Grafana был включён Builder вместо Code и запросов к сервису почти не было.

<img src="./pics/panel-editor.png" alt="" width="700"/>

<img src="./pics/dashboard-no-data.png" alt="" width="700"/>

Решил погонять немного запросов и посмотреть, изменится ли что-нибудь

```bash
kubectl port-forward -n demo svc/api 8080:8080
for i in $(seq 1 40); do curl -s localhost:8080/fail >/dev/null; done
for i in $(seq 1 10); do curl -s localhost:8080/slow >/dev/null & done; wait
curl "localhost:8080/load?n=300"
```

После нагрузки данные наконец появились

<img src="./pics/red-dashboard-load.png" alt="" width="700"/>

<img src="./pics/red-dashboard-errors.png" alt="" width="700"/>

Правда, p95 при этом оставался около 5 мс, хотя `/slow` отвечает за секунды. Сначала странно, но потом дошло, что я считаю p95 сразу по всем путям. `/load` создал кучу быстрых запросов к `/health`, и на их фоне медленных было слишком мало. Поэтому позже алерт на задержку проверял отдельно, нагружая именно `/slow`.

И ещё момент с `/load`. Он обращается на `127.0.0.1` внутри того же пода, поэтому запросы не проходят через Service и балансировку. RPS таким способом поднять можно, но проверкой всей сети это, конечно, не назвать.

## Часть 2. Loki и Alloy

Потом занялся логами. Loki нужен для хранения и поиска, а Alloy собирает логи с подов и отправляет их туда. Alloy поставил как DaemonSet, то есть по одному агенту на ноду. Нода у меня одна, так что и агент один.

Loki работает в режиме `SingleBinary`, без всех распределённых компонентов. Настройки лежат в [monitoring/values-loki.yaml](./monitoring/values-loki.yaml) и [monitoring/values-alloy.yaml](./monitoring/values-alloy.yaml). Alloy находит поды через Kubernetes API, оставляет поды своей ноды, добавляет лейблы `namespace`, `pod`, `container`, `app` и отправляет логи в Loki.

<details>
<summary>Конфиг Alloy</summary>

```alloy
discovery.kubernetes "pods" { role = "pod" }

discovery.relabel "pods" {
  targets = discovery.kubernetes.pods.targets
  rule {
    source_labels = ["__meta_kubernetes_pod_node_name"]
    regex = env("HOSTNAME")
    action = "keep"
  }
  rule { source_labels = ["__meta_kubernetes_namespace"] target_label = "namespace" }
  rule { source_labels = ["__meta_kubernetes_pod_name"] target_label = "pod" }
  rule { source_labels = ["__meta_kubernetes_pod_container_name"] target_label = "container" }
  rule { source_labels = ["__meta_kubernetes_pod_label_app"] target_label = "app" }
}

loki.source.kubernetes "pods" {
  targets = discovery.relabel.pods.output
  forward_to = [loki.write.default.receiver]
}

loki.write "default" {
  endpoint { url = "http://loki.monitoring.svc:3100/loki/api/v1/push" }
}
```

</details>

Alloy поднялся нормально, под был `2/2 Running`.

<img src="./pics/alloy-pods.png" alt="" width="500"/>

А вот Loki сразу решил упасть в `CrashLoopBackOff`. Посмотрел логи предыдущего запуска и увидел вот это.

```text
mkdir /var/loki: read-only file system
error initialising module: ruler-storage
```

Оказалось, что файловая система контейнера только для чтения, а с выключенным `persistence` в `/var/loki` вообще не было места, куда Loki мог бы писать. Добавил PVC на 2 ГБ. После этого пришлось переустановить релиз, потому что `volumeClaimTemplates` у StatefulSet просто так не поменять.

После переустановки логи появились в Grafana. Ошибки API ищу таким запросом.

```logql
{namespace="demo", app="api"} | json | level="ERROR"
```

`| json` разбирает поля из строки лога, так что можно фильтровать по `level` и смотреть `msg` с `trace_id`.

<img src="./pics/logs-grafana.png" alt="" width="700"/>

## Часть 3. OpenTelemetry и Jaeger

С логами уже разобрался, теперь хотел посмотреть, где именно запрос тратит время. Для этого поставил Jaeger и настроил отправку трейсов из API через OpenTelemetry.

Jaeger запустил из чарта `jaegertracing/jaeger`. Для лабы хватило all-in-one с хранением в памяти, хотя после перезапуска пода старые трейсы, понятно, пропадут. Values лежат в [monitoring/values-jaeger.yaml](./monitoring/values-jaeger.yaml).

В чарте API указал адрес Jaeger.

```yaml
otlpEndpoint: http://jaeger.monitoring.svc:4318
```

В Deployment он превращается в `OTEL_EXPORTER_OTLP_ENDPOINT`, а `/v1/traces` экспортёр добавляет сам.

Открыл Jaeger и увидел шесть трейсов. Три от `/slow` с длительностью примерно от 1,1 до 2,5 секунды и три от `/fail`, которые выполнялись быстро, но заканчивались ошибкой.

<img src="./pics/jaeger-traces.png" alt="" width="700"/>

У `/slow` внутри запроса появился отдельный спан `slow-op`. На скрине у него `sleep_ms = 2449` и длительность около 2,4 секунды. То есть теперь видно не просто то, что ответ был долгим, а куда конкретно ушло время.

<img src="./pics/trace-slow.png" alt="" width="700"/>

С `/fail` тоже всё понятно. Спан красный, `error = true`, `http.status_code = 500`, `http.target = /fail`.

<img src="./pics/trace-fail.png" alt="" width="700"/>

### Переход из лога в трейс

Захотелось ещё сделать так, чтобы не искать каждый трейс вручную. В логах уже есть `trace_id`, поэтому настроил в Grafana ссылку из Loki сразу в Jaeger. Для этого добавил Jaeger как источник данных и прописал у Loki `derivedFields`.

```yaml
jsonData:
  derivedFields:
    - name: trace_id
      matcherRegex: '"trace_id":"(\w+)"'
      url: '$${__value.raw}'
      datasourceUid: jaeger
```

С этой настройкой немного повозился из-за экранирования. В YAML с одинарными кавычками регулярке нужен обычный `\w`, а `$$` в URL нужен, чтобы Grafana не пыталась подставить переменную раньше времени.

Нашёл ошибку в Explore, у записи есть `trace_id`.

<img src="./pics/log-trace-id.png" alt="" width="700"/>

Раскрыл строку, и рядом появилась ссылка на Jaeger.

<img src="./pics/log-jaeger-link.png" alt="" width="700"/>

Нажал и попал сразу в трейс того самого `/fail` с ошибкой. Вот это уже удобно, не надо прыгать между вкладками и копировать ID руками.

<img src="./pics/log-to-trace.png" alt="" width="700"/>

## Часть 4. Alertmanager и Karma

Осталось проверить, что система умеет не только показывать ошибки, но и сообщать о них. Alertmanager уже был в `kube-prometheus-stack`. Для проверки сделал простой webhook, который печатает пришедшие запросы в логи. Манифест лежит в [monitoring/webhook-receiver.yaml](./monitoring/webhook-receiver.yaml).

Алерты с `app="api"` отправляются в этот webhook. Остальные алерты самого кластера уходят в `null`, чтобы не мешались. Интервалы поставил короткие, `group_wait: 10s` и `group_interval: 30s`, чтобы в лабораторной не сидеть лишнее время в ожидании.

Правила описал в [chart/api/templates/prometheusrule.yaml](./chart/api/templates/prometheusrule.yaml).

Первый алерт назвал `ApiHighErrorRate`. Он должен срабатывать, если ошибок больше 5% в течение двух минут. Но я ещё добавил проверку, что запросы вообще идут, иначе можно получить тревогу из-за одной ошибки при почти нулевом трафике.

```promql
sum(rate(http_request_errors_total{namespace="demo"}[2m]))
  / sum(rate(http_requests_total{namespace="demo"}[2m])) > 0.05
and sum(rate(http_requests_total{namespace="demo"}[2m])) > 0.05
```

Если бы такое случилось на настоящем сервисе, я бы сначала посмотрел ERROR-логи, потом открыл трейс по `trace_id` и проверил, не появились ли ошибки после последнего релиза.

Второй алерт `ApiHighLatencyP95` проверяет, что p95 больше одной секунды в течение двух минут.

```promql
histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{namespace="demo"}[2m]))) > 1
```

Тут как раз пригодится Jaeger, потому что по графику понятно, что сервис тормозит, но не видно, в каком именно месте запроса.

И третий `ApiDown`. Он срабатывает, если больше минуты нет ни одной живой цели скрейпа.

```promql
absent(up{namespace="demo", job="api"} == 1)
```

Использовал `absent`, а не просто `up == 0`. Если удалить вообще все поды, ряды `up` тоже исчезнут и сравнивать с нулём будет уже нечего.

### Проверка алертов

Чтобы поймать ошибки, отправлял запросы на `/fail` примерно раз в секунду около трёх минут. Для задержки аналогично гонял `/slow`. А для проверки `ApiDown` просто выключил все реплики.

```bash
kubectl scale deploy/api -n demo --replicas=0
```

Потом вернул обратно две реплики через `--replicas=2`.

Сначала все правила были в `inactive`.

<img src="./pics/alerts-inactive.png" alt="" width="700"/>

После нагрузки алерты на ошибки и задержку перешли в `firing`.

<img src="./pics/alerts-firing.png" alt="" width="700"/>

На подробном скрине доля ошибок получилась `0.568`, то есть почти 57%, а p95 дошёл до 3,625 секунды. Оба условия держались больше двух минут, поэтому всё сработало как задумано.

<img src="./pics/alerts-firing-details.png" alt="" width="700"/>

`ApiDown` сначала был в `pending`, потому что нужная минута ещё не прошла.

<img src="./pics/alert-down-pending.png" alt="" width="700"/>

Потом он тоже стал `firing`.

<img src="./pics/alert-down-firing.png" alt="" width="700"/>

Проверил Alertmanager, туда всё дошло.

<img src="./pics/alertmanager.png" alt="" width="700"/>

<img src="./pics/alertmanager-down.png" alt="" width="700"/>

В логах webhook тоже нашлись все три алерта, причём не только `firing`, но и `resolved` после восстановления сервиса.

```bash
kubectl logs -n monitoring deploy/webhook-logger --tail=60
```

Там есть `ApiDown`, `ApiHighErrorRate`, `ApiHighLatencyP95` и успешные ответы HTTP 200 на запросы Alertmanager. Значит, вся цепочка уведомлений работает.

Напоследок поставил Karma. Это интерфейс для Alertmanager, только с более удобной группировкой и фильтрами.

<img src="./pics/karma.png" alt="" width="700"/>

Мои алерты видны с получателем `webhook`. Рядом ещё куча стандартных алертов от одноузлового minikube, всего 11 групп, включая `Watchdog`, `TargetDown`, `NodeClockNotSynchronizing` и другие. Они отправляются в `null`, но Karma всё равно их показывает, потому что читает весь Alertmanager.

## Выводы

В итоге получилось связать метрики, логи, трейсы и алерты. В Prometheus и Grafana видно, что с сервисом не так, в Loki можно найти конкретную ошибку, а Jaeger показывает, где запрос долго выполнялся. Больше всего понравился переход из лога сразу в нужный трейс, без ручного поиска по `trace_id`.

С алертами тоже получилось всё проверить. Я специально вызвал ошибки, нагрузил `/slow`, потом вообще выключил поды и посмотрел, как правила переходят в `firing`, а Alertmanager отправляет сообщения в webhook.

Самое муторное оказалось не в самих запросах, а в настройках вокруг них. То Helm не находил файл, то Loki не мог писать в каталог, то ServiceMonitor не подходил под селектор и просто исчезал из Targets без ошибок. Но теперь хотя бы стало понятно, как все эти компоненты друг с другом связаны. Для лабы этого достаточно, хотя в реальном проекте, конечно, пришлось бы ещё нормально настроить хранение метрик и трейсов, чтобы они не пропадали после перезапуска.
