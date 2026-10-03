# Security

## Не публиковать

- `API.txt`;
- MTS Link API token;
- ЭлЖур login/password/devkey;
- Unsloth Studio password;
- Unsloth API keys/JWT;
- `%APPDATA%\MoodleWorker\settings.json`;
- реальные записи/расшифровки, если репозиторий публичный;
- student email / eljurID / attendance raw data.

## MoodleWorker settings

Текущая версия хранит настройки в:

```text
%APPDATA%\MoodleWorker\settings.json
```

Если поле Studio password заполнено, оно является локальным секретом. Не копируйте этот файл в репозиторий.

## Unsloth

Для LAN доступа используйте доверенную локальную сеть и пароль Studio. Для публичной экспозиции используйте безопасный режим Unsloth и не публикуйте временные токены.
