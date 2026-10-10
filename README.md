# Demon Engine

![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)
![Linux](https://img.shields.io/badge/Linux-Ubuntu%20VPS-E95420?logo=ubuntu&logoColor=white)
![Status](https://img.shields.io/badge/Status-Pet%20Project-blue)

**Demon Engine** — автономный Go-сервис, который регулярно собирает материалы из внешних источников, отбирает подходящие темы, преобразует их с помощью LLM и публикует готовый контент в Telegram. Сервис также поддерживает лид-магнит с проверкой подписки и выдачей PDF.



## Задача проекта

Ручная подготовка технических публикаций требует времени на мониторинг нескольких источников, оценку актуальности, редактуру, подбор обложки и форматирование под Telegram. Demon Engine автоматизирует большую часть этого процесса: от сбора ссылок до публикации и последующего обсуждения.

## Архитектура и поток данных

```text
┌─────────────────────────┐       ┌─────────────────────────┐
│ Habr RSS: Go, DevOps,   │       │ Hacker News API         │
│ AI, карьера, OSS        │       │ top stories + items     │
└────────────┬────────────┘       └────────────┬────────────┘
             └────────────────┬─────────────────┘
                              ▼
                  ┌───────────────────────┐
                  │ Сбор и фильтрация     │
                  │ cache.txt             │
                  └───────────┬───────────┘
                              ▼
                  ┌───────────────────────┐
                  │ Оценка и выбор статьи │
                  │ Habr: параллельный    │
                  │ запрос статистики     │
                  └───────────┬───────────┘
                              ▼
                  ┌───────────────────────┐
                  │ VseGPT Chat API       │
                  │ генерация JSON-контента│
                  └───────────┬───────────┘
                              ▼
                 ┌──────────────────────────┐
                 │ Редактура и определение  │
                 │ режима публикации        │
                 └───────────┬──────────────┘
                             │
               ┌─────────────┴──────────────┐
               ▼                            ▼
   ┌───────────────────────┐    ┌────────────────────────┐
   │ Короткий Telegram-пост│    │ Telegra.ph: полная     │
   │ + текст отдельным msg │    │ статья + анонс         │
   └─────────────┬─────────┘    └────────────┬───────────┘
                 └──────────────┬─────────────┘
                                ▼
               ┌──────────────────────────────┐
               │ Изображение: og:image либо    │
               │ Pollinations → watermark.png  │
               └──────────────┬───────────────┘
                              ▼
                    ┌─────────────────────┐
                    │ Telegram Bot API    │
                    │ канал и обсуждение  │
                    └─────────────────────┘
```

Параллельные задачи вокруг основного потока: Telegram long polling для команд/лид-магнита и отложенный комментарий к опубликованному посту.

## Ключевые возможности

### Мониторинг и отбор

- Читает RSS-ленты Habr по направлениям Go, DevOps, AI, карьера и Open Source.
- Получает top stories Hacker News и запрашивает данные отдельных материалов.
- Исключает ссылки, сохраненные в `cache.txt`.
- Параллельно запрашивает статистику Habr и выбирает кандидата с наибольшей оценкой.
- При пустом кэше прогревает его текущей лентой, не отправляя всю ленту как новые публикации.

### Генерация и публикация

- Отправляет контекст статьи и редакционный system prompt в OpenAI-compatible API VseGPT.
- Проверяет и очищает JSON-ответ модели перед декодированием.
- Повторяет неудачный запрос до трех раз.
- Выбирает прямой режим для обычных материалов или Telegra.ph для англоязычных/длинных материалов.
- Создает страницу Telegra.ph и публикует в Telegram анонс со ссылкой, если создание страницы прошло успешно.
- Ищет обложку через `og:image`; при неудаче пытается получить иллюстрацию от Pollinations.
- Накладывает прозрачный PNG-watermark в правый нижний угол перед отправкой изображения.
- Через 90 секунд асинхронно создает комментарий-адвокат в группе обсуждения.

### Telegram-бот и лид-магнит

- Получает обновления Telegram через long polling.
- Принимает `/logs` и `/force` от администратора.
- Обрабатывает запрос PDF-мануала, проверяет членство в канале через `getChatMember` и выдает `data/Go_AI_Dev_Pack.pdf`.
- Отправляет администратору уведомления о новых пользователях и успешной выдаче PDF.

### Планировщик и конкурентность

- При старте выполняет одну попытку публикации, затем планирует публикации по расписанию.
- Параллельно запускает listener Telegram и стартовую публикацию через goroutines.
- Оценка Habr-статей также выполняется конкурентно с синхронизацией результатов.
- Расписание задано в UTC в `postScheduleUTC`: часы `07:00`, `12:00`, `17:00`, отображаемые как `10:00`, `15:00`, `20:00` по Москве.


## Инженерные решения

- **Стандартные интерфейсы HTTP/JSON** для Telegram Bot API и OpenAI-compatible API; для RSS используется `encoding/xml`.
- **Параллельная обработка** рейтинга источников и фоновых Telegram-задач через goroutines, `sync.WaitGroup` и mutex.
- **Повторные попытки** генерации текста с задержкой между запросами.
- **Защита от повторной публикации** через локальный файл кэша ссылок.
- **Деградация без падения** для необязательного watermark и изображения: если watermark отсутствует, картинка отправляется без него; если обложка недоступна, публикационный поток может продолжиться текстом.
- **Логи в файл и stdout**: события приложения пишутся в `bot.log` и стандартный вывод, доступный systemd journal. Формат сейчас человекочитаемый текстовый, не JSON structured logging.
- **Управление процессом через systemd**: unit-файл может обеспечить автозапуск после перезагрузки и автоматический рестарт процесса.

## Технологический стек

| Область | Технологии |
|---|---|
| Язык и runtime | Go 1.22+, goroutines, `net/http`, `encoding/json`, `encoding/xml` |
| Telegram | Telegram Bot API, long polling, Bot API methods `getUpdates`, `getChatMember`, `sendMessage`, `sendDocument`, `sendPhoto` |
| Генерация текста | VseGPT, OpenAI-compatible Chat Completions API |
| Полные публикации | Telegra.ph API, Instant View |
| Источники | Habr RSS/API, Hacker News Firebase API |
| Обложки | Open Graph `og:image`, Pollinations, Go `image`/`image/draw` |
| Хранение состояния | Локальный `cache.txt`, `bot.log`, PDF и PNG assets |
| Деплой | Ubuntu/Linux VPS, systemd, SSH/SCP; Dockerfile и Docker Compose также присутствуют |
| Тестирование | Go `testing` |

## Требования

- Go **1.22+** для сборки из исходников.
- Linux VPS (инструкция ниже написана для Ubuntu) для постоянной работы.
- Исходящие HTTPS-запросы к Telegram, VseGPT, источникам контента, Pollinations и Telegra.ph.
- Telegram bot token, ID администратора и настроенные API-ключи.
- Права бота, достаточные для публикации в канале, отправки личных сообщений пользователям и проверки подписки.
- `watermark.png` в рабочей директории (необязателен для запуска).
- `data/Go_AI_Dev_Pack.pdf` для выдачи лид-магнита.

> **Секреты:** в текущем `main.go` некоторые токены/ключи заданы константами. Не публикуйте такие значения в портфолио или Git-репозитории. Перед открытым размещением замените их на новые и вынесите чтение секретов в переменные окружения или secret manager. Предложенный ниже `EnvironmentFile` не заменяет секреты сам по себе: текущая версия приложения не читает все эти параметры из окружения без соответствующего изменения кода.

## Локальная сборка и запуск

Запускайте команды из корня проекта, так как приложение использует относительные пути к `cache.txt`, `bot.log`, `watermark.png` и PDF.

```bash
go mod download
go test ./...
go build -o bot_daemon .
./bot_daemon
```

Остановить процесс можно `Ctrl+C`. После старта приложение **сразу запускает публикационный проход**, а затем ждет следующего расписания.

Запускайте только один экземпляр бота на один Telegram bot token: параллельный polling локально и на VPS может привести к `409 Conflict`.

## Деплой в Ubuntu/Linux через systemd

Ниже приведен пример установки бинарника в `/opt/demon-engine`. Он предполагает архитектуру VPS `linux/amd64`, совпадающую с текущим `deploy.sh`. Для ARM-сервера измените `GOARCH`.

### 1. Соберите бинарник

На локальной машине в корне проекта:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bot_daemon .
```

### 2. Создайте системного пользователя и директорию на VPS

Подключитесь к Ubuntu-серверу и выполните:

```bash
sudo useradd --system --home-dir /opt/demon-engine --shell /usr/sbin/nologin demon-engine
sudo install -d -o demon-engine -g demon-engine -m 0750 /opt/demon-engine
sudo install -d -o demon-engine -g demon-engine -m 0750 /opt/demon-engine/data
```

### 3. Передайте приложение и обязательные assets

С локальной машины из корня проекта:

```bash
scp bot_daemon user@YOUR_SERVER:/tmp/bot_daemon
scp watermark.png user@YOUR_SERVER:/tmp/watermark.png
scp data/Go_AI_Dev_Pack.pdf user@YOUR_SERVER:/tmp/Go_AI_Dev_Pack.pdf
```

Затем на VPS:

```bash
sudo install -o demon-engine -g demon-engine -m 0750 /tmp/bot_daemon /opt/demon-engine/bot_daemon
sudo install -o demon-engine -g demon-engine -m 0640 /tmp/watermark.png /opt/demon-engine/watermark.png
sudo install -o demon-engine -g demon-engine -m 0640 /tmp/Go_AI_Dev_Pack.pdf /opt/demon-engine/data/Go_AI_Dev_Pack.pdf
```

`cache.txt` и `bot.log` создаются приложением относительно `WorkingDirectory`; процессу нужно разрешить запись в `/opt/demon-engine`.

### 4. Создайте systemd unit

Создайте `/etc/systemd/system/demon-engine.service`:

```ini
[Unit]
Description=Demon Engine Telegram Publishing Bot
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=demon-engine
Group=demon-engine
WorkingDirectory=/opt/demon-engine
ExecStart=/opt/demon-engine/bot_daemon
Restart=on-failure
RestartSec=10
TimeoutStopSec=30
KillSignal=SIGINT

# Приложению нужны права записи для bot.log и cache.txt в рабочей директории.
# После выноса секретов из исходника добавьте EnvironmentFile с ограниченными правами.
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=/opt/demon-engine

[Install]
WantedBy=multi-user.target
```

### 5. Включите автозапуск и проверьте сервис

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now demon-engine.service
sudo systemctl status demon-engine.service --no-pager
```

Просмотр логов:

```bash
sudo journalctl -u demon-engine.service -f
```

Хотя приложение пишет в `bot.log` и stdout, журнал systemd получает вывод процесса через stdout/stderr. Для обновления бинарника загрузите новую версию в `/opt/demon-engine/bot_daemon`, затем выполните:

```bash
sudo systemctl restart demon-engine.service
```

### Особенность существующего `deploy.sh`

Текущий `deploy.sh` использует `root@YOUR_SERVER_IP`, копирует только бинарник в `/root/bot_daemon` и управляет уже существующим сервисом с именем `tgbot`. Скрипт не переносит `watermark.png` или PDF и не устанавливает unit-файл. Пример выше — отдельный рекомендуемый вариант с отдельным системным пользователем; перед использованием адаптируйте скрипт деплоя под выбранные пути и имя сервиса.

## Файлы проекта

```text
.
├── main.go                 # планировщик, источники, генерация, Telegram и изображения
├── telegraph.go            # создание и очистка контента для Telegra.ph
├── main_test.go            # unit-тесты
├── test_telegraph.go       # вспомогательная проверка Telegra.ph
├── go.mod / go.sum         # Go module и зависимости
├── watermark.png           # необязательный PNG watermark
├── data/
│   └── Go_AI_Dev_Pack.pdf  # лид-магнит
├── Dockerfile
├── docker-compose.yml
└── deploy.sh               # текущий VPS deployment script
```

## Качество и дальнейшее развитие

Перед изменением кода запускайте:

```bash
gofmt -w *.go
go test ./...
go vet ./...
```

Приоритетные улучшения для дальнейшей инженерной работы:

1. Вынести секреты и конфигурацию в environment variables; не хранить действующие ключи в исходном коде.
2. Реализовать и протестировать Smart Chunking, если необходимо публиковать длинный текст несколькими Telegram-сообщениями.
3. Добавить таймауты и единый HTTP client для внешних API, обработку HTTP-кодов и наблюдаемость по каждому этапу.
4. Персистентно хранить статус уведомленных пользователей, если дедупликация должна сохраняться после перезапуска.
5. Добавить интеграционные тесты генерации, изображений и Telegram API с тестовым HTTP-сервером.
6. Обновить `deploy.sh`, чтобы он безопасно разворачивал assets и совпадал с unit-файлом systemd.
