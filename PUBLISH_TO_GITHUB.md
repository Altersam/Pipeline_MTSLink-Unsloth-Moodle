# Публикация на GitHub

Рекомендуемое имя:

```text
Pipeline_MTSLink-Unsloth-Moodle
```

## Вариант через Git

После создания пустого репозитория на GitHub:

```powershell
git clone https://github.com/<OWNER>/Pipeline_MTSLink-Unsloth-Moodle.git
cd Pipeline_MTSLink-Unsloth-Moodle
```

Скопируйте содержимое подготовленного архива в эту папку, затем:

```powershell
git add .
git status
git commit -m "Initial MTS Link → Unsloth → Moodle pipeline"
git push origin main
```

Перед `git commit` обязательно убедитесь, что в `git status` нет:

- `API.txt`;
- `settings.json`;
- реальных расшифровок;
- MTS/Eljur tokens;
- паролей;
- student personal data.

## GitHub Pages

В репозитории уже есть:

```text
.github/workflows/pages.yml
```

После первого push:

1. GitHub → Settings → Pages.
2. Source/Build and deployment → GitHub Actions.
3. Запустить workflow `Deploy documentation to GitHub Pages`, если он не стартовал автоматически.
4. Страница будет публиковать `docs/index.html`.

## Windows artifact

Workflow:

```text
.github/workflows/build-windows.yml
```

собирает `MoodleWorker.exe` и сохраняет его как Actions Artifact.

## Release

Создайте tag:

```powershell
git tag moodleworker-v0.5
git push origin moodleworker-v0.5
```

Workflow `release.yml` соберёт Windows x64 EXE и прикрепит его к GitHub Release.

## LFU v2.6

Перед публичным push добавьте оригинальные файлы source bundle только после проверки,
что в них нет встроенных токенов/паролей.

`config/API.txt` в Git не добавлять.
