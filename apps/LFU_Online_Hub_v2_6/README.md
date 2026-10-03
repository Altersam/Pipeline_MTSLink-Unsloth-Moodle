# LFU Online Hub v2.6 — интеграция с MoodleWorker

LFU Online Hub — upstream-компонент конвейера. Его задача — собрать данные MTS Link и положить их в простой файловый контракт, который дальше обрабатывает MoodleWorker.

## Выход

```text
output/
└── <дата>/
    └── уроки/
        ├── 00_<...>/
        │   ├── Запись.txt
        │   ├── Расшифровка.txt
        │   └── files/
        ├── 01_<...>/
        └── ...
```

## Что использует MoodleWorker

Только три вида входных данных:

1. `Расшифровка.txt`
2. `Запись.txt`
3. `files/`

Технический MTS/Eljur JSON в папку урока класть не нужно.

## Известные особенности v2.6

- полная расшифровка через `GET /transcript/{transcriptId}`;
- одна/несколько ссылок записи;
- fallback на converted-records;
- EventSession + Event attachments;
- очистка старых технических файлов перед повторной выгрузкой;
- посещаемость хранится отдельно от материалов урока.

## API.txt

v2.6 использует единый `API.txt` для:
- ЭлЖур API;
- MTS API token.

Секреты не должны попадать в Git.

Минимальный пример-шаблон:

```text
https://YOUR-ELJUR-HOST/api
Vendor: YOUR_VENDOR
Ключ разработчика: YOUR_DEVKEY
Логин: SERVICE_LOGIN
Пароль: SERVICE_PASSWORD

Логин ЭлЖура
Логин: USER_LOGIN
Пароль: USER_PASSWORD

MTS_API: YOUR_MTS_LINK_TOKEN
```

Формат конкретного рабочего `API.txt` может зависеть от вашей инсталляции ЭлЖур.

## Исходные файлы v2.6

Оригинальный комплект, известный по предыдущей сборке:

```text
LFU_Online_Hub_v2_6.exe
LFU_Online_Hub_v2_6_source.zip
LFU_Online_Hub_v2_6_README.txt
LFU_Online_Hub_v2_6_main.go
LFU_Online_Hub_v2_6_engine.go
LFU_Online_Hub_v2_6_eljur_writer.go
```

В текущем сеансе метаданные и текст README доступны, но исходные raw-байты полного source bundle не удалось материализовать в контейнер. Поэтому здесь не публикуется реконструированный/неполный код. Добавьте оригинальные файлы из вашей Library без изменений.

## Сборка

Из оригинального README:

```bat
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -trimpath -ldflags="-s -w -H=windowsgui" -o LFU_Online_Hub_v2_6.exe .
```
