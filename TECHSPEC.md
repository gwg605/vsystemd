# vsystemd Technical Specification

---

## Парсинг OnCalendar= и timespan

**Библиотека:** `gitlab.com/allddd/go-systemd-time` v1.1.0 (BSD-2-Clause)

Используется для разбора:

- `OnCalendar=` — через `ParseCalendarEvent(s) → CalendarEvent` и `CalendarEvent.Next(t time.Time) → (time.Time, bool)`
- `RestartSec=`, `OnBootSec=`, `OnStartupSec=` — через `ParseTimespan(s) → time.Duration`

Библиотека поддерживает полный синтаксис systemd calendar expressions: списки, диапазоны (`..`), шаги (`/`), тильда (`~` — дни с конца месяца), алиасы (`daily`, `weekly`, `monthly`, `hourly`, `minutely`, `quarterly`, `semiannually`, `annually`), суффикс часового пояса (IANA, UTC, offset). Чистый Go без cgo, работает на Windows.

Поддержка часовых поясов обеспечивается через embedded IANA tzdata (`import _ "time/tzdata"`), без зависимости от наличия tzdata в ОС.

Внутри кода и в persistence время хранится и обрабатывается в UTC.
`OnCalendar=` без суффикса TZ интерпретируется в локальной TZ машины, с
суффиксом — в указанной TZ. При вводе время переводится в UTC, при отображении
показывается в локальной TZ либо с явно указанной TZ.

---

## Scheduler

**Назначение:** внутренний компонент daemon'а, отвечает за своевременный запуск сервисов по расписанию `OnCalendar=`.

**Поведение:**

- Хранит набор активных timer'ов.
- Для каждого timer'а вычисляет следующий момент срабатывания через `CalendarEvent.Next(now)`.
- Ждёт до этого момента, после чего запускает целевой сервис вызовом `StartService()` через Windows SCM.
- После каждого срабатывания немедленно вычисляет следующий тик и снова ждёт (цикл).
- Если `CalendarEvent.Next` возвращает `ok=false` (нет будущих тиков) — timer останавливается, в лог пишется предупреждение.

**Политика пропущенных тиков:** пропущенные тики не компенсируются. Если daemon был остановлен, расписание возобновляется с первого будущего тика относительно текущего времени на момент старта.

**Persistence:** после каждого срабатывания Scheduler обновляет `last_run` и `next_run` в `storage.WriteTimerState()`.

**Reload:** при `daemon-reload` Scheduler получает обновлённый набор timer'ов и выполняет diff с текущим состоянием:

- Новые timer'ы — запускаются (если `enabled=true`).
- Удалённые timer'ы — останавливаются, запись удаляется из persistence (`DeleteTimerState`).
- Timer'ы с изменившимся `OnCalendar=` — перезапускаются.

---

## IPC: схема коммуникации daemon ↔ runner ↔ systemctl

### Принцип

Коммуникация построена на асинхронных сообщениях поверх однонаправленных named pipes. Каждый участник владеет своим входящим pipe и отвечает за его жизненный цикл. Исходящие сообщения отправляются через ленивый connect к pipe получателя.

Цель схемы — полностью event-driven взаимодействие без периодического polling'а с любой из сторон. Соединение устанавливается только в момент отправки сообщения и закрывается после доставки (по таймауту).

### Named pipes

| Участник | Pipe (входящий) | Владелец |
|---|---|---|
| daemon | `\\.\pipe\vsystemd-in` | daemon |
| runner (wrapper) | `\\.\pipe\vsystemd-svc-<unitname>` | runner |
| systemctl | `\\.\pipe\vsystemd-ctl-<pid>` | systemctl |

daemon держит `vsystemd-in` открытым всё время своей работы и принимает сообщения от всех runner'ов и от systemctl.

Каждый runner держит `vsystemd-svc-<unitname>` открытым всё время своей работы и принимает сообщения от daemon'а.

systemctl создаёт `vsystemd-ctl-<pid>` при старте, слушает ответы, после завершения pipe закрывается. Имя обратного pipe вычисляется из поля `from` входящего сообщения — явная регистрация не требуется.

### Ленивый connect

Перед отправкой сообщения отправитель проверяет наличие активного соединения с pipe получателя. Если соединения нет — выполняется попытка подключения. При неудаче сообщение не доставляется, факт записывается в лог. Повторных попыток нет — следующее событие создаст новое соединение.

Соединение закрывается после отправки сообщения по истечении таймаута простоя.

### Гарантии доставки

Схема работает по принципу best-effort: подтверждение доставки отсутствует. Потерянное сообщение (pipe недоступен, таймаут) фиксируется в лог и не повторяется. Это принятое ограничение — актуальное состояние всегда можно получить через SCM или явный запрос статуса.

### Поведение при старте daemon'а

При запуске daemon обходит список известных юнитов и отправляет каждому runner'у запрос на получение текущего состояния. Runner'ы, получившие запрос, отвечают сообщением с текущим состоянием. Daemon обновляет внутреннее состояние на основе полученных ответов. Runner'ы, pipe которых недоступен, считаются неактивными — daemon берёт состояние из SCM.

---

## IPC: каталог сообщений

### Правила идентификации

- Каждое сообщение имеет уникальный `id` (UUID), включая ответные.
- `request_id` — `id` сообщения, на которое данное сообщение является ответом. `null` если сообщение не является ответом.
- Ответное сообщение имеет собственный уникальный `id` и заполненный `request_id`.

---

### runner → daemon

| Тип | request_id | Fire-and-forget | Payload |
|---|---|---|---|
| `state_event` | null | да | `unit`, `state`, `pid`, `exit_code`, `restart_count`, `timestamp`, `last_error` |

**`state_event`** — отправляется runner'ом при каждой смене состояния управляемого процесса: запуск, успешная остановка, падение, рестарт. Подтверждение от daemon'а не ожидается.

---

### daemon → runner

| Тип | request_id | Ожидаемый ответ | Payload |
|---|---|---|---|
| `status_request` | null | `state_event` | — |

**`status_request`** — отправляется daemon'ом при старте для получения актуального состояния каждого runner'а. Runner отвечает сообщением `state_event` с `request_id` равным `id` этого запроса.

---

### systemctl → daemon

| Тип | Ожидаемый ответ | Payload |
|---|---|---|
| `daemon_reload` | `reload_result` | — |
| `status_request` | `status_response` | `unit` |
| `list_units` | `list_units_response` | — |
| `timer_enable` | `timer_result` | `unit`, `enabled` (bool) |
| `timer_start` | `timer_result` | `unit` |
| `timer_stop` | `timer_result` | `unit` |

**`daemon_reload`** — инициирует пересканирование unit-файлов и обновление конфигурации.

**`status_request`** — запрос агрегированного состояния конкретного юнита.

**`list_units`** — запрос списка всех известных юнитов с их состояниями.

**`timer_enable`** — установить `enabled=true/false` для таймера (persist).

**`timer_start`** — добавить таймер в scheduler (не меняет `enabled`).

**`timer_stop`** — убрать таймер из scheduler (не меняет `enabled`); `next_run` сбрасывается, `last_run` сохраняется.

---

### daemon → systemctl

| Тип | request_id | Payload |
|---|---|---|
| `reload_result` | id запроса `daemon_reload` | `success` |
| `status_response` | id запроса `status_request` | `unit`, `systemd_state`, `scm_state`, `pid`, `exit_code`, `restart_count`, `last_transition`, `last_error`, `log_tail`; для `.timer` дополнительно `enabled`, `running`, `next_run`, `last_run`, `activates` |
| `list_units_response` | id запроса `list_units` | `units[]`: `unit`, `type`, `systemd_state`, `scm_state`, `description`, `enabled`, `running`, `next_run`, `last_run`, `activates` |
| `timer_result` | id исходного запроса | `unit`, `success`, `enabled`, `running`, `last_run`, `next_run` |
| `error_response` | id исходного запроса | `error` |

**`error_response`** — универсальный ответ на любой запрос в случае ошибки обработки. `request_id` содержит `id` того запроса, который вызвал ошибку.

Поля `enabled`, `running`, `next_run`, `last_run`, `activates` в `list_units_response` заполняются только для юнитов типа `.timer`; для `.service` — `null`/пусто.

Для `.timer` поле `systemd_state` равно `active (waiting)`, если таймер добавлен в scheduler, иначе `inactive (dead)`.

---

## Вычисление статуса юнита

Статус юнита вычисляется daemon'ом из двух источников: **SCM** (авторитетный источник факта running/stopped) и **внутреннее состояние daemon'а** (обогащённая systemd-семантика, получаемая от runner'а через `state_event`). При конфликте SCM всегда приоритетнее.

### Таблица вычисления статуса

| SCM state | Daemon state | exit_code | Итоговый статус |
|---|---|---|---|
| `SERVICE_RUNNING` | `active` | — | `active (running)` |
| `SERVICE_RUNNING` | `activating` | — | `activating (start)` |
| `SERVICE_RUNNING` | `restarting` | — | `activating (auto-restart)` |
| `SERVICE_RUNNING` | нет данных | — | `active (running)` |
| `SERVICE_STOPPED` | `inactive` | 0 | `inactive (dead)` |
| `SERVICE_STOPPED` | `failed` | ≠ 0 | `failed` |
| `SERVICE_STOPPED` | нет данных | — | `inactive (dead)` |
| `SERVICE_START_PENDING` | любое | — | `activating (start)` |
| `SERVICE_STOP_PENDING` | любое | — | `deactivating (stop)` |
| нет в SCM | любое | — | `inactive (dead)` |

### Правила

**SCM приоритетнее daemon state** для факта running/stopped. Daemon state уточняет детали внутри этого факта (например, различает `active` и `restarting` при `SERVICE_RUNNING`).

**`failed`** наступает только при одновременном выполнении двух условий:
- SCM сообщает `SERVICE_STOPPED`
- Runner сообщил daemon'у `state=failed` с ненулевым `exit_code`

Пока runner перезапускает процесс в рамках `Restart=` — статус `activating (auto-restart)`, не `failed`. `failed` наступает только когда исчерпан лимит рестартов (`StartLimitBurst`) или `Restart=no`.

**Если daemon не запущен или pipe runner'а недоступен** — поле `systemd_state` недоступно, статус определяется только по SCM. Состояние `failed` в этом случае не отображается — только `inactive (dead)`. Это зафиксированное расхождение с оригинальным systemd (known difference #2 в ARCHITECTURE.md).

### Поля `status_response`

| Поле | Источник | Описание |
|---|---|---|
| `systemd_state` | daemon internal | итоговый вычисленный статус (`active (running)`, `failed`, и т.д.) |
| `scm_state` | SCM | числовой код состояния SCM (`SERVICE_RUNNING`, `SERVICE_STOPPED`, и т.д.) |
| `pid` | runner / SCM | PID основного процесса; 0 если не запущен |
| `exit_code` | runner | код завершения последнего запуска процесса |
| `restart_count` | runner | количество автоматических рестартов с момента последнего старта |
| `last_transition` | daemon | timestamp последней смены состояния |
| `last_error` | runner | строка с ошибкой запуска, сгенерированная runner'ом; только последняя; null если ошибок не было |
| `log_tail` | лог-файл | последние 10 строк из текущего файла `logs\<service>\` или явного `file:`/`append:` файла |

---

## Структура проекта

### Бинари

| Бинарь | Режим | Роль |
|---|---|---|
| `vsystemd.exe` | `--mode=daemon` | Windows Service, управляет состоянием, scheduler, IPC сервер |
| `vsystemd.exe` | `--mode=run --unit=<name>` | Обёртка процесса, Restart= логика, pipe к daemon |
| `systemctl.exe` | — | CLI, SCM напрямую или через daemon |

### Пакеты

```
cmd/
  vsystemd/             — точка входа, dispatch --mode=daemon / --mode=run
  systemctl/            — CLI

internal/
  unit/                 — парсер unit-файлов, модели UnitFile, ServiceUnit, TimerUnit
  scm/                  — интерфейс Manager + Windows реализация
  transport/            — интерфейс Transport, Message, MsgHandler, Opt
  transport/pipe/       — реализация на named pipes, pipe envelope, lazy connect
  daemon/               — Daemon, ServiceState, логика daemon-reload
  scheduler/            — OnCalendar= scheduler
  runner/               — Runner, управление дочерним процессом, Restart= логика
  storage/              — интерфейс Storage + prod реализация + fake реализация

tests/                  — интеграционные тесты (fake SCM + fake Storage)
```

### Ключевые интерфейсы

```go
type Storage interface {
    // unit files
    ListUnits() ([]string, error)
    ReadUnit(name string) ([]byte, error)
    // timer state (persistence)
    ReadTimerState(name string) (TimerState, error)
    WriteTimerState(name string, state TimerState) error
    DeleteTimerState(name string) error
    ListTimerStates() (map[string]TimerState, error)
}

type TimerState struct {
    Enabled bool
    LastRun time.Time
    NextRun time.Time
}

type Manager interface {
    CreateService(cfg ServiceConfig) error
    UpdateService(cfg ServiceConfig) error
    DeleteService(name string) error
    StartService(name string) error
    StopService(name string) error
    QueryStatus(name string) (ServiceStatus, error)
    ListServices() ([]ServiceStatus, error)
}

type ServiceConfig struct {
    Name         string
    BinaryPath   string
    StartType    StartType
    Dependencies []string
    User         string
    Password     string
    Description  string
}
```

`Storage` абстрагирует только persistence-операции: unit-файлы и состояние
таймеров. Логи ведутся отдельным компонентом `logwriter`.

На Windows `ListServices()` возвращает только managed-сервисы, помеченные
`LoadOrderGroup = "vsystemd"`; чужие SCM-сервисы не затрагиваются.

### Go module

- Module: `github.com/gwg605/vsystemd`
- Минимальная версия Go: 1.26+

### За рамками v1

- `OnBootSec=` / `OnStartupSec=` через Windows Task Scheduler
- `Wants=`, `Before=`, `BindsTo=`, `PartOf=`, `Conflicts=`

---

## Runner

### Компоненты

| Компонент | Ответственность |
|---|---|
| `SCMHandler` | Регистрация в SCM, приём control-кодов (STOP, INTERROGATE), обновление `SetServiceStatus` |
| `ProcessManager` | Запуск ExecStartPre → ExecStart → ExecStartPost, настройка env/user/workdir, ExecStop |
| `RestartController` | `Restart=` / `RestartSec=` / `StartLimitBurst=` логика, счётчик `restart_count` |
| `LogStreamer` | Перехват stdout/stderr дочернего процесса → `logwriter.Writer` |
| `StateReporter` | Отправка `state_event` в daemon, обслуживание входящего pipe, ответ на `status_request` |

### Жизненный цикл

```
SCM start → Runner start
  → ExecStartPre
  → ExecStart (child process)
  → ExecStartPost
  → [работает, мониторит child]
  → child exit → RestartController
  → [исчерпан Restart= или Restart=no] → Runner exit
SCM stop → SERVICE_CONTROL_STOP → ExecStop → Runner exit
```

### ExecStartPre / ExecStartPost

Поведение как в systemd:

- `ExecStartPre` ненулевой exit code → Runner переходит в `failed`, `ExecStart` не запускается
- `ExecStartPost` ненулевой exit code → сервис переходит в `failed`, дочерний процесс останавливается
- Префикс `-` (`-/path/to/cmd`) → ошибка игнорируется, выполнение продолжается

### EnvironmentFile=

Поведение как в systemd:

- `EnvironmentFile=/path` — файл обязателен; отсутствие → `failed`
- `EnvironmentFile=-/path` — файл опциональный; отсутствие игнорируется

### Restart= логика

| Директива | Поведение |
|---|---|
| `Restart=no` | Не перезапускать |
| `Restart=always` | Перезапускать всегда |
| `Restart=on-failure` | Перезапускать только при ненулевом exit code |

- `StartLimitBurst=` / `StartLimitIntervalSec=` — как в systemd: N попыток за интервал, после исчерпания → `failed`
- Между попытками — пауза `RestartSec=`

### Остановка процесса

При получении `SERVICE_CONTROL_STOP`:

1. Если задан `ExecStop=` — выполнить его
2. Если не задан — отправить `Ctrl+C` (`GenerateConsoleCtrlEvent`)
3. Ждать `TimeoutStopSec=` (default 45s)
4. Если процесс не завершился — `TerminateProcess()`

`TimeoutStopSec=` — директива unit-файла, поддерживается в v1.

---

## Таймеры: управление и persistence

### Два независимых состояния

- `enabled` — сохраняется в persistence; определяет автозапуск при старте daemon'а
- `running` — in-memory; таймер активен в scheduler в рамках текущей сессии

### Persistence

Файл: `C:\ProgramData\vsystemd\timers.json`

```json
{
  "foo.timer": {
    "enabled": true,
    "last_run": "2026-11-09T10:00:00Z",
    "next_run": "2026-11-09T11:00:00Z"
  }
}
```

`last_run` и `next_run` хранятся и обрабатываются только в UTC (RFC3339 с `Z`).
При отображении время либо выводится в UTC с явным `Z`, либо конвертируется в
локальную TZ с указанием зоны.

### Семантика команд

| Команда | Эффект |
|---|---|
| `systemctl enable foo.timer` | `enabled=true` в persistence; не запускает таймер |
| `systemctl disable foo.timer` | `enabled=false` в persistence; не останавливает таймер |
| `systemctl start foo.timer` | добавляет в scheduler (running); не меняет `enabled` |
| `systemctl stop foo.timer` | убирает из scheduler; не меняет `enabled` |

При старте daemon'а: все таймеры с `enabled=true` автоматически добавляются в scheduler.

### Routing

Все команды для таймеров (`enable`, `disable`, `start`, `stop`) — через daemon pipe (не SCM). SCM о таймерах не знает.

### IPC: новые сообщения systemctl → daemon

| Тип | Ожидаемый ответ | Payload |
|---|---|---|
| `timer_enable` | `timer_result` | `unit`, `enabled` (bool) |
| `timer_start` | `timer_result` | `unit` |
| `timer_stop` | `timer_result` | `unit` |

| Тип | request_id | Payload |
|---|---|---|
| `timer_result` | id исходного запроса | `unit`, `success`, `enabled`, `running`, `last_run`, `next_run` |

---

## Логирование

### Логи сервисов

- по умолчанию stdout и stderr → `C:\ProgramData\vsystemd\logs\<service>\<timestamp>.log`
- `<service>` — имя юнита без расширения (`foo.service` → `foo`)
- `<timestamp>` — UTC время создания файла, формат `YYYY-MM-DD_HH-MM-SS.mmm`
- `StandardOutput=file:<path>` / `StandardError=file:<path>` — truncate, без ротации
- `StandardOutput=append:<path>` / `StandardError=append:<path>` — append, без ротации
- `null` — поток отбрасывается
- прочие значения — warning и дефолтный лог
- одинаковый канонический путь для stdout/stderr → один writer
- `file:` + `append:` на один путь → append для обоих
- относительные пути считаются от `WorkingDirectory`; родительские каталоги создаются

### Собственные логи vsystemd

- daemon → `C:\ProgramData\vsystemd\logs\vsystemd\daemon_<timestamp>.log`
- runner → `C:\ProgramData\vsystemd\logs\vsystemd\<service>_<timestamp>.log`
- формат: plain text
- файлы открыты на append и доступны на чтение
- ротация по `logs.max_size_mb` и `logs.max_age_days`
- при ротации создаётся новый файл с UTC timestamp; переименования не используются
- закрытые managed-логи старше `max_age_days` удаляются
- `file:`/`append:` файлы не ротируются и не удаляются

### `systemctl status`

Показывает последние 10 строк из текущего/последнего managed-файла
`logs\<service>\`, либо из явно заданного `file:`/`append:` файла.
Ротированные файлы не подмешиваются. При `null` для обоих потоков tail пуст.

---

## Transport (IPC абстракция)

### Два уровня

**Уровень Transport** (`internal/transport/`) — абстрактный peer-центричный интерфейс. Оперирует понятиями `Peer` (тип + имя), `Message`, `Target`, `Send`, `Req`. Не знает про pipes, конверты, именование.

**Уровень Pipe** (`internal/transport/pipe/`) — реализация Transport на named pipes. Добавляет envelope с деталями доставки, аутентифицирует входящее соединение по ОС-идентичности и отдаёт доверенный `Peer`. Исходящий target строится методом `Target`.

### Transport API

```go
// internal/transport/

type PeerType uint8 // PeerDaemon | PeerRunner | PeerControl

// Peer — проверенная идентичность пира. Создаётся только транспортом;
// приложение получает его в обработчике или через Target.
type Peer struct{ /* typ, name, elevated */ }

func (p Peer) Type() PeerType
func (p Peer) Name() string
func (p Peer) Elevated() bool

type Message struct {
    ID        string          // UUID
    RequestID string          // UUID ответного запроса; "" если не ответ
    Type      string          // тип сообщения
    Payload   json.RawMessage // тело сообщения
}

type MsgHandler func(source Peer, msg Message) error

type Transport interface {
    Target(typ PeerType, opts ...PeerOption) Peer
    Send(target Peer, msg Message, opts ...Opt) error
    Req(target Peer, msg Message, opts ...Opt) (Message, error)
    Close() error
}

func New(config Config, local Peer, handler MsgHandler) (Transport, error)

// Opts — переопределяют значения из конфига
WithTimeout(d time.Duration)        // таймаут ожидания ответа (для Req)
WithConnectTimeout(d time.Duration) // таймаут подключения (для Send и Req)

// PeerOption
WithName(name string) // имя целевого пира (напр. юнит для runner)
```

Хендлер отвечает, используя `source` как target:

```go
func (c *Component) handleStatusRequest(source transport.Peer, msg transport.Message) error {
    resp := c.buildStatusResponse(msg)
    return c.transport.Send(source, resp)
}
```

### Pipe envelope (внутренний формат)

Формат одного сообщения на уровне pipe реализации — newline-delimited JSON:

```json
{
  "from": "systemctl_1234",
  "msg": {
    "id": "uuid",
    "request_id": null,
    "type": "status_request",
    "payload": { "unit": "foo.service" }
  }
}
```

Поле `from` содержит имя отправителя (недоверенное). Транспорт сверяет его с именем проверенного `Peer` и дропает сообщение при несовпадении.

### Именование peers и pipes

| PeerType | peerName | Входящий pipe |
|---|---|---|
| daemon | `daemon` | `\\.\pipe\vsystemd-in` |
| runner | `foo.service` | `\\.\pipe\vsystemd-svc-foo.service` |
| control | `systemctl_<pid>` | `\\.\pipe\vsystemd-ctl-<pid>` |

### Аутентификация входящих соединений

Аутентификатор внутренний (не часть публичного интерфейса) и выбирается по типу локального пира:

- **daemon-канал** (`clientAuthenticator`): клиент — либо service-runner (own-process служба SCM, чей `ServiceStartName` совпадает с SID токена, а `FILE_ID` запущенного образа совпадает с `FILE_ID` exe из `BinaryPathName`) → `PeerRunner(<service>.service)`; либо elevated control-клиент → `PeerControl(systemctl_<pid>)`; иначе соединение отклоняется.
- **runner/control-каналы** (`daemonOnlyAuthenticator`): принимается только демон (LocalSystem или elevated-администратор) → `PeerDaemon`.

Общие проверки: `GetNamedPipeClientProcessId` → `OpenProcess`; время создания процесса должно быть раньше соединения (защита от переиспользования PID); токен → SID и elevation. Из-за проверки elevation control-клиент (`systemctl`) должен запускаться elevated.

### Request/Response

Transport реализует матчинг ответов по `request_id` и сверяет источник ответа с ожидаемым target:

- `Req()` отправляет сообщение с уникальным `id` и ждёт входящего сообщения с `request_id` равным этому `id`
- ответ принимается только от того пира, которому был адресован запрос
- поддерживает несколько одновременно ожидающих запросов
- при истечении таймаута возвращает ошибку и пишет в лог

### Логи

Логи pipe-транспорта помечаются тегом `[clientPid->serverPid]`, например `pipe: [1110->13241] sent`. `systemctl` включает диагностические логи флагом `--log [none|stdout|stderr]` (по умолчанию `none`).

---

## Конфигурация

### Расположение

Путь захардкожен: `C:\ProgramData\vsystemd\config.json`

Переопределяется через переменную окружения: `VSYSTEMD_CONFIG=C:\custom\path\config.json`

Каждый бинарь (`vsystemd`, `systemctl`) читает конфиг самостоятельно при старте. Все значения имеют дефолты, конфиг переопределяет только нужные.

### Формат

```json
{
  "system_dir": "C:\\ProgramData\\vsystemd\\system",
  "logs_dir": "C:\\ProgramData\\vsystemd\\logs",
  "timers_state_file": "C:\\ProgramData\\vsystemd\\timers.json",
  "log_level": "info",
  "logs": {
    "max_size_mb": 10,
    "max_age_days": 7
  },
  "ipc": {
    "connect_timeout_ms": 2000,
    "idle_timeout_ms": 5000,
    "request_timeout_ms": 10000
  },
  "defaults": {
    "restart_sec_ms": 100,
    "timeout_stop_sec_ms": 45000,
    "start_limit_burst": 5,
    "start_limit_interval_sec_ms": 10000
  }
}
```
