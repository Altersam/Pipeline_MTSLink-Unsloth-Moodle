# Contributing

1. Не добавляйте реальные секреты и персональные данные.
2. Для MoodleWorker изменения должны сохранять:
   - stateless pipeline;
   - disk-backed state;
   - Moodle/WAF-safe validator;
   - Windows path semantics;
   - отсутствие рекурсивного сканирования дисков.
3. Перед PR:
   - `go test ./...` (если тесты добавлены);
   - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ...`;
   - проверить WebUI;
   - проверить 401 refresh;
   - проверить resume `in_progress`.
