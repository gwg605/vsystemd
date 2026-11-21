# vsystemd Technical Specification

## Overview
### Applications and instances

vsystemd has three runtime entities: the **daemon**, **runners**, and the **control** client.

- **daemon** — the manager. A single, always-running process that owns the unit registry, computes service state, runs the timer manager, and serves the IPC endpoint. It is the source of truth for unit configuration and for daemon-specific operations.
- **runner** — a per-service process wrapper. There is one runner per managed service; it launches and supervises the service's child process, applies `Restart=`/`RestartSec=` logic, and reports state back to the daemon.
- **control** — the user-facing CLI (`systemctl.exe`). It is short-lived: it performs a single command and exits. Operations that SCM can serve go directly to SCM; daemon-specific operations (reload, unit listing, timer management) go through the daemon.

All three come from two executables. `vsystemd.exe` is dual-mode and becomes either the daemon or a runner depending on its arguments:

- the daemon starts as `vsystemd.exe --mode=daemon`; it is registered in the Windows SCM as a service and started by SCM (typically at boot);
- a runner starts as `vsystemd.exe --mode=run --unit=<name>`, where `<name>` is the unit it serves; every managed service is registered in SCM with this command line as its service binary, so SCM starts and stops the runner together with the service.

The control client is the separate `systemctl.exe`, run by the user for each command. It must be elevated: control connections to the daemon are accepted only from an elevated token.

### Interaction and data flow
### Repository structure
### v1 scope

## Interfaces
### Transport
### SCM manager
### Storage
### Timer manager

## Protocol
### Identification rules
### Message catalog
### Delivery guarantees

## Components
### daemon
### runner
### timermgr
### scm
### transport / pipe
### storage
### unit
### systemctl
### Unit status computation
### Logging
### Configuration

---

## Appendix A. Legacy content (to be migrated)

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

## Timer manager

**Назначение:** внутренний компонент daemon'а (`internal/timermgr`), владеет базой timer'ов и отвечает за своевременный запуск сервисов по расписанию `OnCalendar=`.

**Поведение:**

- Хранит набор timer'ов (`TimerConfig{Name, OnCalendar}`) и их статус (`TimerStatus{Enabled, Running, LastRun, NextRun, ActiveSince}`).
- `Enabled` — административный флаг (в БД), `Running` — рантайм-статус (timer реально в расписании); они независимы.
- Для каждого timer'а вычисляет следующий момент срабатывания через `CalendarEvent.Next(now)`.
- Ждёт до этого момента, после чего сообщает о срабатывании через колбэк `func(name string, status TimerStatus)`.
- Демон по имени timer'а находит timer unit, определяет целевой сервис (`Unit=`) и запускает его вызовом `StartService()` через Windows SCM. Менеджер не зависит от SCM.
- После каждого срабатывания немедленно вычисляет следующий тик и снова ждёт (цикл).
- Если `CalendarEvent.Next` возвращает `ok=false` (нет будущих тиков) — timer останавливается, в лог пишется предупреждение.

**Политика пропущенных тиков:** пропущенные тики не компенсируются. Если daemon был остановлен, расписание возобновляется с первого будущего тика относительно текущего времени на момент старта.

**Persistence:** после каждого изменения (срабатывание, `start`/`stop`, `enable`/`disable`, правка конфига) менеджер сериализует базу в свой JSON-формат и атомарно пишет через `storage.WriteTimers()`; при `New` читает `storage.ReadTimers()`. `Running` не персистится — при `Start()` в расписание ставятся все `enabled`-timer'ы.

**API:** `SetTimer` (upsert конфига), `DeleteTimer` (только если `disabled` и `stopped`), `Enable`, `StartTimer`, `StopTimer`, `QueryStatus`, `QueryStatuses`, `ListTimers`, `GetTimer`, `Start` (запускает scheduler и ставит `enabled`-timer'ы), `Stop` (останавливает все и завершает работу).

**Жизненный цикл:** `New` только загружает БД и не вызывает event handler; `Start` запускает scheduler; `Stop` останавливает все timer'ы и завершает работу (демон зовёт `Start`/`Stop`).

**Reload:** при `daemon-reload` демон синхронизирует БД с юнитами:

- Новые timer'ы — добавляются (`SetTimer`); автостарт `enabled`-timer'ов выполняется только в `Start()`.
- Timer'ы с изменившимся `OnCalendar=` — перезапускаются.
- Timer'ы без файла юнита — удаляются только если `disabled` и `stopped`; иначе сохраняются.

---

## IPC: схема коммуникации daemon ↔ runner ↔ systemctl

### Принцип

Коммуникация построена на модели клиент–сервер поверх одного named pipe. Сервер — daemon; клиенты — runner'ы и systemctl. Клиенты инициируют соединение к pipe демона и работают по нему в обе стороны: запрос и ответ идут по одному соединению, обратный pipe не нужен.

Демон ничего не пушит: runner сам отправляет состояние при изменении и по heartbeat-таймеру; systemctl получает ответ на свой запрос в том же соединении.

### Named pipes

| Участник | Роль | Pipe |
|---|---|---|
| daemon | слушает | `\\.\pipe\vsystemd-in` |
| runner | подключается | `\\.\pipe\vsystemd-in` |
| systemctl | подключается | `\\.\pipe\vsystemd-in` |

Демон держит `vsystemd-in` открытым всё время работы. Каждый runner держит постоянное соединение с демоном и переподключается при разрыве. systemctl подключается на время выполнения команды.

### Аутентификация

Аутентификацию выполняет демон при приёме соединения. Клиент — либо managed-служба (own-process, аккаунт токена совпадает с `ServiceStartName`, образ — с `BinaryPathName`), либо elevated-клиент без службы (control); иное отклоняется. Время создания процесса должно предшествовать соединению (защита от PID reuse).

Клиент удостоверяет сервер по позиции: демон — единственный владелец pipe (`FILE_FLAG_FIRST_PIPE_INSTANCE` + ACL + ранний старт службы). Отдельной верификации сервера клиентом нет.

### Heartbeat и жизнеспособность

Runner держит постоянное соединение и по таймеру отправляет текущее состояние (`state_event`). Если демон не получает heartbeat дольше `RunnerDeadAfter`, раннер помечается `dead`.

### Поведение при старте daemon'а

Демон не опрашивает runner'ов. После рестарта демона соединения runner'ов рвутся; runner переподключается и отправляет текущее состояние при следующем heartbeat.

### Гарантии доставки

Схема работает по принципу best-effort: подтверждение доставки отсутствует. Потерянное сообщение (pipe недоступен, таймаут) фиксируется в лог. Актуальное состояние всегда доступно через SCM или следующий heartbeat.

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

**`state_event`** — отправляется runner'ом при каждой смене состояния управляемого процесса (запуск, успешная остановка, падение, рестарт) и периодически как heartbeat. Подтверждение от daemon'а не ожидается. Wire-типы (`StateEvent`, тип сообщения и значения `state`) определены в `internal/protocol`.

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

**`timer_start`** — поставить таймер в расписание (не меняет `enabled`).

**`timer_stop`** — убрать таймер из расписания (не меняет `enabled`); `next_run` и `active_since` сбрасываются, `last_run` сохраняется.

---

### daemon → systemctl

| Тип | request_id | Payload |
|---|---|---|
| `reload_result` | id запроса `daemon_reload` | `success` |
| `status_response` | id запроса `status_request` | `unit`, `systemd_state`, `scm_state`, `pid`, `exit_code`, `restart_count`, `last_transition`, `last_error`, `log_tail`, `description`, `enabled`; для `.timer` дополнительно `running`, `next_run`, `last_run`, `active_since`, `activates` |
| `list_units_response` | id запроса `list_units` | `units[]`: `unit`, `type`, `systemd_state`, `scm_state`, `description`, `enabled`, `running`, `next_run`, `last_run`, `activates` |
| `timer_result` | id исходного запроса | `unit`, `success`, `enabled`, `running`, `last_run`, `next_run` |
| `error_response` | id исходного запроса | `error` |

**`error_response`** — универсальный ответ на любой запрос в случае ошибки обработки. `request_id` содержит `id` того запроса, который вызвал ошибку.

Поля `running`, `next_run`, `last_run`, `active_since`, `activates` заполняются только для юнитов типа `.timer`; для `.service` — `null`/пусто. Поле `enabled` для `.service` вычисляется из SCM `StartType` (`Automatic`/`Boot`/`System` → `true`, `Manual`/`Disabled` → `false`), для `.timer` — из базы timer'ов.

Для `.timer` поле `systemd_state` равно `active (waiting)`, если таймер стоит в расписании, иначе `inactive (dead)`. Поле `active_since` содержит момент добавления таймера в расписание и сбрасывается при `stop`.

---

## Вычисление статуса юнита

Статус юнита вычисляется daemon'ом из двух источников: **SCM** (авторитетный источник факта running/stopped) и **внутреннее состояние daemon'а** (обогащённая systemd-семантика, получаемая от runner'а через `state_event`). При конфликте SCM всегда приоритетнее.

### Таблица вычисления статуса

| SCM state | Daemon state | exit_code | Итоговый статус |
|---|---|---|---|
| `SERVICE_RUNNING` | `active` | — | `active (running)` |
| `SERVICE_RUNNING` | `activating` | — | `activating (start)` |
| `SERVICE_RUNNING` | `restarting` | — | `activating (auto-restart)` |
| `SERVICE_RUNNING` | `deactivating` | — | `deactivating (stop)` |
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
| `description` | unit-файл | значение `Description=` из секции `[Unit]` |
| `enabled` | SCM `StartType` / timers.json | для `.service` — из SCM; для `.timer` — из `timers.json` |
| `active_since` | timer manager / timers.json | для `.timer` — момент добавления в расписание; отсутствует у неактивного таймера |

---

## Структура проекта

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
  timermgr/             — база timer'ов и OnCalendar= scheduler
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
    // timers (raw bytes; layout owned by timermgr)
    ReadTimers() ([]byte, error)
    WriteTimers(data []byte) error
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

type Account string

type ServiceConfig struct {
    Name         string
    BinaryPath   string
    StartType    StartType
    Dependencies []string
    User         Account
    password     string
    Description  string
}
```

`NewServiceConfig(unit, binaryPath, startType)` транслирует unit-файл в
`ServiceConfig`; `NewAccount(user, name)` резолвит win32-аккаунт. Пароль
устанавливается методом `SetPassword` и никогда не возвращается при чтении
(поле неэкспортируемое).

`Storage` абстрагирует только persistence-операции: unit-файлы и байты базы
таймеров (структуру JSON определяет `timermgr`). Логи ведутся отдельным
компонентом `logwriter`.

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
| `controlHandler` | Регистрация в SCM, приём control-кодов (STOP/SHUTDOWN/INTERROGATE), цикл состояний и обновление `SetServiceStatus` |
| `operation` | Запуск внешнего процесса (`processOperation`) или последовательности (`sequenceOperation`), ожидание завершения, `Cancel(Interrupt\|Kill)` |
| `RestartController` | `Restart=` / `RestartSec=` / `StartLimitBurst=` логика, счётчик `restart_count` |
| `LogStreamer` | Перехват stdout/stderr дочернего процесса → `logwriter.Writer` |
| `StateReporter` | Отправка `state_event` в daemon и heartbeat-таймер |

### Жизненный цикл

```
SCM start → Runner start
  → ExecStartPre
  → ExecStart (child process)
  → ExecStartPost
  → [работает, мониторит child]
  → child exit → drain (дожидаемся завершения всех операций)
  → [Restart= активен и лимит не исчерпан] → RestartSec → повтор
  → [Restart=no или лимит исчерпан] → Runner exit
SCM stop → SERVICE_CONTROL_STOP → drain (ExecStop / Ctrl+C, по таймауту Kill) → Runner exit
```

### ExecStartPre / ExecStartPost

Поведение как в systemd:

- `ExecStartPre` ненулевой exit code → Runner переходит в `failed`, `ExecStart` не запускается
- `ExecStartPost` ненулевой exit code → сервис переходит в `failed`, дочерний процесс останавливается
- Префикс `-` (`-/path/to/cmd`) → ошибка игнорируется, выполнение продолжается

### EnvironmentFile=

Путь должен быть **абсолютным** и не содержать компонент `..`; иначе — ошибка
запуска (префикс `-` это не отменяет). Реакция на отсутствие файла — как в systemd:

- `EnvironmentFile=/path` — файл обязателен; отсутствие → `failed`
- `EnvironmentFile=-/path` — файл опциональный; отсутствие игнорируется

### Формирование окружения сервисов

Сервисные процессы **не наследуют** окружение раннера. Окружение собирается
из `config.json` (секция `environment`) и директив юнита, в порядке возрастания
приоритета:

1. `environment.inherit` — список имён (через запятую), значения берутся из
   окружения раннера; при отсутствии поля используется фиксированный дефолтный
   список стандартных переменных Windows; `"inherit": ""` — не наследовать ничего;
2. `environment.vars` — дополнительные переменные; в значениях `%NAME%`
   подставляется значение из окружения раннера (`%%` → литеральный `%`;
   неопределённое имя → пусто; `%` без закрывающего `%` → как есть + warning);
3. `EnvironmentFile=` (unit);
4. `Environment=` (unit) — высший приоритет.

`runUnit` резолвит `environment` в `runner.EnvironmentDefaults` и передаёт
раннеру; сам раннер остаётся config-agnostic.

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

- `enabled` — сохраняется в БД; определяет автозапуск при `Start()` менеджера (старт daemon'а)
- `running` — рантайм-статус; таймер реально стоит в расписании в рамках текущей сессии

### Persistence

Файл: `C:\ProgramData\vsystemd\timers.json`

```json
{
  "foo.timer": {
    "on_calendar": ["daily"],
    "enabled": true,
    "last_run": "2026-11-09T10:00:00Z",
    "next_run": "2026-11-09T11:00:00Z",
    "active_since": "2026-11-09T09:00:00Z"
  }
}
```

Структуру файла определяет пакет `timermgr`; `storage` хранит только байты.
`last_run`, `next_run` и `active_since` хранятся и обрабатываются только в UTC (RFC3339 с `Z`).
При отображении время либо выводится в UTC с явным `Z`, либо конвертируется в
локальную TZ с указанием зоны.

Файл записывается атомарно: во временный файл в том же каталоге с последующей
заменой через `rename`, поэтому сбой в момент записи не оставляет повреждённый
`timers.json`.

### Семантика команд

| Команда | Эффект |
|---|---|
| `systemctl enable foo.timer` | `enabled=true` в БД; не запускает таймер |
| `systemctl disable foo.timer` | `enabled=false` в БД; не останавливает таймер |
| `systemctl start foo.timer` | ставит в расписание (running); не меняет `enabled`; устанавливает `active_since` |
| `systemctl stop foo.timer` | убирает из расписания; не меняет `enabled`; сбрасывает `next_run` и `active_since` |

При `Start()` менеджера: все таймеры с `enabled=true` автоматически ставятся в расписание.

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
- кастомный путь (`file:`/`append:`) должен быть **абсолютным** и быть обычным
  файловым путём: без компонент `..`/`.`, без хвостовых точек/пробелов в
  компонентах, без Alternate Data Stream (`:`), без device-путей (`\\.\`, `\\?\`)
  и без reserved-имён устройств (`CON`, `NUL`, `COM1`, ...); **родительская папка
  должна существовать** и быть каталогом (для `file:`/`append:` — доступным на
  запись), сам путь не должен указывать на каталог; иначе — ошибка запуска
  (каталоги автоматически не создаются)
- любое другое значение (`journal`, мусор) — ошибка запуска
- одинаковый канонический путь для stdout/stderr → один writer
- `file:` + `append:` на один путь → append для обоих

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

Вывод оформлен в systemd-стиле. Для `.service`:
`● unit - description`, `Loaded`, `Active` (со `since ...; ... ago`),
`Main PID`, а также `Exit code`/`Restart`/`Error` при наличии значения.
Для `.timer`: `Loaded`, `Active` (со `since ...; ... ago` при активном таймере),
`Trigger` (время следующего запуска и остаток) и `Triggers` (активируемый сервис).

После блока идёт пустая строка и последние 10 строк лога: из текущего/последнего
managed-файла `logs\<service>\`, либо из явно заданного `file:`/`append:` файла.
Для `.timer` показывается хвост лога активируемого сервиса. Ротированные файлы не
подмешиваются. При `null` для обоих потоков tail пуст.

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

Размер одного сообщения ограничен (`transport.Config.MaxMessageSize`, по умолчанию 4 MiB): строка сверх лимита рвёт соединение. Пустые и нераспознанные строки пропускаются.

### Именование peers

| PeerType | peerName | Роль |
|---|---|---|
| daemon | `daemon` | сервер, слушает `\\.\pipe\vsystemd-in` |
| runner | `foo.service` | клиент |
| control | `systemctl_<pid>` | клиент |

### Аутентификация входящих соединений

Аутентификатор внутренний (не часть публичного интерфейса) и используется демоном при приёме соединения (`clientAuthenticator`):

- клиент — managed-служба: own-process, `ServiceStartName` совпадает с SID токена, а `FILE_ID` запущенного образа совпадает с `FILE_ID` exe из `BinaryPathName` → `PeerRunner(<service>.service)` (peer несёт `ServiceConfig`);
- иначе, если токен elevated и процесс не служба → `PeerControl(systemctl_<pid>)`;
- иначе соединение отклоняется.

Общие проверки: `GetNamedPipeClientProcessId` → `OpenProcess`; время создания процесса должно быть раньше соединения (защита от переиспользования PID); токен → elevation. Из-за проверки elevation control-клиент (`systemctl`) должен запускаться elevated.

### Доверие серверу

Клиенты (runner/control) не верифицируют сервер: демон — единственный владелец pipe (`FILE_FLAG_FIRST_PIPE_INSTANCE` + ACL + ранний старт службы), доверие строится по позиции. Сквоттинг имени приводит лишь к потере сообщений/DoS (не к эскалации): легитимный демон не стартует при занятом имени, а управляющие операции `systemctl` идут напрямую в SCM.

### Request/Response

Transport реализует матчинг ответов по `request_id` в рамках соединения:

- `Req()` отправляет сообщение с уникальным `id` и ждёт ответ с `request_id` равным этому `id` на том же соединении
- поддерживает несколько одновременно ожидающих запросов
- при истечении таймаута возвращает ошибку и пишет в лог

### Логи

Логи pipe-транспорта помечаются тегом `[clientPid->serverPid]`, например `pipe: [1110->13241] sent`. `systemctl` включает диагностические логи флагом `--log [none|stdout|stderr]` (по умолчанию `none`).

---

## Конфигурация

### Расположение

Путь фиксирован: `C:\ProgramData\vsystemd\config.json`. Отсутствие файла → дефолты; переопределения пути нет.

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
