# Настройка конвейера

## 1. Компоненты

Минимально нужны:

1. Windows 10/11.
2. Папка с уроками на локальном диске / сетевом диске / YandexDisk.
3. Unsloth Studio с загруженной моделью.
4. MoodleWorker.

LFU Online Hub нужен, если материалы уроков автоматически приходят из MTS Link.

---

## 2. Unsloth Studio

Пример локального адреса:

```text
http://192.168.1.12:8888
```

MoodleWorker v0.5 умеет входить через:

```text
POST /api/auth/login
```

пользователем:

```text
unsloth
```

и использовать полученный `access_token` как Bearer для OpenAI-compatible API `/v1/*`.

Рекомендуемые поля MoodleWorker:

| Поле | Значение |
|---|---|
| Unsloth Studio URL | `http://IP:PORT` |
| Studio пользователь | `unsloth` |
| Studio пароль | пароль страницы `/login` |
| API Base URL | оставить пустым |
| Model ID | оставить пустым |
| API key | оставить пустым |
| Команда запуска Unsloth | оставить пустым, если Studio запускается отдельно |

При пустом `API Base URL` используется:

```text
<Studio URL>/v1
```

При пустом `Model ID` worker запрашивает:

```text
GET /v1/models
```

---

## 3. Запуск Studio

Пример:

```powershell
unsloth studio -H 0.0.0.0 -p 8888
```

Сброс пароля:

```powershell
unsloth studio reset-password
```

Для доступа из LAN в интерфейсе Unsloth включите:

`Settings → API keys → LAN access`

---

## 4. Папка уроков

MoodleWorker получает путь именно к каталогу `уроки`.

Правильно:

```text
Z:\YandexDisk\...\output\2026-10-03\уроки
```

Внутри него должны находиться непосредственные папки уроков.

---

## 5. Запуск MoodleWorker

Запустите `MoodleWorker.exe`.

WebUI:

```text
http://127.0.0.1:8765
```

Приложение остаётся в системном трее.

Порядок:

1. Выбрать папку уроков.
2. Ввести Studio URL/логин/пароль.
3. Нажать `Сохранить настройки`.
4. Нажать `Починить подключение`.
5. Убедиться, что в правом верхнем углу `Unsloth: online`.
6. Нажать `Старт`.

---

## 6. Skill и Prompt

В v0.5 они уже встроены в EXE.

Поля:

- `Skill .md`
- `Prompt .md`

оставляйте пустыми, пока не хотите заменить встроенные версии.

Встроенные исходники находятся:

```text
apps/MoodleWorker/resources/moodle-lesson-builder.md
apps/MoodleWorker/resources/moodle-batch-run-prompt.md
```

---

## 7. Рекомендуемые параметры worker

Для текущего локального режима:

| Параметр | Рекомендация |
|---|---:|
| Watchdog | 15 сек |
| Timeout API | 900 сек |
| API retries | 2 |
| QA fixes | 2 |
| Max input chars | 220000 |
| Vision | выключен, если изображения не нужны |
| Max images | 6 |

Если BUILD длинного урока реально занимает >15 минут, увеличьте `Timeout API`.

---

## 8. Что происходит после Start

Worker:

1. строит очередь;
2. сохраняет `moodle_batch_status.json`;
3. выбирает первый `in_progress`, иначе `queued`;
4. читает только текущий урок;
5. создаёт compact memory;
6. сбрасывает сырой контекст между этапами;
7. создаёт HTML;
8. перечитывает HTML;
9. проводит локальный и LLM QA;
10. исправляет при необходимости;
11. ставит `done` или `needs_review`;
12. переходит к следующему уроку без команды `продолжай`.
