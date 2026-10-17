# HomeBudget AI

Сервис учёта семейных расходов из тестового задания: административная веб-панель с REST API и Telegram-бот, который выполняет операции через OpenAI-compatible tool calling. Реализация ведётся по [SDD](SDD.md), исходные требования сохранены в [task.md](task.md).

## Что уже работает

- полный CRUD расходов через веб-панель и `/api/v1`;
- фильтры по владельцу, категории, периоду и тексту комментария;
- общая сумма и разбиение по категориям, рассчитанные в PostgreSQL;
- owner-scoped сервис для будущего Telegram-бота: чужая запись выглядит как отсутствующая;
- защита редактирования и удаления через версию записи (`409 version_conflict`);
- точные денежные значения без `float64`, календарные даты в `APP_TIMEZONE`;
- автоматические versioned-миграции, readiness/liveness endpoints;
- Docker Compose и сохранение PostgreSQL в named volume;
- отдельные smoke-клиенты Telegram `getMe` и OpenAI-compatible tool calling.
- durable Telegram inbox: update и polling offset фиксируются одной транзакцией;
- шесть owner-scoped tools, лимиты хода и строгий разбор аргументов;
- атомарное сохранение мутации и готового ответа, отдельные повторы доставки;
- подтверждение удаления серверным кодом без повторного вызова модели.
- восстановление незавершённого уточнения после рестарта и TTL для устаревшего состояния.
- выбор одной записи из нескольких по сохранённому порядку без повторного вызова модели;
- до шести предыдущих пар диалога с автоматическим сокращением контекста.

## Быстрый запуск

Требуется Docker с Compose:

```bash
docker compose up --build
```

Откройте [http://localhost:8080](http://localhost:8080). Панель позволяет ввести новый Telegram ID, поэтому заранее создавать пользователя не нужно.

Проверка процесса и базы:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

PostgreSQL хранит данные в named volume `expenses_data`. Обычный `docker compose down` сохраняет данные; `docker compose down -v` удаляет локальную базу.

## REST API

Основные маршруты:

- `GET /api/v1/meta`, `GET /api/v1/users`;
- `GET /api/v1/expenses` и `GET /api/v1/expenses/summary`;
- `GET`, `PATCH`, `DELETE /api/v1/expenses/{id}`;
- `POST /api/v1/expenses`.

Доступны параметры `user_id`, `category`, `date_from`, `date_to`, `query`, а для списка — `limit` и `offset`. ID, версии и суммы передаются строками, чтобы не терять точность на JSON-границах. PATCH и DELETE требуют актуальный `expected_version`.

Полный контракт доступен в [OpenAPI](docs/openapi.yaml), а решения по безопасности описаны в [SDD](SDD.md#6-rest-api).

## Конфигурация

Для веб CRUD дополнительных секретов не требуется. Скопируйте `.env.example` в `.env`, когда понадобится проверить внешние интеграции:

- `TELEGRAM_BOT_TOKEN` — токен отдельного тестового бота;
- `LLM_BASE_URL` — базовый URL OpenAI-compatible API;
- `LLM_MODEL` — модель с tool calling;
- `LLM_API_KEY` — ключ провайдера, если endpoint его требует;
- `FAMILY_USER_IDS` — необязательный CSV разрешённых Telegram ID.

Секреты из `.env` исключены из Git.

Для запуска бота установите `BOT_ENABLED=true`, заполните `TELEGRAM_BOT_TOKEN`, `LLM_MODEL` и при необходимости `LLM_API_KEY`/`LLM_BASE_URL`. При настроенном Telegram webhook приложение завершится с ошибкой: оно использует только long polling и не удаляет чужую конфигурацию автоматически. `FAMILY_USER_IDS` ограничивает доступ списком Telegram ID; пустое значение разрешает любые личные чаты.

## Проверки

Unit-тесты и статическая проверка Go:

```bash
docker run --rm -v "${PWD}:/src" -w /src golang:1.25-alpine go test ./...
docker run --rm -v "${PWD}:/src" -w /src golang:1.25-alpine go vet ./...
```

Сборка веб-панели:

```bash
docker run --rm -v "${PWD}/web:/app" -w /app node:24-alpine npm ci
docker run --rm -v "${PWD}/web:/app" -w /app node:24-alpine npm run build
```

После запуска Compose интеграционный тест PostgreSQL выполняется внутри его сети:

```bash
docker run --rm --network test-task-expenses_default -e TEST_DATABASE_URL="postgres://expenses:expenses@db:5432/expenses?sslmode=disable" -v "${PWD}:/src" -w /src golang:1.25-alpine go test -p 1 -tags integration ./internal/storage/postgres ./internal/appbot
```

Отдельный offline eval использует scripted provider и не расходует токены внешней модели. Девять фиксированных сценариев находятся в `evals/offline_cases.json`:

```bash
docker run --rm --network test-task-expenses_default -e TEST_DATABASE_URL="postgres://expenses:expenses@db:5432/expenses?sslmode=disable" -v "${PWD}:/src" -w /src golang:1.25-alpine go test -p 1 -tags integration ./internal/appbot -run TestOfflineAgentEval -v
```

Clean-start проверка поднимает отдельный Compose-проект на порту `18080`, создаёт расход, перезапускает PostgreSQL и приложение, проверяет сохранность записи и удаляет только свои временные контейнеры и volume:

```powershell
./scripts/clean-start.ps1
```

### Live eval

Live eval не использует Telegram и запускается с отдельной временной PostgreSQL. Нужен OpenAI-compatible endpoint с поддержкой `POST /chat/completions` и function tools. Заполните в локальном `.env` только `LLM_BASE_URL`, `LLM_MODEL` и при необходимости `LLM_API_KEY`, затем выполните:

```powershell
$env:LIVE_EVAL_COMMIT = (git rev-parse HEAD)
docker compose --profile eval run --rm --build live-eval
docker compose --profile eval rm -sf eval-db
```

Runner выполняет 19 языковых сценариев по три раза. Отчёт сохраняется в `evals/reports/latest.json`: модель, commit, версия prompt, результат каждого прогона, tool calls, token usage и задержки p50/p95. Промпты, ключи и DSN в отчёт не попадают. Обычная рабочая БД и Telegram polling offset не затрагиваются.

Если выбранная модель поддерживает tools только через Responses API, она не совместима с текущим OpenAI-compatible Chat Completions адаптером — для тестового задания следует указать модель провайдера, которая поддерживает `/chat/completions` с `tools`.

Smoke-команда внешних интеграций не пишет в базу и не отправляет сообщения:

```bash
docker compose run --rm --entrypoint /usr/local/bin/smoke app -telegram -llm
```

## Архитектурные решения

Денежные значения хранятся в Go как целые копейки, в PostgreSQL — `NUMERIC(12,2)`. `spent_at` — календарная дата. Административный сервис доступен только REST-адаптеру, а Telegram получит отдельный owner-scoped сервис, который принудительно подставляет ID владельца на каждой операции.

Веб-панель намеренно работает без авторизации: это ограничение сформулировано в тестовом задании. Поэтому Compose публикует её только на `127.0.0.1`; для публичного размещения потребуется аутентификация и TLS.

## Использование ИИ

ИИ-ассистент помогал разбирать требования, проектировать SDD, находить граничные сценарии и писать часть реализации. Все предложенные изменения проверяются компилятором, тестами, статическим анализом, сборкой контейнеров и ручными сквозными сценариями; секреты и реальные сообщения в запросы не передаются.
