package main

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	AppName = "MoodleWorker"
	Host    = "127.0.0.1"
	Port    = 8765
)

//go:embed resources/*
var embeddedFS embed.FS

// ---------------- Settings ----------------

type Settings struct {
	RootPath             string  `json:"root_path"`
	SkillPath            string  `json:"skill_path"`
	PromptPath           string  `json:"prompt_path"`
	StudioURL            string  `json:"studio_url"`
	StudioUsername       string  `json:"studio_username"`
	StudioPassword       string  `json:"studio_password"`
	UnslothBaseURL       string  `json:"unsloth_base_url"`
	UnslothAPIKey        string  `json:"unsloth_api_key"`
	Model                string  `json:"model"`
	UnslothStartCommand  string  `json:"unsloth_start_command"`
	LaunchHidden         bool    `json:"launch_hidden"`
	WatchdogIntervalSec  int     `json:"watchdog_interval_sec"`
	LaunchCooldownSec    int     `json:"launch_cooldown_sec"`
	RequestTimeoutSec    int     `json:"request_timeout_sec"`
	MaxAPIRetries        int     `json:"max_api_retries"`
	MaxQAFixes           int     `json:"max_qa_fixes"`
	MaxInputChars        int     `json:"max_input_chars"`
	MaxExistingHTMLChars int     `json:"max_existing_html_chars"`
	AnalyzeMaxTokens     int     `json:"analyze_max_tokens"`
	PlanMaxTokens        int     `json:"plan_max_tokens"`
	BuildMaxTokens       int     `json:"build_max_tokens"`
	QAMaxTokens          int     `json:"qa_max_tokens"`
	FixMaxTokens         int     `json:"fix_max_tokens"`
	TemperatureAnalyze   float64 `json:"temperature_analyze"`
	TemperaturePlan      float64 `json:"temperature_plan"`
	TemperatureBuild     float64 `json:"temperature_build"`
	TemperatureQA        float64 `json:"temperature_qa"`
	TemperatureFix       float64 `json:"temperature_fix"`
	AutoResume           bool    `json:"auto_resume"`
	AutoOpenBrowser      bool    `json:"auto_open_browser"`
	VisionEnabled        bool    `json:"vision_enabled"`
	MaxImagesPerLesson   int     `json:"max_images_per_lesson"`
	ExtraInstructions    string  `json:"extra_instructions"`
}

func defaultSettings() Settings {
	return Settings{
		StudioURL:            "http://192.168.1.12:8888",
		StudioUsername:       "unsloth",
		UnslothBaseURL:       "",
		LaunchHidden:         true,
		WatchdogIntervalSec:  15,
		LaunchCooldownSec:    90,
		RequestTimeoutSec:    900,
		MaxAPIRetries:        2,
		MaxQAFixes:           2,
		MaxInputChars:        220000,
		MaxExistingHTMLChars: 100000,
		AnalyzeMaxTokens:     10000,
		PlanMaxTokens:        7000,
		BuildMaxTokens:       30000,
		QAMaxTokens:          5000,
		FixMaxTokens:         30000,
		TemperatureAnalyze:   0.25,
		TemperaturePlan:      0.2,
		TemperatureBuild:     0.35,
		TemperatureQA:        0.1,
		TemperatureFix:       0.2,
		AutoResume:           true,
		AutoOpenBrowser:      true,
		VisionEnabled:        false,
		MaxImagesPerLesson:   6,
	}
}

func appDataDir() string {
	root := os.Getenv("APPDATA")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = home
	}
	p := filepath.Join(root, AppName)
	_ = os.MkdirAll(p, 0755)
	return p
}

func settingsPath() string { return filepath.Join(appDataDir(), "settings.json") }

func loadSettings() Settings {
	s := defaultSettings()
	b, err := os.ReadFile(settingsPath())
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	// v0.4 migration: older builds knew only the API Base URL.
	// For this workstation prefer the reachable LAN Studio address.
	if strings.TrimSpace(s.StudioURL) == "" {
		s.StudioURL = "http://192.168.1.12:8888"
	}
	if strings.TrimSpace(s.StudioUsername) == "" {
		s.StudioUsername = "unsloth"
	}
	if strings.Contains(s.UnslothBaseURL, "172.18.0.1:8888") {
		s.UnslothBaseURL = ""
	}
	// v0.5: temporary trycloudflare URLs expire and must not override a reachable LAN Studio.
	if strings.Contains(strings.ToLower(s.UnslothBaseURL), "trycloudflare.com") && strings.TrimSpace(s.StudioURL) != "" {
		s.UnslothBaseURL = ""
	}
	return s
}

func saveSettings(s Settings) error { return atomicWriteJSON(settingsPath(), s) }

func atomicWriteJSON(path string, obj any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	var check any
	tb, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(tb, &check); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func readEmbeddedOrFile(path, embeddedName string) (string, error) {
	if strings.TrimSpace(path) != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	b, err := embeddedFS.ReadFile(embeddedName)
	return string(b), err
}

// ---------------- Runtime ----------------

type Totals struct {
	Total       int `json:"total"`
	Done        int `json:"done"`
	Queued      int `json:"queued"`
	InProgress  int `json:"in_progress"`
	Failed      int `json:"failed"`
	NeedsReview int `json:"needs_review"`
}

type RuntimeState struct {
	mu                sync.Mutex
	RunState          string   `json:"run_state"`
	APIOnline         bool     `json:"api_online"`
	APIError          string   `json:"api_error"`
	ModelResolved     string   `json:"model_resolved"`
	CurrentLesson     string   `json:"current_lesson"`
	CurrentStage      string   `json:"current_stage"`
	LastProgressTs    int64    `json:"last_progress_ts"`
	LastProgressText  string   `json:"last_progress_text"`
	WorkerAlive       bool     `json:"worker_alive"`
	UnslothProcessPID int      `json:"unsloth_process_pid,omitempty"`
	Totals            Totals   `json:"totals"`
	Logs              []string `json:"logs"`
}

func newRuntime() *RuntimeState {
	return &RuntimeState{RunState: "idle", CurrentStage: "idle", Logs: make([]string, 0, 500)}
}
func (r *RuntimeState) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Logs = append(r.Logs, line)
	if len(r.Logs) > 500 {
		r.Logs = r.Logs[len(r.Logs)-500:]
	}
	r.LastProgressTs = time.Now().Unix()
	r.LastProgressText = msg
}
func (r *RuntimeState) progress(stage, lesson string) {
	r.mu.Lock()
	r.CurrentStage = stage
	if lesson != "" {
		r.CurrentLesson = lesson
	}
	r.mu.Unlock()
	if lesson != "" {
		r.logf("%s: %s", lesson, stage)
	} else {
		r.logf("%s", stage)
	}
}
func (r *RuntimeState) snapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	logs := append([]string(nil), r.Logs...)
	if len(logs) > 120 {
		logs = logs[len(logs)-120:]
	}
	return map[string]any{"run_state": r.RunState, "api_online": r.APIOnline, "api_error": r.APIError, "model_resolved": r.ModelResolved, "current_lesson": r.CurrentLesson, "current_stage": r.CurrentStage, "last_progress_ts": r.LastProgressTs, "last_progress_text": r.LastProgressText, "worker_alive": r.WorkerAlive, "unsloth_process_pid": r.UnslothProcessPID, "totals": r.Totals, "logs": logs}
}

// ---------------- Status store ----------------

type LessonRecord struct {
	Index        int    `json:"index"`
	Folder       string `json:"folder"`
	Status       string `json:"status"`
	Attempts     int    `json:"attempts"`
	Output       string `json:"output,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	ErrorType    string `json:"error_type,omitempty"`
	Error        string `json:"error,omitempty"`
	ReviewReason string `json:"review_reason,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type BatchStatus struct {
	Root      string          `json:"root"`
	Total     int             `json:"total"`
	Current   int             `json:"current"`
	UpdatedAt string          `json:"updated_at"`
	Lessons   []*LessonRecord `json:"lessons"`
}

func statusPath(root string) string   { return filepath.Join(root, "moodle_batch_status.json") }
func pipelinePath(root string) string { return filepath.Join(root, "moodle_pipeline_state.json") }
func summaryPath(root string) string  { return filepath.Join(root, "moodle_batch_summary.json") }

var prefixRE = regexp.MustCompile(`^(\d+)(?:_|\b)`)

func lessonFolders(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	type item struct {
		n    int
		name string
	}
	items := []item{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := prefixRE.FindStringSubmatch(e.Name())
		if len(m) < 2 {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		items = append(items, item{n, e.Name()})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].n == items[j].n {
			return strings.ToLower(items[i].name) < strings.ToLower(items[j].name)
		}
		return items[i].n < items[j].n
	})
	out := make([]string, len(items))
	for i, x := range items {
		out[i] = x.name
	}
	return out, nil
}
func loadStatus(root string) BatchStatus {
	var st BatchStatus
	b, err := os.ReadFile(statusPath(root))
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}
func saveStatus(root string, st *BatchStatus) error {
	st.UpdatedAt = time.Now().Format(time.RFC3339)
	return atomicWriteJSON(statusPath(root), st)
}
func reconcileStatus(root string) (*BatchStatus, error) {
	folders, err := lessonFolders(root)
	if err != nil {
		return nil, err
	}
	old := loadStatus(root)
	om := map[string]*LessonRecord{}
	for _, r := range old.Lessons {
		om[r.Folder] = r
	}
	ls := make([]*LessonRecord, 0, len(folders))
	for i, name := range folders {
		var r *LessonRecord
		if o, ok := om[name]; ok {
			c := *o
			r = &c
		} else {
			r = &LessonRecord{Status: "queued"}
		}
		r.Index = i
		r.Folder = name
		if r.Status == "" {
			r.Status = "queued"
		}
		if r.Status == "done" {
			out := r.Output
			if out == "" {
				out = "lesson.html"
			}
			if !fileExists(filepath.Join(root, name, out)) || !fileExists(filepath.Join(root, name, "lesson_build_report.json")) {
				r.Status = "queued"
				r.Reason = "done_recheck_missing_output_or_report"
			}
		}
		ls = append(ls, r)
	}
	st := &BatchStatus{Root: root, Total: len(ls), Current: old.Current, Lessons: ls}
	if err := saveStatus(root, st); err != nil {
		return nil, err
	}
	return st, nil
}
func fileExists(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }
func nextLesson(st *BatchStatus) *LessonRecord {
	for _, r := range st.Lessons {
		if r.Status == "in_progress" {
			return r
		}
	}
	for _, r := range st.Lessons {
		if r.Status == "queued" {
			return r
		}
	}
	return nil
}
func countStatus(st *BatchStatus) Totals {
	t := Totals{Total: len(st.Lessons)}
	for _, r := range st.Lessons {
		switch r.Status {
		case "done":
			t.Done++
		case "queued":
			t.Queued++
		case "in_progress":
			t.InProgress++
		case "failed":
			t.Failed++
		case "needs_review":
			t.NeedsReview++
		}
	}
	return t
}
func writePipeline(root string, fields map[string]any) error {
	m := map[string]any{}
	b, _ := os.ReadFile(pipelinePath(root))
	_ = json.Unmarshal(b, &m)
	for k, v := range fields {
		m[k] = v
	}
	m["updated_at"] = time.Now().Format(time.RFC3339)
	return atomicWriteJSON(pipelinePath(root), m)
}
func writeSummary(root string, st *BatchStatus) (map[string]any, error) {
	t := countStatus(st)
	status := "incomplete"
	if t.Queued == 0 && t.InProgress == 0 {
		status = "completed"
	}
	m := map[string]any{"status": status, "total": t.Total, "done": t.Done, "failed": t.Failed, "needs_review": t.NeedsReview, "completed_at": time.Now().Format(time.RFC3339)}
	return m, atomicWriteJSON(summaryPath(root), m)
}

// ---------------- Extraction ----------------

var textExt = map[string]bool{".txt": true, ".md": true, ".csv": true, ".json": true, ".py": true, ".js": true, ".ts": true, ".java": true, ".cpp": true, ".c": true, ".h": true, ".css": true, ".xml": true, ".sql": true, ".yaml": true, ".yml": true}
var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true}

func readTextSafe(path string, max int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	if max > 0 && len(s) > max {
		return smartTrim(s, max)
	}
	return s
}
func smartTrim(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 200 {
		return s[:max]
	}
	half := (max - 80) / 2
	return s[:half] + "\n...[содержимое сокращено]...\n" + s[len(s)-half:]
}

func stripHTML(s string) string {
	reScript := regexp.MustCompile(`(?is)<script\b.*?</script>`)
	s = reScript.ReplaceAllString(s, " ")
	reStyle := regexp.MustCompile(`(?is)<style\b.*?</style>`)
	s = reStyle.ReplaceAllString(s, " ")
	reTag := regexp.MustCompile(`(?s)<[^>]+>`)
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func zipXMLText(path string, selectors []string, max int) string {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "[Ошибка чтения архива: " + err.Error() + "]"
	}
	defer zr.Close()
	var out strings.Builder
	for _, f := range zr.File {
		match := false
		for _, p := range selectors {
			ok, _ := filepath.Match(p, f.Name)
			if ok {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		dec := xml.NewDecoder(rc)
		for {
			tok, er := dec.Token()
			if er != nil {
				break
			}
			if ch, ok := tok.(xml.CharData); ok {
				t := strings.TrimSpace(string(ch))
				if t != "" {
					out.WriteString(t)
					out.WriteByte(' ')
				}
			}
		}
		rc.Close()
		out.WriteByte('\n')
		if max > 0 && out.Len() > max {
			break
		}
	}
	return smartTrim(out.String(), max)
}
func extractDocx(path string, max int) string {
	return zipXMLText(path, []string{"word/document.xml", "word/header*.xml", "word/footer*.xml"}, max)
}
func extractPptx(path string, max int) string {
	return zipXMLText(path, []string{"ppt/slides/slide*.xml", "ppt/notesSlides/notesSlide*.xml"}, max)
}
func extractXlsx(path string, max int) string {
	return zipXMLText(path, []string{"xl/sharedStrings.xml", "xl/worksheets/sheet*.xml"}, max)
}

// Best-effort PDF extractor: extracts literal text from raw and FlateDecode streams.
func extractPDF(path string, max int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "[Ошибка чтения PDF: " + err.Error() + "]"
	}
	chunks := [][]byte{b}
	re := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	for _, m := range re.FindAllSubmatch(b, -1) {
		raw := m[1]
		zr, e := zlib.NewReader(bytes.NewReader(raw))
		if e == nil {
			d, _ := io.ReadAll(io.LimitReader(zr, 4<<20))
			zr.Close()
			chunks = append(chunks, d)
		}
	}
	tj := regexp.MustCompile(`\((?:\\.|[^\\)])*\)\s*Tj`)
	ta := regexp.MustCompile(`\[(.*?)\]\s*TJ`)
	lit := regexp.MustCompile(`\((?:\\.|[^\\)])*\)`)
	var out strings.Builder
	decode := func(x string) string {
		x = strings.TrimPrefix(strings.TrimSuffix(x, ")"), "(")
		x = strings.ReplaceAll(x, `\(`, "(")
		x = strings.ReplaceAll(x, `\)`, ")")
		x = strings.ReplaceAll(x, `\n`, "\n")
		x = strings.ReplaceAll(x, `\r`, "\n")
		x = strings.ReplaceAll(x, `\\`, `\`)
		return x
	}
	for _, c := range chunks {
		for _, m := range tj.FindAll(c, -1) {
			s := string(m)
			i := strings.LastIndex(s, ")")
			if i > 0 {
				out.WriteString(decode(s[:i+1]))
				out.WriteByte(' ')
			}
		}
		for _, m := range ta.FindAllSubmatch(c, -1) {
			for _, lm := range lit.FindAll(m[1], -1) {
				out.WriteString(decode(string(lm)))
				out.WriteByte(' ')
			}
		}
		if max > 0 && out.Len() > max {
			break
		}
	}
	s := out.String()
	if strings.TrimSpace(s) == "" {
		return "[PDF обнаружен; встроенное извлечение текста не смогло получить читаемый текст. Модель должна опираться на остальные материалы или пользователь может конвертировать PDF в TXT/DOCX.]"
	}
	return smartTrim(s, max)
}
func extractFile(path string, max int) string {
	ext := strings.ToLower(filepath.Ext(path))
	if textExt[ext] {
		return readTextSafe(path, max)
	}
	switch ext {
	case ".html", ".htm":
		return smartTrim(stripHTML(readTextSafe(path, max*2)), max)
	case ".docx":
		return extractDocx(path, max)
	case ".pptx":
		return extractPptx(path, max)
	case ".xlsx", ".xlsm":
		return extractXlsx(path, max)
	case ".pdf":
		return extractPDF(path, max)
	}
	if imageExt[ext] {
		return "Изображение: " + filepath.Base(path)
	}
	return fmt.Sprintf("Файл %s, тип %s, содержимое автоматически не извлечено.", filepath.Base(path), ext)
}

type Material struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}
type LessonData struct {
	Transcript       string     `json:"transcript"`
	Recording        string     `json:"recording"`
	Materials        []Material `json:"materials"`
	ExistingHTML     string     `json:"existing_html"`
	ExistingHTMLName string     `json:"existing_html_name"`
	Images           []string   `json:"-"`
}

func findExistingHTML(lesson string) []string {
	matches, _ := filepath.Glob(filepath.Join(lesson, "*.html"))
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		if a == nil || b == nil {
			return matches[i] > matches[j]
		}
		return a.ModTime().After(b.ModTime())
	})
	return matches
}
func collectLesson(lesson string, maxTotal int, vision bool, maxImages int) (LessonData, error) {
	d := LessonData{}
	d.Transcript = readTextSafe(filepath.Join(lesson, "Расшифровка.txt"), 0)
	d.Recording = readTextSafe(filepath.Join(lesson, "Запись.txt"), 20000)
	fd := filepath.Join(lesson, "files")
	count := 0
	if fi, err := os.Stat(fd); err == nil && fi.IsDir() {
		filepath.WalkDir(fd, func(p string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			count++
			if count > 80 {
				return filepath.SkipAll
			}
			rel, _ := filepath.Rel(lesson, p)
			ext := strings.ToLower(filepath.Ext(p))
			if imageExt[ext] && vision && len(d.Images) < maxImages {
				d.Images = append(d.Images, p)
			}
			d.Materials = append(d.Materials, Material{Name: rel, Content: extractFile(p, 50000)})
			return nil
		})
	}
	hs := findExistingHTML(lesson)
	if len(hs) > 0 {
		d.ExistingHTMLName = filepath.Base(hs[0])
		d.ExistingHTML = readTextSafe(hs[0], 120000)
	}
	b, _ := json.Marshal(struct {
		Transcript       string     `json:"transcript"`
		Recording        string     `json:"recording"`
		Materials        []Material `json:"materials"`
		ExistingHTML     string     `json:"existing_html"`
		ExistingHTMLName string     `json:"existing_html_name"`
	}{d.Transcript, d.Recording, d.Materials, d.ExistingHTML, d.ExistingHTMLName})
	if len(b) > maxTotal {
		over := len(b) - len(d.Transcript)
		budget := maxTotal - over
		if budget < 20000 {
			budget = 20000
		}
		d.Transcript = smartTrim(d.Transcript, budget)
	}
	return d, nil
}

// ---------------- HTML checks ----------------

type CheckPattern struct {
	Re  *regexp.Regexp
	Msg string
}

var forbidden = []CheckPattern{
	{regexp.MustCompile(`(?is)<\s*script\b`), "Запрещён <script>"}, {regexp.MustCompile(`(?is)<\s*style\b`), "Запрещён <style>"}, {regexp.MustCompile(`(?is)<\s*(?:html|head|body)\b`), "HTML должен быть фрагментом без html/head/body"}, {regexp.MustCompile(`(?is)<\s*(?:object|embed|applet)\b`), "Запрещён object/embed/applet"}, {regexp.MustCompile(`(?i)\bjavascript\s*:`), "Запрещён javascript:"}, {regexp.MustCompile(`(?i)\bsrcdoc\s*=`), "Запрещён srcdoc"}, {regexp.MustCompile(`(?i)data\s*:\s*text/html`), "Запрещён data:text/html"}, {regexp.MustCompile(`(?i)\bon[a-zA-Z]+\s*=`), "Запрещены обработчики on..."}, {regexp.MustCompile(`(?i)\bwidth\s*:\s*100vw`), "Не использовать width:100vw"}, {regexp.MustCompile(`(?i)file\s*:///`), "Запрещены file:/// URL"}, {regexp.MustCompile(`(?i)(?:[A-Za-z]:\\|[A-Za-z]:/)[^\"'<>\s]+`), "В HTML обнаружен локальный Windows-путь"}, {regexp.MustCompile(`(?i)(?:src|href)\s*=\s*[\"']\.\/files\/`), "Запрещены ./files ссылки"},
}
var pairTags = []string{"div", "table", "thead", "tbody", "tr", "td", "th", "details", "summary", "pre", "code", "svg"}

func checkHTML(h, recording string) []string {
	issues := []string{}
	if strings.TrimSpace(h) == "" {
		return []string{"HTML пустой"}
	}
	if len(h) < 1500 {
		issues = append(issues, "HTML подозрительно короткий для полноценного урока")
	}
	for _, p := range forbidden {
		if p.Re.MatchString(h) {
			issues = append(issues, p.Msg)
		}
	}
	for _, tag := range pairTags {
		op := len(regexp.MustCompile(`(?i)<\s*`+tag+`\b`).FindAllStringIndex(h, -1))
		cl := len(regexp.MustCompile(`(?i)<\s*/\s*`+tag+`\s*>`).FindAllStringIndex(h, -1))
		if op != cl {
			issues = append(issues, fmt.Sprintf("Несбалансированный тег <%s>: открыто %d, закрыто %d", tag, op, cl))
		}
	}
	lh := strings.ToLower(h)
	if strings.Contains(lh, "<iframe") {
		if strings.TrimSpace(recording) == "" {
			issues = append(issues, "Есть iframe, но Запись.txt пуст")
		}
		if regexp.MustCompile(`(?is)<iframe[^>]*(?:on[a-z]+|srcdoc)\s*=`).MatchString(h) {
			issues = append(issues, "iframe содержит потенциально опасный атрибут")
		}
	}
	if strings.Contains(lh, "<svg") && regexp.MustCompile(`(?is)<\s*(?:animate|animateTransform|foreignObject)\b`).MatchString(h) {
		issues = append(issues, "SVG содержит потенциально проблемную активную конструкцию")
	}
	root := regexp.MustCompile(`(?is)<\s*div\b[^>]*style\s*=\s*[\"']([^\"']+)`).FindStringSubmatch(h)
	if len(root) > 1 {
		st := strings.ReplaceAll(strings.ToLower(root[1]), " ", "")
		for _, e := range []string{"width:100%", "max-width:100%", "box-sizing:border-box", "min-width:0"} {
			if !strings.Contains(st, e) {
				issues = append(issues, "В корневом контейнере не найдено "+e)
			}
		}
	} else {
		issues = append(issues, "Не найден корневой <div style=...>")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, x := range issues {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// ---------------- Prompts ----------------
func baseSystem(skill, runner, extra string) string {
	return `Ты — автономный Moodle Lesson Builder. Работай только над ОДНИМ переданным этапом одного урока.
Ты не управляешь очередью и не решаешь, останавливать ли пакет: это делает внешний Windows-worker.
Не проси пользователя написать «продолжай». Не описывай планы вместо результата текущего этапа.

=== SKILL ===
` + skill + `

=== ДОПОЛНИТЕЛЬНЫЙ СОХРАНЁННЫЙ PROMPT ===
` + runner + `

=== ДОПОЛНИТЕЛЬНЫЕ УКАЗАНИЯ ПОЛЬЗОВАТЕЛЯ ===
` + extra
}
func analyzePrompt(folder string, d LessonData) string {
	clean := map[string]any{"transcript": d.Transcript, "recording": d.Recording, "materials": d.Materials, "existing_html": d.ExistingHTML, "existing_html_name": d.ExistingHTMLName}
	b, _ := json.Marshal(clean)
	return `ЭТАП: ANALYZE / EXTRACT.
Папка урока: ` + folder + `

Изучи материалы и верни ТОЛЬКО валидный JSON для файла lesson_context.json, без markdown fence и без пояснений.
Не пиши HTML урока на этом этапе.

JSON обязан содержать минимум:
subject, class, topic, subtopics, lesson_summary, recording, key_concepts, definitions,
theory_points, formulas, teacher_examples, tasks_from_lesson, typical_errors,
important_facts, visuals_needed, practice_needed, source_material_notes,
must_preserve, must_not_invent, existing_html_notes, search_facts, moodle_constraints.

Сохрани точные числа, даты, имена, формулы, условия задач, реальные URL и важные формулировки.
Не копируй всю расшифровку; создай компактную внешнюю память урока.
Если данных нет — используй пустой список/строку, а не выдумывай.

МАТЕРИАЛЫ:
` + string(b)
}
func planPrompt(c map[string]any) string {
	b, _ := json.Marshal(c)
	return `ЭТАП: PLAN.
По lesson_context создай lesson_plan.json.
Верни ТОЛЬКО валидный JSON без markdown.

Нужны: title, color_theme, sections, svg, tables, graphs, examples, practice, summary.
План должен привести к полноценному, красивому, но Moodle/WAF-safe уроку.
Если нужна «анимация» — планируй статические последовательности кадров/timeline/before-after, без JavaScript.

LESSON_CONTEXT:
` + string(b)
}
func buildPrompt(c, p map[string]any, existing string) string {
	bc, _ := json.Marshal(c)
	bp, _ := json.Marshal(p)
	ex := ""
	if existing != "" {
		ex = "\nСУЩЕСТВУЮЩИЙ HTML ДЛЯ УЛУЧШЕНИЯ:\n" + existing
	}
	return `ЭТАП: BUILD.
Создай итоговый Moodle HTML-фрагмент по lesson_context и lesson_plan.
Верни ТОЛЬКО сам HTML-фрагмент. Никакого markdown fence, комментариев о работе или JSON.

КРИТИЧЕСКИ:
- полноценный содержательный урок, не краткий конспект;
- запись в начале;
- только inline CSS;
- без script/style/javascript:/on.../srcdoc/object/embed;
- статический SVG;
- Python-код разрешён внутри pre/code как текст, HTML-символы в примерах экранировать;
- таблицы адаптивные;
- никаких локальных Z:/ C:/ file:/// ./files ссылок;
- не выдумывать URL;
- сделать теорию, определения, примеры, практику, ошибки, итоги;
- использовать формулы, схемы, SVG и таблицы там, где они действительно помогают.

LESSON_CONTEXT:
` + string(bc) + `\n\nLESSON_PLAN:\n` + string(bp) + ex
}
func qaPrompt(c map[string]any, h string, issues []string) string {
	bc, _ := json.Marshal(c)
	bi, _ := json.Marshal(issues)
	return `ЭТАП: QA.
Проверь HTML как строгий Moodle/WAF-safe урок и на соответствие lesson_context.
Верни ТОЛЬКО JSON:
{"passed":true/false,"issues":["..."],"content_missing":["..."],"waf_risks":["..."]}

Локальный валидатор уже нашёл:
` + string(bi) + `\n\nLESSON_CONTEXT:\n` + string(bc) + `\n\nHTML:\n` + h
}
func fixPrompt(c map[string]any, h string, issues []string) string {
	bc, _ := json.Marshal(c)
	bi, _ := json.Marshal(issues)
	return `ЭТАП: FIX.
Исправь HTML по списку ошибок. Сохрани качественное содержание и дизайн.
Верни ТОЛЬКО полный исправленный HTML-фрагмент без markdown.

ОШИБКИ:
` + string(bi) + `\n\nLESSON_CONTEXT:\n` + string(bc) + `\n\nHTML:\n` + h
}

// ---------------- Unsloth Studio + OpenAI-compatible client ----------------
type APIClient struct {
	settings Settings
	mu       sync.Mutex
	token    string
}

func cleanStudioURL(raw string) string {
	u := strings.TrimSpace(raw)
	u = strings.TrimRight(u, "/")
	for _, suffix := range []string{"/login", "/v1", "/api"} {
		if strings.HasSuffix(strings.ToLower(u), suffix) {
			u = strings.TrimRight(u[:len(u)-len(suffix)], "/")
		}
	}
	return u
}

func (c *APIClient) studioBase() string {
	u := cleanStudioURL(c.settings.StudioURL)
	if u != "" {
		return u
	}
	return cleanStudioURL(c.settings.UnslothBaseURL)
}

func (c *APIClient) apiBase() string {
	u := strings.TrimSpace(c.settings.UnslothBaseURL)
	// Temporary Cloudflare quick-tunnel URLs are deliberately ignored when a LAN Studio URL exists.
	// They rotate/expire and were the source of repeated 401s in v0.4.
	if strings.Contains(strings.ToLower(u), "trycloudflare.com") && strings.TrimSpace(c.settings.StudioURL) != "" {
		u = ""
	}
	if u == "" {
		u = c.studioBase()
	}
	u = strings.TrimRight(u, "/")
	if strings.HasSuffix(strings.ToLower(u), "/login") {
		u = strings.TrimRight(u[:len(u)-len("/login")], "/")
	}
	if !strings.HasSuffix(strings.ToLower(u), "/v1") {
		u += "/v1"
	}
	return u
}

func (c *APIClient) clearToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// loginToken prefers the Studio password/JWT whenever a password is configured.
// A manually saved API key is only a fallback. This prevents an expired API key from
// shadowing a perfectly valid Studio login.
func (c *APIClient) loginToken(forceRefresh bool) (string, error) {
	password := strings.TrimSpace(c.settings.StudioPassword)
	key := strings.TrimSpace(c.settings.UnslothAPIKey)
	if password != "" {
		if !forceRefresh {
			c.mu.Lock()
			if c.token != "" {
				t := c.token
				c.mu.Unlock()
				return t, nil
			}
			c.mu.Unlock()
		}
		base := c.studioBase()
		if base == "" {
			if key != "" {
				return key, nil
			}
			return "", errors.New("не задан Studio URL для входа по паролю")
		}
		username := strings.TrimSpace(c.settings.StudioUsername)
		if username == "" {
			username = "unsloth"
		}
		payload, _ := json.Marshal(map[string]any{"username": username, "password": password})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/api/auth/login", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if resp.StatusCode < 400 {
				var obj map[string]any
				if json.Unmarshal(rb, &obj) == nil {
					if tok, _ := obj["access_token"].(string); strings.TrimSpace(tok) != "" {
						c.mu.Lock()
						c.token = tok
						c.mu.Unlock()
						return tok, nil
					}
				}
			}
			if key == "" {
				return "", fmt.Errorf("Studio login HTTP %d: %s", resp.StatusCode, smartTrim(string(rb), 1200))
			}
		} else if key == "" {
			return "", err
		}
		// Password login failed, but an explicit API key exists: use it as a last resort.
		return key, nil
	}
	if key != "" {
		return key, nil
	}
	return "", nil
}

func (c *APIClient) headers(req *http.Request, forceRefresh bool) error {
	req.Header.Set("Content-Type", "application/json")
	tok, err := c.loginToken(forceRefresh)
	if err != nil {
		return err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return nil
}

func (c *APIClient) request(method, url string, body []byte, timeout time.Duration) (int, []byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, _ := http.NewRequestWithContext(ctx, method, url, rd)
		if err := c.headers(req, attempt > 0); err != nil {
			cancel()
			return 0, nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			return 0, nil, err
		}
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		cancel()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 && strings.TrimSpace(c.settings.StudioPassword) != "" {
			c.clearToken()
			continue
		}
		return resp.StatusCode, rb, nil
	}
	return 0, nil, errors.New("auth retry exhausted")
}

func (c *APIClient) studioHealth() (bool, string) {
	base := c.studioBase()
	if base == "" {
		return false, "Studio URL не задан"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return true, ""
}

func (c *APIClient) models() ([]string, error) {
	status, rb, err := c.request("GET", c.apiBase()+"/models", nil, 12*time.Second)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", status, smartTrim(string(rb), 2000))
	}
	var obj struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rb, &obj); err != nil {
		return nil, err
	}
	out := []string{}
	for _, x := range obj.Data {
		if x.ID != "" {
			out = append(out, x.ID)
		}
	}
	return out, nil
}

func (c *APIClient) resolveModel() (string, error) {
	configured := strings.TrimSpace(c.settings.Model)
	ms, err := c.models()
	if err == nil && len(ms) > 0 {
		if configured != "" {
			for _, m := range ms {
				if m == configured {
					return configured, nil
				}
			}
		}
		// If the hand-entered ID is stale or slightly wrong, prefer the model actually loaded by Studio.
		return ms[0], nil
	}
	if configured != "" {
		return configured, nil
	}
	return "default", nil
}
func imagePart(path string) map[string]any {
	b, _ := os.ReadFile(path)
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mt == "" {
		mt = "image/png"
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(b)}}
}
func (c *APIClient) chat(system, user string, maxTokens int, temp float64, images []string) (string, error) {
	model, err := c.resolveModel()
	if err != nil {
		return "", err
	}
	var content any = user
	if len(images) > 0 {
		parts := []any{map[string]any{"type": "text", "text": user}}
		for _, p := range images {
			parts = append(parts, imagePart(p))
		}
		content = parts
	}
	payload := map[string]any{"model": model, "messages": []any{map[string]any{"role": "system", "content": system}, map[string]any{"role": "user", "content": content}}, "temperature": temp, "max_tokens": maxTokens, "stream": false}
	b, _ := json.Marshal(payload)
	status, rb, err := c.request("POST", c.apiBase()+"/chat/completions", b, time.Duration(c.settings.RequestTimeoutSec)*time.Second)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", status, smartTrim(string(rb), 2000))
	}
	var obj map[string]any
	if err := json.Unmarshal(rb, &obj); err != nil {
		return "", err
	}
	choices, _ := obj["choices"].([]any)
	if len(choices) == 0 {
		return "", errors.New("пустой choices")
	}
	ch, _ := choices[0].(map[string]any)
	msg, _ := ch["message"].(map[string]any)
	ct := msg["content"]
	switch v := ct.(type) {
	case string:
		return v, nil
	case []any:
		var sb strings.Builder
		for _, it := range v {
			if m, ok := it.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		return sb.String(), nil
	default:
		return fmt.Sprint(v), nil
	}
}
func (c *APIClient) ping() (bool, string, []string) {
	if ok, e := c.studioHealth(); !ok {
		return false, "Studio недоступен: " + e, nil
	}
	m, e := c.models()
	if e != nil {
		return false, e.Error(), nil
	}
	return true, "", m
}

// ---------------- Parsing helpers ----------------
func parseJSONObject(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "```json") {
		s = strings.TrimSpace(s[7:])
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimSpace(s[3:])
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) == nil {
		return m, nil
	}
	start := strings.Index(s, "{")
	if start < 0 {
		return nil, errors.New("модель не вернула JSON")
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inStr {
			if esc {
				esc = false
			} else if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inStr = false
			}
			continue
		}
		if ch == '"' {
			inStr = true
		} else if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				if err := json.Unmarshal([]byte(s[start:i+1]), &m); err == nil {
					return m, nil
				}
				break
			}
		}
	}
	return nil, errors.New("не удалось выделить JSON")
}
func cleanHTML(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "```html") {
		s = strings.TrimSpace(s[7:])
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimSpace(s[3:])
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	a := strings.Index(s, "<")
	b := strings.LastIndex(s, ">")
	if a >= 0 && b > a {
		s = s[a : b+1]
	}
	return strings.TrimSpace(s)
}
func chooseOutputPath(lesson string, rec *LessonRecord) string {
	if rec.Output != "" && rec.Status == "in_progress" {
		p := filepath.Join(lesson, rec.Output)
		if fileExists(p) {
			return p
		}
	}
	p := filepath.Join(lesson, "lesson.html")
	if !fileExists(p) {
		return p
	}
	p = filepath.Join(lesson, "lesson_improved.html")
	if !fileExists(p) {
		return p
	}
	for n := 2; ; n++ {
		p = filepath.Join(lesson, fmt.Sprintf("lesson_improved_%d.html", n))
		if !fileExists(p) {
			return p
		}
	}
}

// ---------------- Worker manager ----------------
type Manager struct {
	mu          sync.Mutex
	settings    Settings
	runtime     *RuntimeState
	stop        bool
	pause       bool
	workerAlive bool
	lastLaunch  time.Time
	shutdown    chan struct{}
}

func NewManager() *Manager {
	return &Manager{settings: loadSettings(), runtime: newRuntime(), shutdown: make(chan struct{})}
}
func (m *Manager) getSettings() Settings { m.mu.Lock(); defer m.mu.Unlock(); return m.settings }
func (m *Manager) updateSettings(s Settings) error {
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
	return saveSettings(s)
}
func (m *Manager) checkpoint() error {
	for {
		m.mu.Lock()
		stop := m.stop
		pause := m.pause
		m.mu.Unlock()
		if stop {
			return errors.New("pipeline_stopped")
		}
		if !pause {
			m.runtime.mu.Lock()
			if m.runtime.RunState == "paused" {
				m.runtime.RunState = "running"
			}
			m.runtime.mu.Unlock()
			return nil
		}
		m.runtime.mu.Lock()
		m.runtime.RunState = "paused"
		m.runtime.mu.Unlock()
		time.Sleep(500 * time.Millisecond)
	}
}
func (m *Manager) callLLM(client *APIClient, system, user string, max int, temp float64, images []string, label string) (string, error) {
	s := m.getSettings()
	var last error
	for a := 0; a <= s.MaxAPIRetries; a++ {
		if err := m.checkpoint(); err != nil {
			return "", err
		}
		m.runtime.logf("%s: запрос к Unsloth, попытка %d/%d", label, a+1, s.MaxAPIRetries+1)
		r, e := client.chat(system, user, max, temp, images)
		if e == nil && strings.TrimSpace(r) != "" {
			return r, nil
		}
		if e == nil {
			e = errors.New("пустой ответ модели")
		}
		last = e
		m.runtime.logf("%s: ошибка API — %v", label, e)
		if a < s.MaxAPIRetries {
			time.Sleep(time.Duration(2+a*3) * time.Second)
		}
	}
	return "", last
}
func (m *Manager) setStage(root, lesson, stage, next string) {
	_ = writePipeline(root, map[string]any{"run_status": "running", "can_finish_response": false, "current_lesson": lesson, "stage": stage, "next_action": next})
	m.runtime.progress(stage, lesson)
}
func (m *Manager) mark(st *BatchStatus, rec *LessonRecord, state string) {
	rec.Status = state
	st.Current = rec.Index
	_ = saveStatus(st.Root, st)
	m.runtime.mu.Lock()
	m.runtime.Totals = countStatus(st)
	m.runtime.mu.Unlock()
}
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func listStrings(v any) []string {
	out := []string{}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			s := strings.TrimSpace(asString(x))
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
func (m *Manager) processLesson(st *BatchStatus, rec *LessonRecord, skill, runner string) error {
	s := m.getSettings()
	root := st.Root
	lesson := filepath.Join(root, rec.Folder)
	rec.Attempts++
	rec.StartedAt = time.Now().Format(time.RFC3339)
	m.mark(st, rec, "in_progress")
	client := &APIClient{settings: s}
	system := baseSystem(skill, runner, s.ExtraInstructions)
	m.setStage(root, rec.Folder, "INGEST", "read_current_lesson_files")
	data, err := collectLesson(lesson, s.MaxInputChars, s.VisionEnabled, s.MaxImagesPerLesson)
	if err != nil {
		return err
	}
	if strings.TrimSpace(data.Transcript) == "" {
		return errors.New("не найден или пуст Расшифровка.txt")
	}
	if strings.TrimSpace(data.Recording) == "" {
		m.runtime.logf("%s: Запись.txt отсутствует/пуст — продолжу без выдумывания URL", rec.Folder)
	}
	m.setStage(root, rec.Folder, "EXTRACT", "create_lesson_context_json")
	imgs := []string{}
	if s.VisionEnabled {
		imgs = data.Images
	}
	raw, e := m.callLLM(client, system, analyzePrompt(rec.Folder, data), s.AnalyzeMaxTokens, s.TemperatureAnalyze, imgs, "ANALYZE")
	if e != nil {
		return e
	}
	ctx, e := parseJSONObject(raw)
	if e != nil {
		return e
	}
	if e = atomicWriteJSON(filepath.Join(lesson, "lesson_context.json"), ctx); e != nil {
		return e
	}
	if e = m.checkpoint(); e != nil {
		return e
	}
	m.setStage(root, rec.Folder, "PLAN", "create_lesson_plan_json")
	raw, e = m.callLLM(client, system, planPrompt(ctx), s.PlanMaxTokens, s.TemperaturePlan, nil, "PLAN")
	if e != nil {
		return e
	}
	plan, e := parseJSONObject(raw)
	if e != nil {
		return e
	}
	if e = atomicWriteJSON(filepath.Join(lesson, "lesson_plan.json"), plan); e != nil {
		return e
	}
	if e = m.checkpoint(); e != nil {
		return e
	}
	m.setStage(root, rec.Folder, "BUILD", "generate_moodle_html_from_compact_memory")
	existing := data.ExistingHTML
	if len(existing) > s.MaxExistingHTMLChars {
		existing = existing[:s.MaxExistingHTMLChars] + "\n...[существующий HTML сокращён]..."
	}
	raw, e = m.callLLM(client, system, buildPrompt(ctx, plan, existing), s.BuildMaxTokens, s.TemperatureBuild, nil, "BUILD")
	if e != nil {
		return e
	}
	h := cleanHTML(raw)
	out := chooseOutputPath(lesson, rec)
	tmp := out + ".tmp"
	if e = os.WriteFile(tmp, []byte(h), 0644); e != nil {
		return e
	}
	fi, _ := os.Stat(tmp)
	if fi == nil || fi.Size() == 0 {
		return errors.New("модель создала пустой HTML")
	}
	_ = os.Remove(out)
	if e = os.Rename(tmp, out); e != nil {
		return e
	}
	rec.Output = filepath.Base(out)
	_ = saveStatus(root, st)
	if e = m.checkpoint(); e != nil {
		return e
	}
	m.setStage(root, rec.Folder, "READ_BACK", "verify_saved_html")
	h = readTextSafe(out, 0)
	local := checkHTML(h, data.Recording)
	m.setStage(root, rec.Folder, "QA", "run_local_and_llm_qa")
	raw, e = m.callLLM(client, system, qaPrompt(ctx, h, local), s.QAMaxTokens, s.TemperatureQA, nil, "QA")
	if e != nil {
		return e
	}
	qa, e := parseJSONObject(raw)
	if e != nil {
		return e
	}
	issues := append([]string{}, local...)
	for _, k := range []string{"issues", "content_missing", "waf_risks"} {
		issues = append(issues, listStrings(qa[k])...)
	}
	issues = dedupe(issues)
	passed, _ := qa["passed"].(bool)
	passed = passed && len(issues) == 0
	for fr := 1; (!passed || len(issues) > 0) && fr <= s.MaxQAFixes; fr++ {
		if e = m.checkpoint(); e != nil {
			return e
		}
		m.setStage(root, rec.Folder, fmt.Sprintf("FIX_%d", fr), "fix_qa_issues_and_recheck")
		raw, e = m.callLLM(client, system, fixPrompt(ctx, h, issues), s.FixMaxTokens, s.TemperatureFix, nil, fmt.Sprintf("FIX %d", fr))
		if e != nil {
			return e
		}
		h = cleanHTML(raw)
		if e = os.WriteFile(tmp, []byte(h), 0644); e != nil {
			return e
		}
		_ = os.Remove(out)
		if e = os.Rename(tmp, out); e != nil {
			return e
		}
		h = readTextSafe(out, 0)
		local = checkHTML(h, data.Recording)
		raw, e = m.callLLM(client, system, qaPrompt(ctx, h, local), s.QAMaxTokens, s.TemperatureQA, nil, fmt.Sprintf("QA %d", fr+1))
		if e != nil {
			return e
		}
		qa, e = parseJSONObject(raw)
		if e != nil {
			return e
		}
		issues = append([]string{}, local...)
		for _, k := range []string{"issues", "content_missing", "waf_risks"} {
			issues = append(issues, listStrings(qa[k])...)
		}
		issues = dedupe(issues)
		passed, _ = qa["passed"].(bool)
		passed = passed && len(issues) == 0
	}
	m.setStage(root, rec.Folder, "COMMIT", "save_report_and_mark_done")
	report := map[string]any{"status": map[bool]string{true: "done", false: "needs_review"}[passed], "subject": ctx["subject"], "class": ctx["class"], "topic": ctx["topic"], "output": rec.Output, "checks_passed": passed, "qa_issues": issues, "color_theme": plan["color_theme"], "completed_at": time.Now().Format(time.RFC3339)}
	_ = atomicWriteJSON(filepath.Join(lesson, "lesson_build_report.json"), report)
	rec.CompletedAt = time.Now().Format(time.RFC3339)
	if passed {
		rec.LastError = ""
		m.mark(st, rec, "done")
		m.runtime.logf("[%d/%d] %s — DONE — %s", rec.Index+1, st.Total, rec.Folder, rec.Output)
	} else {
		rec.ReviewReason = strings.Join(issues, "; ")
		m.mark(st, rec, "needs_review")
		m.runtime.logf("[%d/%d] %s — NEEDS_REVIEW", rec.Index+1, st.Total, rec.Folder)
	}
	m.setStage(root, rec.Folder, "RESET", "select_next_lesson")
	return nil
}
func dedupe(a []string) []string {
	seen := map[string]bool{}
	o := []string{}
	for _, x := range a {
		x = strings.TrimSpace(x)
		if x != "" && !seen[x] {
			seen[x] = true
			o = append(o, x)
		}
	}
	return o
}
func (m *Manager) runWorker() {
	m.mu.Lock()
	if m.workerAlive {
		m.mu.Unlock()
		return
	}
	m.workerAlive = true
	m.stop = false
	m.mu.Unlock()
	m.runtime.mu.Lock()
	m.runtime.WorkerAlive = true
	m.runtime.RunState = "running"
	m.runtime.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.workerAlive = false
		m.mu.Unlock()
		m.runtime.mu.Lock()
		m.runtime.WorkerAlive = false
		m.runtime.mu.Unlock()
	}()
	s := m.getSettings()
	root := s.RootPath
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		m.runtime.logf("Worker: ROOT_PATH недоступен: %s", root)
		m.runtime.mu.Lock()
		m.runtime.RunState = "error"
		m.runtime.mu.Unlock()
		return
	}
	skill, err := readEmbeddedOrFile(s.SkillPath, "resources/moodle-lesson-builder.md")
	if err != nil {
		m.runtime.logf("Skill error: %v", err)
		return
	}
	runner, err := readEmbeddedOrFile(s.PromptPath, "resources/moodle-batch-run-prompt.md")
	if err != nil {
		m.runtime.logf("Prompt error: %v", err)
		return
	}
	st, err := reconcileStatus(root)
	if err != nil {
		m.runtime.logf("Status error: %v", err)
		return
	}
	m.runtime.mu.Lock()
	m.runtime.Totals = countStatus(st)
	m.runtime.mu.Unlock()
	m.runtime.logf("Найдено уроков: %d", st.Total)
	for {
		if err = m.checkpoint(); err != nil {
			m.runtime.logf("Worker остановлен пользователем")
			m.runtime.mu.Lock()
			m.runtime.RunState = "idle"
			m.runtime.mu.Unlock()
			return
		}
		st, err = reconcileStatus(root)
		if err != nil {
			m.runtime.logf("Reconcile error: %v", err)
			return
		}
		rec := nextLesson(st)
		if rec == nil {
			break
		}
		err = m.processLesson(st, rec, skill, runner)
		if err != nil {
			if err.Error() == "pipeline_stopped" {
				return
			}
			client := &APIClient{settings: s}
			ok, _, _ := client.ping()
			rec.LastError = err.Error()
			_ = saveStatus(root, st)
			if !ok {
				m.runtime.logf("%s: API offline — оставляю in_progress для watchdog", rec.Folder)
				m.runtime.mu.Lock()
				m.runtime.RunState = "error"
				m.runtime.APIError = err.Error()
				m.runtime.mu.Unlock()
				return
			}
			if rec.Attempts >= 2 {
				rec.ErrorType = "llm_api_or_generation_error"
				rec.Error = err.Error()
				m.mark(st, rec, "needs_review")
				continue
			}
			rec.ErrorType = "processing_error"
			rec.Error = err.Error()
			m.mark(st, rec, "needs_review")
		}
	}
	st, _ = reconcileStatus(root)
	sum, _ := writeSummary(root, st)
	_ = writePipeline(root, map[string]any{"run_status": "completed", "can_finish_response": true, "current_lesson": "", "stage": "DONE", "next_action": "none", "summary": sum})
	m.runtime.mu.Lock()
	m.runtime.RunState = "completed"
	m.runtime.CurrentStage = "DONE"
	m.runtime.mu.Unlock()
	m.runtime.logf("Пакет завершён: %v", sum)
}
func (m *Manager) startWorker() (bool, string) {
	m.mu.Lock()
	alive := m.workerAlive
	m.mu.Unlock()
	if alive {
		return false, "Worker уже работает"
	}
	s := m.getSettings()
	if strings.TrimSpace(s.RootPath) == "" {
		return false, "Сначала выберите папку уроков"
	}
	go m.runWorker()
	return true, "Worker запущен"
}
func (m *Manager) pauseWorker() {
	m.mu.Lock()
	m.pause = true
	m.mu.Unlock()
	m.runtime.logf("Пауза")
}
func (m *Manager) resumeWorker() (bool, string) {
	m.mu.Lock()
	m.pause = false
	alive := m.workerAlive
	m.mu.Unlock()
	if !alive {
		return m.startWorker()
	}
	m.runtime.logf("Продолжение")
	return true, "Продолжено"
}
func (m *Manager) stopWorker() {
	m.mu.Lock()
	m.stop = true
	m.pause = false
	m.mu.Unlock()
	m.runtime.logf("Запрошен стоп")
}
func (m *Manager) apiStatus() (bool, string, []string) {
	s := m.getSettings()
	c := &APIClient{settings: s}
	ok, e, mods := c.ping()
	m.runtime.mu.Lock()
	m.runtime.APIOnline = ok
	m.runtime.APIError = e
	if ok && len(mods) > 0 {
		if s.Model != "" {
			m.runtime.ModelResolved = s.Model
		} else {
			m.runtime.ModelResolved = mods[0]
		}
	}
	m.runtime.mu.Unlock()
	return ok, e, mods
}
func (m *Manager) launchUnslothIfNeeded() bool {
	s := m.getSettings()
	cmd := strings.TrimSpace(s.UnslothStartCommand)
	if cmd == "" {
		return false
	}
	m.mu.Lock()
	if time.Since(m.lastLaunch) < time.Duration(s.LaunchCooldownSec)*time.Second {
		m.mu.Unlock()
		return false
	}
	m.lastLaunch = time.Now()
	m.mu.Unlock()
	c := exec.Command("cmd.exe", "/C", cmd)
	if s.LaunchHidden {
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	if err := c.Start(); err != nil {
		m.runtime.logf("Не удалось запустить Unsloth: %v", err)
		return false
	}
	m.runtime.mu.Lock()
	m.runtime.UnslothProcessPID = c.Process.Pid
	m.runtime.mu.Unlock()
	m.runtime.logf("Команда запуска Unsloth выполнена, PID %d", c.Process.Pid)
	return true
}
func (m *Manager) hasPending() bool {
	s := m.getSettings()
	if s.RootPath == "" {
		return false
	}
	st, err := reconcileStatus(s.RootPath)
	if err != nil {
		return false
	}
	return nextLesson(st) != nil
}
func (m *Manager) startWatchdog() {
	go func() {
		for {
			select {
			case <-m.shutdown:
				return
			default:
			}
			ok, err, _ := m.apiStatus()
			if !ok {
				m.runtime.logf("Watchdog: Unsloth offline — %s", err)
				if m.hasPending() {
					m.launchUnslothIfNeeded()
				}
			} else {
				m.mu.Lock()
				alive := m.workerAlive
				m.mu.Unlock()
				s := m.getSettings()
				if s.AutoResume && !alive && m.hasPending() {
					m.runtime.logf("Watchdog: API online, возобновляю worker")
					m.startWorker()
				}
			}
			iv := m.getSettings().WatchdogIntervalSec
			if iv < 5 {
				iv = 5
			}
			select {
			case <-m.shutdown:
				return
			case <-time.After(time.Duration(iv) * time.Second):
			}
		}
	}()
}
func (m *Manager) shutdownAll() {
	select {
	case <-m.shutdown:
	default:
		close(m.shutdown)
	}
	m.stopWorker()
}

// ---------------- System stats ----------------
type MEMORYSTATUSEX struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")

func memoryStats() (float64, float64) {
	var m MEMORYSTATUSEX
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0
	}
	used := m.TotalPhys - m.AvailPhys
	return float64(used) / (1 << 30), float64(m.TotalPhys) / (1 << 30)
}
func gpuStats() map[string]any {
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu", "--format=csv,noheader,nounits")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	b, err := cmd.Output()
	if err != nil {
		return map[string]any{}
	}
	line := strings.Split(strings.TrimSpace(string(b)), "\n")[0]
	p := strings.Split(line, ",")
	if len(p) < 5 {
		return map[string]any{}
	}
	for i := range p {
		p[i] = strings.TrimSpace(p[i])
	}
	iv := func(s string) int { n, _ := strconv.Atoi(strings.Split(s, ".")[0]); return n }
	return map[string]any{"name": p[0], "util": iv(p[1]), "memory_used": iv(p[2]), "memory_total": iv(p[3]), "temp": iv(p[4])}
}
func systemStats() map[string]any {
	u, t := memoryStats()
	return map[string]any{"cpu": 0, "ram_used_gb": fmt.Sprintf("%.1f", u), "ram_total_gb": fmt.Sprintf("%.1f", t), "gpu": gpuStats()}
}

// ---------------- Web UI ----------------
func getWebUI() string { b, _ := embeddedFS.ReadFile("resources/ui.html"); return string(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v)
}
func browseDialog(kind string) string {
	var ps string
	if kind == "root" {
		ps = `Add-Type -AssemblyName System.Windows.Forms; $d=New-Object System.Windows.Forms.FolderBrowserDialog; if($d.ShowDialog() -eq 'OK'){[Console]::OutputEncoding=[Text.Encoding]::UTF8; Write-Output $d.SelectedPath}`
	} else {
		ps = `Add-Type -AssemblyName System.Windows.Forms; $d=New-Object System.Windows.Forms.OpenFileDialog; $d.Filter='Markdown/Text|*.md;*.txt|All files|*.*'; if($d.ShowDialog() -eq 'OK'){[Console]::OutputEncoding=[Text.Encoding]::UTF8; Write-Output $d.FileName}`
	}
	c := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", ps)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	b, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func openBrowser() {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", fmt.Sprintf("http://%s:%d", Host, Port)).Start()
}
func (m *Manager) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, getWebUI())
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		s := m.getSettings()
		safe := s
		if safe.UnslothAPIKey != "" {
			safe.UnslothAPIKey = "***"
		}
		if safe.StudioPassword != "" {
			safe.StudioPassword = "***"
		}
		lessons := []*LessonRecord{}
		if s.RootPath != "" {
			if st, err := reconcileStatus(s.RootPath); err == nil {
				lessons = st.Lessons
			}
		}
		writeJSON(w, map[string]any{"runtime": m.runtime.snapshot(), "settings": safe, "lessons": lessons, "system": systemStats()})
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Data Settings `json:"data"`
		}
		if readJSON(r, &p) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		old := m.getSettings()
		if p.Data.UnslothAPIKey == "***" {
			p.Data.UnslothAPIKey = old.UnslothAPIKey
		}
		if p.Data.StudioPassword == "***" {
			p.Data.StudioPassword = old.StudioPassword
		}
		fillDefaults(&p.Data)
		err := m.updateSettings(p.Data)
		writeJSON(w, map[string]any{"ok": err == nil})
	})
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Kind string `json:"kind"`
		}
		_ = readJSON(r, &p)
		writeJSON(w, map[string]any{"path": browseDialog(p.Kind)})
	})
	mux.HandleFunc("/api/repair-auth", func(w http.ResponseWriter, r *http.Request) {
		s := m.getSettings()
		// Force the stable LAN Studio path and password/JWT flow.
		s.UnslothBaseURL = ""
		s.UnslothAPIKey = ""
		s.Model = ""
		if strings.TrimSpace(s.StudioURL) == "" {
			s.StudioURL = "http://192.168.1.12:8888"
		}
		if strings.TrimSpace(s.StudioUsername) == "" {
			s.StudioUsername = "unsloth"
		}
		_ = m.updateSettings(s)
		ok, e, mods := m.apiStatus()
		changed := 0
		if ok {
			st, err := reconcileStatus(s.RootPath)
			if err == nil {
				for _, rec := range st.Lessons {
					msg := strings.ToLower(rec.LastError + " " + rec.Error + " " + rec.ReviewReason)
					if rec.Status == "needs_review" && (strings.Contains(msg, "401") || strings.Contains(msg, "auth") || strings.Contains(msg, "api key") || strings.Contains(msg, "api offline")) {
						rec.Status = "queued"
						rec.Reason = "auto_requeue_after_auth_repair"
						rec.Error = ""
						rec.LastError = ""
						rec.ErrorType = ""
						changed++
					}
				}
				_ = saveStatus(s.RootPath, st)
			}
		}
		writeJSON(w, map[string]any{"ok": ok, "error": e, "models": mods, "requeued": changed})
	})
	mux.HandleFunc("/api/test-api", func(w http.ResponseWriter, r *http.Request) {
		ok, e, mods := m.apiStatus()
		writeJSON(w, map[string]any{"ok": ok, "error": e, "models": mods})
	})
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		ok, msg := m.startWorker()
		writeJSON(w, map[string]any{"ok": ok, "message": msg})
	})
	mux.HandleFunc("/api/pause", func(w http.ResponseWriter, r *http.Request) {
		m.pauseWorker()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/resume", func(w http.ResponseWriter, r *http.Request) {
		ok, msg := m.resumeWorker()
		writeJSON(w, map[string]any{"ok": ok, "message": msg})
	})
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) { m.stopWorker(); writeJSON(w, map[string]any{"ok": true}) })
	mux.HandleFunc("/api/launch-unsloth", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": m.launchUnslothIfNeeded()})
	})
	mux.HandleFunc("/api/reconcile", func(w http.ResponseWriter, r *http.Request) {
		s := m.getSettings()
		st, e := reconcileStatus(s.RootPath)
		writeJSON(w, map[string]any{"ok": e == nil, "status": st, "message": errString(e)})
	})
	mux.HandleFunc("/api/requeue", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Statuses []string `json:"statuses"`
		}
		_ = readJSON(r, &p)
		s := m.getSettings()
		st, e := reconcileStatus(s.RootPath)
		if e != nil {
			writeJSON(w, map[string]any{"ok": false, "message": e.Error()})
			return
		}
		set := map[string]bool{}
		for _, x := range p.Statuses {
			set[x] = true
		}
		n := 0
		for _, rec := range st.Lessons {
			if set[rec.Status] {
				rec.Status = "queued"
				rec.Reason = "manual_requeue"
				n++
			}
		}
		_ = saveStatus(s.RootPath, st)
		writeJSON(w, map[string]any{"ok": true, "changed": n})
	})
	mux.HandleFunc("/api/open-root", func(w http.ResponseWriter, r *http.Request) {
		s := m.getSettings()
		if !fileOrDirExists(s.RootPath) {
			writeJSON(w, map[string]any{"ok": false})
			return
		}
		_ = exec.Command("explorer.exe", s.RootPath).Start()
		writeJSON(w, map[string]any{"ok": true})
	})
	return mux
}
func fillDefaults(s *Settings) {
	d := defaultSettings()
	if strings.TrimSpace(s.StudioURL) == "" {
		s.StudioURL = d.StudioURL
	}
	if strings.TrimSpace(s.StudioUsername) == "" {
		s.StudioUsername = d.StudioUsername
	}
	if s.WatchdogIntervalSec == 0 {
		s.WatchdogIntervalSec = d.WatchdogIntervalSec
	}
	if s.LaunchCooldownSec == 0 {
		s.LaunchCooldownSec = d.LaunchCooldownSec
	}
	if s.RequestTimeoutSec == 0 {
		s.RequestTimeoutSec = d.RequestTimeoutSec
	}
	if s.MaxAPIRetries == 0 {
		s.MaxAPIRetries = d.MaxAPIRetries
	}
	if s.MaxQAFixes == 0 {
		s.MaxQAFixes = d.MaxQAFixes
	}
	if s.MaxInputChars == 0 {
		s.MaxInputChars = d.MaxInputChars
	}
	if s.MaxExistingHTMLChars == 0 {
		s.MaxExistingHTMLChars = d.MaxExistingHTMLChars
	}
	if s.AnalyzeMaxTokens == 0 {
		s.AnalyzeMaxTokens = d.AnalyzeMaxTokens
	}
	if s.PlanMaxTokens == 0 {
		s.PlanMaxTokens = d.PlanMaxTokens
	}
	if s.BuildMaxTokens == 0 {
		s.BuildMaxTokens = d.BuildMaxTokens
	}
	if s.QAMaxTokens == 0 {
		s.QAMaxTokens = d.QAMaxTokens
	}
	if s.FixMaxTokens == 0 {
		s.FixMaxTokens = d.FixMaxTokens
	}
	if s.MaxImagesPerLesson == 0 {
		s.MaxImagesPerLesson = d.MaxImagesPerLesson
	}
	if s.TemperatureAnalyze == 0 {
		s.TemperatureAnalyze = d.TemperatureAnalyze
	}
	if s.TemperaturePlan == 0 {
		s.TemperaturePlan = d.TemperaturePlan
	}
	if s.TemperatureBuild == 0 {
		s.TemperatureBuild = d.TemperatureBuild
	}
	if s.TemperatureQA == 0 {
		s.TemperatureQA = d.TemperatureQA
	}
	if s.TemperatureFix == 0 {
		s.TemperatureFix = d.TemperatureFix
	}
}
func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func fileOrDirExists(p string) bool { _, e := os.Stat(p); return e == nil }

// ---------------- Windows tray ----------------
const (
	WM_DESTROY       = 0x0002
	WM_COMMAND       = 0x0111
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_APP           = 0x8000
	NIM_ADD          = 0x00000000
	NIM_DELETE       = 0x00000002
	NIF_MESSAGE      = 0x00000001
	NIF_ICON         = 0x00000002
	NIF_TIP          = 0x00000004
	MF_STRING        = 0x00000000
	MF_SEPARATOR     = 0x00000800
	TPM_RETURNCMD    = 0x0100
	TPM_RIGHTBUTTON  = 0x0002
	IDI_APPLICATION  = 32512
	IMAGE_ICON       = 1
	LR_LOADFROMFILE  = 0x0010
	LR_DEFAULTSIZE   = 0x0040
	SW_HIDE          = 0
)
const trayMsg = WM_APP + 1
const (
	idOpen  = 1001
	idStart = 1002
	idPause = 1003
	idStop  = 1004
	idExit  = 1005
)

type POINT struct{ X, Y int32 }
type MSG struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             POINT
	LPrivate       uint32
}
type WNDCLASSEXW struct {
	CbSize                                   uint32
	Style                                    uint32
	LpfnWndProc                              uintptr
	CbClsExtra, CbWndExtra                   int32
	HInstance, HIcon, HCursor, HbrBackground uintptr
	LpszMenuName, LpszClassName              *uint16
	HIconSm                                  uintptr
}
type NOTIFYICONDATAW struct {
	CbSize               uint32
	HWnd                 uintptr
	UID                  uint32
	UFlags               uint32
	UCallbackMessage     uint32
	HIcon                uintptr
	SzTip                [128]uint16
	DwState, DwStateMask uint32
	SzInfo               [256]uint16
	UTimeoutOrVersion    uint32
	SzInfoTitle          [64]uint16
	DwInfoFlags          uint32
	GuidItem             [16]byte
	HBalloonIcon         uintptr
}

var user32 = syscall.NewLazyDLL("user32.dll")
var shell32 = syscall.NewLazyDLL("shell32.dll")
var procRegisterClassEx = user32.NewProc("RegisterClassExW")
var procCreateWindowEx = user32.NewProc("CreateWindowExW")
var procDefWindowProc = user32.NewProc("DefWindowProcW")
var procGetMessage = user32.NewProc("GetMessageW")
var procTranslateMessage = user32.NewProc("TranslateMessage")
var procDispatchMessage = user32.NewProc("DispatchMessageW")
var procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
var procAppendMenu = user32.NewProc("AppendMenuW")
var procTrackPopupMenu = user32.NewProc("TrackPopupMenu")
var procDestroyMenu = user32.NewProc("DestroyMenu")
var procGetCursorPos = user32.NewProc("GetCursorPos")
var procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
var procPostQuitMessage = user32.NewProc("PostQuitMessage")
var procLoadIcon = user32.NewProc("LoadIconW")
var procLoadImage = user32.NewProc("LoadImageW")
var procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
var procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
var globalManager *Manager
var trayHWND uintptr

func wstr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func fillUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if len(u) >= len(dst) {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
}
func trayWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case trayMsg:
		switch uint32(lparam) {
		case WM_LBUTTONDBLCLK:
			openBrowser()
		case WM_RBUTTONUP:
			showTrayMenu(hwnd)
		}
		return 0
	case WM_COMMAND:
		return 0
	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}
func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	procAppendMenu.Call(menu, MF_STRING, idOpen, uintptr(unsafe.Pointer(wstr("Открыть MoodleWorker"))))
	procAppendMenu.Call(menu, MF_SEPARATOR, 0, 0)
	procAppendMenu.Call(menu, MF_STRING, idStart, uintptr(unsafe.Pointer(wstr("Старт / Продолжить"))))
	procAppendMenu.Call(menu, MF_STRING, idPause, uintptr(unsafe.Pointer(wstr("Пауза"))))
	procAppendMenu.Call(menu, MF_STRING, idStop, uintptr(unsafe.Pointer(wstr("Стоп"))))
	procAppendMenu.Call(menu, MF_SEPARATOR, 0, 0)
	procAppendMenu.Call(menu, MF_STRING, idExit, uintptr(unsafe.Pointer(wstr("Выход"))))
	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, TPM_RETURNCMD|TPM_RIGHTBUTTON, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	procDestroyMenu.Call(menu)
	switch cmd {
	case idOpen:
		openBrowser()
	case idStart:
		globalManager.resumeWorker()
	case idPause:
		globalManager.pauseWorker()
	case idStop:
		globalManager.stopWorker()
	case idExit:
		globalManager.shutdownAll()
		removeTrayIcon()
		procPostQuitMessage.Call(0)
	}
}
func addTrayIcon(hwnd uintptr) {
	ico := uintptr(0)
	if b, err := embeddedFS.ReadFile("resources/moodleworker.ico"); err == nil {
		p := filepath.Join(appDataDir(), "moodleworker.ico")
		_ = os.WriteFile(p, b, 0644)
		if ip, err := syscall.UTF16PtrFromString(p); err == nil {
			ico, _, _ = procLoadImage.Call(0, uintptr(unsafe.Pointer(ip)), IMAGE_ICON, 0, 0, LR_LOADFROMFILE|LR_DEFAULTSIZE)
		}
	}
	if ico == 0 {
		ico, _, _ = procLoadIcon.Call(0, IDI_APPLICATION)
	}
	var n NOTIFYICONDATAW
	n.CbSize = uint32(unsafe.Sizeof(n))
	n.HWnd = hwnd
	n.UID = 1
	n.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	n.UCallbackMessage = trayMsg
	n.HIcon = ico
	fillUTF16(n.SzTip[:], "MoodleWorker")
	procShellNotifyIcon.Call(NIM_ADD, uintptr(unsafe.Pointer(&n)))
}
func removeTrayIcon() {
	if trayHWND == 0 {
		return
	}
	var n NOTIFYICONDATAW
	n.CbSize = uint32(unsafe.Sizeof(n))
	n.HWnd = trayHWND
	n.UID = 1
	procShellNotifyIcon.Call(NIM_DELETE, uintptr(unsafe.Pointer(&n)))
}
func runTray(m *Manager) {
	globalManager = m
	inst, _, _ := procGetModuleHandle.Call(0)
	cls := wstr("MoodleWorkerTrayClass")
	cb := syscall.NewCallback(trayWndProc)
	wc := WNDCLASSEXW{CbSize: uint32(unsafe.Sizeof(WNDCLASSEXW{})), LpfnWndProc: cb, HInstance: inst, LpszClassName: cls}
	procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(wstr("MoodleWorker"))), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	trayHWND = hwnd
	addTrayIcon(hwnd)
	var msg MSG
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	removeTrayIcon()
}

// ---------------- Main ----------------
func main() {
	log.SetOutput(io.Discard)
	m := NewManager()
	globalManager = m
	m.startWatchdog()
	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", Host, Port), Handler: m.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.runtime.logf("WebUI error: %v", err)
		}
	}()
	time.Sleep(900 * time.Millisecond)
	if m.getSettings().AutoOpenBrowser {
		openBrowser()
	}
	runTray(m)
}
