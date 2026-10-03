# Архитектура

## Зачем нужен отдельный worker

Модель не должна одновременно быть:

- автором урока;
- менеджером очереди;
- watchdog;
- файловым менеджером;
- системой восстановления;
- контроллером контекста.

MoodleWorker оставляет модели только смысловую работу.

## Stateless LLM pipeline

### EXTRACT

Вход:

- расшифровка;
- запись;
- материалы;
- при необходимости существующий HTML.

Выход:

`lesson_context.json`

### PLAN

Вход:

`lesson_context.json`

Выход:

`lesson_plan.json`

### BUILD

Вход:

- compact context;
- plan;
- существующий HTML, если он улучшается.

Выход:

Moodle HTML.

### QA/FIX

Вход:

- compact context;
- HTML;
- локальные validator issues.

Выход:

QA JSON или полностью исправленный HTML.

## Почему это уменьшает context

Сырая расшифровка не переносится в BUILD/QA после создания compact memory.

Следующий урок получает новый набор API messages.

Это устойчивее, чем одна бесконечная chat-session на 17–27 уроков.
