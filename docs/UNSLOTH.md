# Unsloth Studio

## Проверенная схема MoodleWorker v0.5

MoodleWorker работает с Unsloth Studio как с защищённым OpenAI-compatible сервером.

### Studio login

Worker выполняет:

```text
POST /api/auth/login
Content-Type: application/json

{
  "username": "unsloth",
  "password": "..."
}
```

Из ответа берётся `access_token`.

### OpenAI-compatible API

Далее Bearer token используется для:

```text
GET /v1/models
POST /v1/chat/completions
```

При `HTTP 401` токен сбрасывается и логин выполняется заново.

## Почему API key можно не заполнять

Для локальной Studio password/JWT-схемы отдельный API key не обязателен.

В v0.5 Studio password имеет приоритет над старым/просроченным API key.

## Почему лучше не использовать trycloudflare для локального worker

Quick Tunnel URL временный и может измениться/истечь.

Если Studio доступен по LAN:

```text
http://192.168.1.12:8888
```

используйте именно LAN URL.

## 404

Ошибка:

```text
HTTP 404 {"detail":"API endpoint not found"}
```

обычно означает неправильный Base URL.

OpenAI-compatible endpoints находятся под:

```text
/v1
```

## 401

Ошибка:

```text
Invalid or expired API key
```

означает устаревшую авторизацию.

В v0.5 нажмите `Починить подключение`. Worker очистит старый API key/Cloudflare URL и войдёт по Studio password.

## Пароль

Сброс:

```powershell
unsloth studio reset-password
```

Официальные ссылки:

- https://github.com/unslothai/unsloth
- https://unsloth.ai/
