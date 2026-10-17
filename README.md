# HomeBudget AI

Сервис учёта семейных расходов из [тестового задания](task.md): пользователь пишет Telegram-боту обычным текстом, а глава семьи видит и редактирует расходы в веб-панели.

Бэкенд написан на Go, данные хранятся в PostgreSQL, веб-панель собрана на React + TypeScript. Приложение и база запускаются одной командой через Docker Compose. Архитектурные решения, требования и критерии приёмки описаны в [SDD](SDD.md), REST-контракт — в [OpenAPI](docs/openapi.yaml).

## Что реализовано

- Telegram CRUD на естественном языке: создание, просмотр, изменение и удаление расходов;
- создание от двух до десяти расходов одним сообщением и одной транзакцией;
- изоляция данных: бот всегда выполняет операции только от имени отправителя;
- уточнения, выбор записи и обязательное подтверждение удаления;
- `/start`, `/help` и контекстные подсказки без обращения к LLM;
- защита от повторной обработки Telegram update и восстановление после перезапуска;
- веб CRUD всех расходов через REST API;
- фильтры по владельцу, периоду, категории и комментарию;
- точные денежные значения, SQL-агрегация и контроль конфликтов версий;
- автоматические миграции, health checks и сохранение PostgreSQL в Docker volume;
- unit- и интеграционные тесты, offline eval и отчёт настоящего live eval.

## Быстрая проверка без внешних ключей

Нужен только Docker с Compose. Из корня репозитория выполните:

```bash
docker compose up --build
```

После запуска:

1. Откройте [http://localhost:8080](http://localhost:8080).
2. Нажмите «Добавить расход».
3. Укажите любой тестовый Telegram ID, сумму, категорию, дату и комментарий.
4. Проверьте фильтры, редактирование и удаление.

Состояние приложения можно проверить отдельно:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

По умолчанию `BOT_ENABLED=false`, поэтому веб-панель и PostgreSQL запускаются без Telegram- и LLM-ключей. Обычный `docker compose down` сохраняет данные. Команда `docker compose down -v` также удаляет локальный volume с расходами.

## Полная проверка Telegram-бота

Для живого сценария нужны:

- токен тестового бота, созданного бесплатно через `@BotFather`;
- OpenAI-compatible LLM с поддержкой `POST /chat/completions` и function tools.

Скопируйте пример конфигурации:

```powershell
Copy-Item .env.example .env
```

Для Linux/macOS:

```bash
cp .env.example .env
```

Заполните в `.env`:

```dotenv
BOT_ENABLED=true
TELEGRAM_BOT_TOKEN=<telegram bot token>
LLM_BASE_URL=<OpenAI-compatible endpoint>
LLM_MODEL=<model with tool calling>
LLM_API_KEY=<provider API key>
FAMILY_USER_IDS=
```

`FAMILY_USER_IDS` можно оставить пустым: тогда бот принимает личные сообщения от любого пользователя. Для закрытой проверки укажите разрешённые Telegram ID через запятую.

Запустите систему:

```bash
docker compose up --build
```

Проверить доступность Telegram и LLM без записи расходов:

```bash
docker compose run --rm --no-deps --entrypoint /usr/local/bin/smoke app -telegram -llm
```

### Демонстрационный сценарий

Отправьте боту по очереди:

```text
/start
кофе 350
такси 1200 вчера
кофе 100 и продукты 1500
покажи траты за неделю
сколько потратил на еду в этом месяце
последнее было не 1500, а 1450
удали такси
да, удали
```

Бот должен:

1. создать расходы и самостоятельно определить категории и даты;
2. вернуть список и точную сумму из PostgreSQL;
3. изменить выбранную запись;
4. запросить подтверждение перед удалением.

Затем откройте [http://localhost:8080](http://localhost:8080) и нажмите «Обновить»: операции из Telegram появятся в общей панели.

Один Telegram-токен нельзя одновременно использовать в двух экземплярах long polling. Если этот бот уже запущен на другом компьютере, сначала остановите тот экземпляр. Настроенный webhook также нужно предварительно отключить.

## Автоматические проверки

### Unit-тесты и статический анализ

Команда собирает Go-приложение, запускает все unit-тесты и `go vet`:

```bash
docker build --target test -t homebudget-test .
```

Полная сборка production-образа дополнительно проверяет frontend build:

```bash
docker build -t homebudget-ai .
```

### Clean start

Проверка создаёт отдельный Compose-проект на порту `18080`, выполняет REST CRUD, фильтры, пагинацию и расчёт итогов для двух владельцев, перезапускает приложение и PostgreSQL и проверяет сохранность данных. Основная локальная база не затрагивается.

```powershell
./scripts/clean-start.ps1
```

### Интеграционные тесты и offline eval

Команды ниже используют отдельную временную PostgreSQL и не расходуют LLM-токены:

```powershell
docker compose --profile eval up -d eval-db
$evalDb = docker compose --profile eval ps -q eval-db
docker run --rm --network "container:$evalDb" `
  -e TEST_DATABASE_URL="postgres://expenses:expenses@127.0.0.1:5432/expenses_eval?sslmode=disable" `
  -v "${PWD}:/src" -w /src golang:1.25-alpine `
  go test -p 1 -tags integration ./internal/storage/postgres ./internal/appbot
docker compose --profile eval rm -sf eval-db
```

Offline eval включает десять детерминированных сценариев с scripted provider. Он проверяет orchestration, ограничения доступа и итоговое состояние PostgreSQL, но не выдаётся за проверку качества настоящей модели.

### Live eval

Live eval использует настроенную в `.env` LLM, отдельную временную PostgreSQL и синтетические данные. Telegram для него не нужен.

```powershell
$env:LIVE_EVAL_COMMIT = (git rev-parse HEAD)
docker compose --profile eval run --rm --build live-eval
docker compose --profile eval rm -sf eval-db
```

Runner выполняет 19 сценариев по три раза и записывает модель, commit, версию prompt, результат каждого прогона, tool calls, token usage и задержки в [evals/reports/latest.json](evals/reports/latest.json). Итоговый сохранённый прогон: **57 из 57 сценариев пройдены**. Ключи, DSN и реальные пользовательские данные в отчёт не попадают.

Live eval выполняет платные запросы провайдеру. Для точечной проверки можно временно указать, например:

```powershell
$env:LIVE_EVAL_CASE_IDS = "E04,E05,E21"
$env:LIVE_EVAL_REPEATS = "1"
```

## Конфигурация

| Переменная | Назначение | Значение по умолчанию |
|---|---|---|
| `BOT_ENABLED` | запустить Telegram polling | `false` |
| `TELEGRAM_BOT_TOKEN` | токен Telegram-бота | пусто |
| `FAMILY_USER_IDS` | разрешённые Telegram ID через запятую | пусто, разрешены все |
| `LLM_BASE_URL` | OpenAI-compatible API | `https://api.openai.com/v1` |
| `LLM_MODEL` | модель с tool calling | пусто |
| `LLM_API_KEY` | ключ LLM-провайдера | пусто |
| `APP_TIMEZONE` | календарная зона расходов | `Europe/Moscow` |
| `HTTP_PORT` | локальный порт веб-панели | `8080` |

Дополнительные лимиты и таймауты перечислены в [.env.example](.env.example). Локальный `.env` исключён из Git.

## REST API

Основные маршруты:

- `GET /api/v1/meta`, `GET /api/v1/users`;
- `GET /api/v1/expenses`, `GET /api/v1/expenses/summary`;
- `POST /api/v1/expenses`;
- `GET`, `PATCH`, `DELETE /api/v1/expenses/{id}`.

Для списка доступны `user_id`, `category`, `date_from`, `date_to`, `query`, `limit` и `offset`. Суммы, ID и версии передаются JSON-строками, чтобы не терять точность. PATCH и DELETE требуют актуальный `expected_version`.

## Важные ограничения

- Веб-панель намеренно не имеет авторизации, как и требуется в задании, поэтому Compose публикует её только на `127.0.0.1`.
- Обновления из Telegram появляются в уже открытой панели после нажатия «Обновить» или перезагрузки страницы.
- Один business worker последовательно обрабатывает диалоги; это осознанное упрощение для семейной нагрузки.
- Модель должна поддерживать Chat Completions tools. Провайдер только с Responses API без совместимого `/chat/completions` не подойдёт.

## Использование ИИ

ИИ-ассистент использовался для разбора требований, проектирования, проверки граничных сценариев и части реализации. Все изменения проверены компилятором, тестами, статическим анализом, контейнерной сборкой и воспроизводимыми eval-сценариями. Секреты и реальные пользовательские расходы в репозиторий не добавлялись.
