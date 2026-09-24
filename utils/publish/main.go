package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/gocolly/colly/v2"

	"github.com/BurntSushi/toml"

	as "github.com/asticode/go-astisub"
)

var (
	issue, _ = strconv.Atoi(os.Args[1])
	issueStr = fmt.Sprintf("%d", issue)

	hugoFile         = "../../../radio-t_site/hugo/content/posts/podcast-" + issueStr + ".md"
	descFile         = "../../data/" + issueStr + "/" + issueStr + "_desc.json"
	topicsSearchFile = "../../data/" + issueStr + "/tmp/meili_topics.json"

	chatFileURL = "https://chat.radio-t.com/logs/radio-t-" + issueStr + ".html"
	//	chatSrcFile = "../../data/" + issueStr + "/radio-t-" + issueStr + ".html"
	chatJsonFile   = "../../data/" + issueStr + "/" + issueStr + "_chat.json"
	chatSearchFile = "../../data/" + issueStr + "/tmp/meili_chat.json"

	// ccSrcFile — рабочий SSA выпуска (черновик после диаризации/присвоения голосов).
	// ccCleanSrcFile — SSA после волонтёрской выверки (генерируется внешним
	// radio-t_playchat_clean). При наличии он приоритетнее — см. ccSourceFile.
	ccSrcFile      = "../../data/" + issueStr + "/tmp/06_manual.ssa"
	ccCleanSrcFile = "../../data/" + issueStr + "/tmp/07_clean.ssa"
	ccSsaFile      = "../../data/" + issueStr + "/" + issueStr + "_cc.ssa"
	// jsonFile = "../../data/" + issueStr + "/src/rt_podcast" + issueStr + ".json"
	ccJsonFile   = "../../data/" + issueStr + "/" + issueStr + "_cc.json"
	ccSearchFile = "../../data/" + issueStr + "/tmp/meili_cc.json"

	listFile = "../../data/list.json"

	// Общий .env проекта (корень репозитория); путь — относительно CWD utils/publish
	envFile = "../../.env"

	timezone  = "Europe/Moscow"
	issueDate string
	hostIds   = []string{"umputun", "bobuk", "grayodesa", "alek_sys"}
	hostNames = []string{"Ksenia"}
	botIds    = []string{"radiot_superbot"}
	botNames  = []string{}

	// Флаги CLI: forceChat — пересоздать N_chat.json, даже если он уже существует
	// (id реплик при этом сохраняются). Задаётся как --force-chat или -fc.
	forceChat bool

	// forceDesc — пересоздать N_desc.json из Hugo, даже если он уже существует.
	// Задаётся как --force-desc или -fd.
	forceDesc bool

	// chatStepExecuted — true, если шаг чата реально выполнялся в этом запуске
	// (пересобраны N_chat.json и tmp/meili_chat.json). Определяет, нужно ли
	// трогать индекс chat_msgs в Meilisearch (см. updateSearchData).
	chatStepExecuted bool

	// descStepExecuted — true, если описание реально (пере)собиралось из Hugo или
	// правились нулевые id тем. Определяет, нужно ли трогать индекс topics в Meilisearch.
	descStepExecuted bool

	// Штатные логеры
	infoLog *log.Logger
	warnLog *log.Logger
	errLog  *log.Logger

	logFile    *os.File
	ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

type HugoIssue struct {
	Title      string   `toml:"title"`
	Date       string   `toml:"date"`
	Categories []string `toml:"categories"`
	Image      string   `toml:"image"`
	Filename   string   `toml:"filename"`
}

type DescTopic struct {
	Id    ObjectID `json:"id,omitempty"`
	Issue int                `json:"issue,omitempty"`
	Title string             `json:"title"`
	Links []string           `json:"links"`
	Time  string             `json:"time"`
}

type DescIssue struct {
	Issue     int         `json:"issue"`
	Date      string      `json:"date"`
	Audio     string      `json:"audio"`
	Cover     string      `json:"cover"`
	StartTime int64       `json:"start_time"`
	Topics    []DescTopic `json:"topics"`
	Tags      []string    `json:"tags"`
	Verified  bool        `json:"verified,omitempty"`
}

type ChatLine struct {
	Id             ObjectID `json:"id"`
	Issue          int                `json:"issue"`
	Type           string             `json:"type"`
	AuthorType     string             `json:"author_type"`
	AuthorNickname string             `json:"author_nickname,omitempty"`
	AuthorName     string             `json:"author_name"`
	DateTime       int64              `json:"datetime"`
	ImageUrl       string             `json:"image_url,omitempty"`
	ImageWidth     int                `json:"image_width,omitempty"`
	ImageHeight    int                `json:"image_height,omitempty"`
	Text           string             `json:"text"`
}

// Chat aaa
type Chat struct {
	Chat []ChatLine `json:"chat"`
}

// CCLine aaa
type CCLine struct {
	Id     ObjectID `json:"id"`
	Issue  int                `json:"issue"`
	Type   string             `json:"type"`
	Author string             `json:"author"`
	Stime  float64            `json:"stime"`
	Etime  float64            `json:"etime"`
	Text   string             `json:"text"`
}

// Subs aaa
type Subs struct {
	Subs []CCLine `json:"subs"`
}

// ListLine aaa
type ListLine struct {
	Id       int    `json:"id"`
	Date     string `json:"date"`
	Verified bool   `json:"verified,omitempty"`
}

// List aaa
type List struct {
	List []ListLine `json:"list"`
}

// LogWriter записывает логи в консоль (с цветом) и в файл (без цвета)
type LogWriter struct {
	console io.Writer
	file    io.Writer
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	_, _ = w.console.Write(p)
	if w.file != nil {
		clean := ansiRegexp.ReplaceAll(p, nil)
		_, _ = w.file.Write(clean)
	}
	return len(p), nil
}

// Инициализация штатных логеров
func initLogger(issue int) {
	tmpPath := fmt.Sprintf("../../data/%d/tmp", issue)
	_ = os.MkdirAll(tmpPath, os.ModePerm)

	var err error
	logFile, err = os.OpenFile(tmpPath+"/publish.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("Не удалось открыть файл лога: %v\n", err)
	}

	writer := &LogWriter{
		console: os.Stdout,
		file:    logFile,
	}

	infoLog = log.New(writer, "\033[32m[INFO]\033[0m ", log.LstdFlags)
	warnLog = log.New(writer, "\033[33m[WARN]\033[0m ", log.LstdFlags)
	errLog = log.New(writer, "\033[31m[ERROR]\033[0m ", log.LstdFlags)
}

func createIssueDir(issue int) {
	const loc = "createIssueDir"
	infoLog.Printf("(%s) Создание директорий для выпуска %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)
	path := "../../data/" + issueStr
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		err := os.Mkdir(path, os.ModePerm)
		if err != nil {
			errLog.Printf("(%s) Ошибка создания директории %s: %v", loc, path, err)
		} else {
			infoLog.Printf("(%s) Создана директория %s", loc, path)
		}
	}

	tmpPath := path + "/tmp"
	if _, err := os.Stat(tmpPath); errors.Is(err, os.ErrNotExist) {
		err := os.Mkdir(tmpPath, os.ModePerm)
		if err != nil {
			errLog.Printf("(%s) Ошибка создания директории %s: %v", loc, tmpPath, err)
		} else {
			infoLog.Printf("(%s) Создана директория %s", loc, tmpPath)
		}
	}
}

// ccSourceFile возвращает путь к исходному SSA-файлу выпуска для чтения и правок.
// Приоритет: tmp/07_clean.ssa (результат волонтёрской выверки), если он есть;
// иначе — рабочий tmp/06_manual.ssa.
func ccSourceFile() string {
	if _, err := os.Stat(ccCleanSrcFile); err == nil {
		return ccCleanSrcFile
	}
	return ccSrcFile
}

func createDescFile(issue int) {
	const loc = "createDescFile"
	infoLog.Printf("(%s) Обработка описания для выпуска %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)

	// Если N_desc.json уже существует — генерацию из Hugo пропускаем (идемпотентность).
	// Но id тем проверяем/чиним: у старых выпусков они нулевые.
	// Флаг --force-desc/-fd принудительно пересобирает описание из Hugo.
	if !forceDesc {
		if raw, err := os.ReadFile(descFile); err == nil {
			var issueDesc DescIssue
			if json.Unmarshal(raw, &issueDesc) == nil {
				infoLog.Printf("(%s) Файл описания %s уже существует, генерация из Hugo пропущена (для пересоздания: --force-desc или -fd)", loc, descFile)

				// Нулевые id тем — единственное, что правим при пропуске генерации.
				// Данные изменились — только тогда обновляем поисковый файл и Meilisearch.
				if fixed := ensureTopicIDs(&issueDesc, issue); fixed {
					writeDescJSON(issueDesc, loc)
					writeTopicsSearchJSON(issueDesc.Topics, loc)
					descStepExecuted = true
				}

				// Список тем — комментариями в рабочий 06_manual.ssa (если их там ещё нет).
				appendTopicsToSSA(issueDesc.Topics, false, loc)
				return
			}
			warnLog.Printf("(%s) Не удалось декодировать существующий %s, выполняется повторная генерация", loc, descFile)
		}
	}

	// Генерация из Hugo-поста (соседний репозиторий radio-t_site).
	descRawData, err := os.ReadFile(hugoFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения hugo файла %s: %v", loc, hugoFile, err)
		panic(err)
	}

	descBlocks := strings.Split(string(descRawData), "+++")
	if len(descBlocks) < 3 {
		errLog.Printf("(%s) Неверный формат hugo файла (не найдены разделители +++)", loc)
		return
	}
	descTomlData := descBlocks[1]

	var data HugoIssue
	_, err = toml.Decode(descTomlData, &data)
	if err != nil {
		errLog.Printf("(%s) Ошибка декодирования TOML: %v", loc, err)
		panic(err)
	}

	date, err := time.Parse("2006-01-02T15:04:05", data.Date)
	if err != nil {
		warnLog.Printf("(%s) Не удалось распарсить дату %s: %v", loc, data.Date, err)
		date = time.Now()
	}

	// start_time приходит не из Hugo, а из скрейпа чата; при перегенерации
	// описания (--force-desc, нет файла) сохраняем его из старого N_desc.json.
	var prevStartTime int64
	if prevRaw, err := os.ReadFile(descFile); err == nil {
		var prev DescIssue
		if json.Unmarshal(prevRaw, &prev) == nil {
			prevStartTime = prev.StartTime
		}
	}

	issueDesc := DescIssue{
		Issue:     issue,
		Date:      date.Format("2006-01-02"),
		Audio:     "https://cdn.radio-t.com/" + data.Filename + ".mp3",
		Cover:     data.Image,
		StartTime: prevStartTime,
		Topics:    []DescTopic{},
	}

	// id тем при перегенерации сохраняем: сопоставляем по заголовку с уже
	// опубликованным N_desc.json (стабильные ключи, git-friendly).
	existingTopicIDs := loadExistingTopicIDsIndex(descFile)
	topicIndexPos := make(map[string]int, len(existingTopicIDs))
	usedTopicIDs := make(map[ObjectID]bool)

	lines := strings.Split(descBlocks[2], "\n")
	rawTitleRegexp := regexp.MustCompile(`^-\s+(.+)\s+-`)
	titleRegexp := regexp.MustCompile(`^\[(.+)\]`)
	linkRegexp := regexp.MustCompile(`^-.+\((.+)\)`)
	timeRegexp := regexp.MustCompile(`^-.+\*(.+)\*`)
	for _, line := range lines {
		topic := DescTopic{}
		topic.Links = []string{}
		match := rawTitleRegexp.FindStringSubmatch(line)
		if len(match) > 0 {
			topic.Title = match[1]

			match = titleRegexp.FindStringSubmatch(match[1])
			if len(match) > 0 {
				topic.Title = match[1]
			}
		}

		match = linkRegexp.FindStringSubmatch(line)
		if len(match) > 0 {
			topic.Links = append(topic.Links, match[1])
		}

		match = timeRegexp.FindStringSubmatch(line)
		if len(match) > 0 {
			topic.Time = match[1]
		}

		if len(topic.Title) > 0 {
			// id назначаем ДО append (append копирует значение — иначе id теряется)
			// и стараемся сохранить ранее выданный id по заголовку.
			topic.Id = preservedID(strings.TrimSpace(topic.Title), existingTopicIDs, topicIndexPos, usedTopicIDs)
			topic.Issue = issue
			issueDesc.Topics = append(issueDesc.Topics, topic)
		}
	}

	writeDescJSON(issueDesc, loc)
	writeTopicsSearchJSON(issueDesc.Topics, loc)
	descStepExecuted = true

	// Список тем — комментариями в рабочий 06_manual.ssa. При --force-desc
	// перезаписываем имеющиеся комментарии тем, иначе — только если их нет.
	appendTopicsToSSA(issueDesc.Topics, forceDesc, loc)

	infoLog.Printf("(%s) Файлы описания успешно созданы", loc)
}

// loadExistingTopicIDsIndex строит индекс id тем по заголовку из уже сгенерированного
// N_desc.json. Значения — списки: корректно обрабатывает темы с одинаковыми заголовками.
func loadExistingTopicIDsIndex(path string) map[string][]ObjectID {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var desc DescIssue
	if json.Unmarshal(data, &desc) != nil {
		return nil
	}
	index := make(map[string][]ObjectID, len(desc.Topics))
	for _, t := range desc.Topics {
		if t.Id.IsZero() {
			continue
		}
		key := strings.TrimSpace(t.Title)
		if key == "" {
			continue
		}
		index[key] = append(index[key], t.Id)
	}
	return index
}

// preservedID возвращает id сущности (реплики/темы): сначала пытается взять ранее
// выданный по ключу (сопоставление по содержимому, не по позиции), иначе генерирует
// новый. Следит за уникальностью выданных в этом запуске id (важно для doc-id в Meilisearch).
func preservedID[K comparable](key K, index map[K][]ObjectID, pos map[K]int, used map[ObjectID]bool) ObjectID {
	var id ObjectID
	if list, ok := index[key]; ok && pos[key] < len(list) {
		id = list[pos[key]]
		pos[key]++
	} else {
		id = NewObjectID()
	}
	for used[id] {
		id = NewObjectID()
	}
	used[id] = true
	return id
}

// ensureTopicIDs проставляет корректные id (и issue) темам с нулевыми значениями.
// Возвращает true, если что-то было изменено (тогда N_desc.json нужно перезаписать).
func ensureTopicIDs(issueDesc *DescIssue, issue int) bool {
	changed := false
	for i := range issueDesc.Topics {
		if issueDesc.Topics[i].Id.IsZero() {
			issueDesc.Topics[i].Id = NewObjectID()
			changed = true
		}
		if issueDesc.Topics[i].Issue == 0 {
			issueDesc.Topics[i].Issue = issue
			changed = true
		}
	}
	return changed
}

// writeDescJSON сохраняет описание выпуска в N_desc.json.
func writeDescJSON(issueDesc DescIssue, loc string) {
	jsonData, err := json.MarshalIndent(issueDesc, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка маршалинга json для описания: %v", loc, err)
		return
	}
	if err := os.WriteFile(descFile, jsonData, 0644); err != nil {
		errLog.Printf("(%s) Ошибка записи %s: %v", loc, descFile, err)
	}
}

// writeTopicsSearchJSON сохраняет темы в tmp/meili_topics.json (индекс topics).
func writeTopicsSearchJSON(topics []DescTopic, loc string) {
	if topics == nil {
		topics = []DescTopic{}
	}
	jsonData, err := json.MarshalIndent(topics, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка маршалинга json для тем поиска: %v", loc, err)
		return
	}
	if err := os.WriteFile(topicsSearchFile, jsonData, 0644); err != nil {
		errLog.Printf("(%s) Ошибка записи %s: %v", loc, topicsSearchFile, err)
	}
}

// appendTopicsToSSA вставляет темы как Comment-строки в начало [Events] исходного
// SSA-файла выпуска (tmp/07_clean.ssa при наличии, иначе tmp/06_manual.ssa; см.
// ccSourceFile) сразу после Format. replace=false — только если комментариев
// там ещё нет; replace=true — перезаписывает существующие комментарии тем.
// Текст комментария: "<время> - <заголовок>".
func appendTopicsToSSA(topics []DescTopic, replace bool, loc string) {
	srcFile := ccSourceFile()
	if _, err := os.Stat(srcFile); err != nil {
		warnLog.Printf("(%s) Файл %s отсутствует, список тем в SSA не добавлен", loc, srcFile)
		return
	}
	rawData, err := os.ReadFile(srcFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения %s: %v", loc, srcFile, err)
		return
	}

	updated, changed := insertTopicComments(string(rawData), topics, replace)
	if !changed {
		infoLog.Printf("(%s) Вставка тем в %s не требуется (темы уже есть, нет тем или нет нужных колонок)", loc, srcFile)
		return
	}
	if err := os.WriteFile(srcFile, []byte(updated), 0644); err != nil {
		errLog.Printf("(%s) Ошибка записи %s: %v", loc, srcFile, err)
		return
	}
	infoLog.Printf("(%s) Список тем добавлен комментариями в %s", loc, srcFile)
}

// insertTopicComments возвращает текст SSA с добавленными Comment-строками тем и
// признак изменений. Вставка идёт сразу после Format в [Events] (в начало списка).
// replace=false: если комментарии уже есть — без изменений; replace=true: существующие
// комментарии в [Events] удаляются и вставляются заново. changed=false, если вставлять
// нечего (нет тем с заголовком или нет нужных колонок).
func insertTopicComments(text string, topics []DescTopic, replace bool) (string, bool) {
	doc := parseSSADoc(text)
	_, okStart := doc.cols["start"]
	_, okEnd := doc.cols["end"]
	_, okText := doc.cols["text"]
	if !okStart || !okEnd || !okText {
		return text, false
	}

	comments := make([]string, 0, len(topics))
	for _, t := range topics {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			continue
		}
		// Темы без таймкода не пропускаем: пишем их в том же порядке с временем 0:00:00.
		tm := strings.TrimSpace(t.Time)
		if tm == "" {
			tm = "0:00:00"
		}
		comments = append(comments, topicCommentLine(doc, tm+" - "+title))
	}
	if len(comments) == 0 {
		return text, false
	}

	eventsIdx, formatIdx := -1, -1
	inEvents := false
	hasComment := false
	for i, raw := range doc.lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inEvents = strings.EqualFold(line, "[Events]")
			if inEvents {
				eventsIdx = i
			}
			continue
		}
		if !inEvents {
			continue
		}
		if strings.HasPrefix(line, "Format:") {
			if formatIdx == -1 {
				formatIdx = i
			}
			continue
		}
		if header, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(header), "Comment") {
			hasComment = true
		}
	}

	// Комментарии уже есть — перезаписываем только по запросу (replace).
	if hasComment && !replace {
		return text, false
	}

	insertAt := formatIdx
	if insertAt == -1 {
		insertAt = eventsIdx
	}
	if insertAt == -1 {
		return text, false
	}

	newLines := make([]string, 0, len(doc.lines)+len(comments))
	inEvents = false
	for i, raw := range doc.lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inEvents = strings.EqualFold(line, "[Events]")
		} else if inEvents && replace {
			if header, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(header), "Comment") {
				continue // выкидываем старые комментарии тем при перезаписи
			}
		}
		newLines = append(newLines, raw)
		if i == insertAt {
			newLines = append(newLines, comments...)
		}
	}
	return doc.render(newLines), true
}

// topicCommentLine собирает SSA-строку Comment с числом полей, равным Format [Events]
// (иначе go-astisub не сможет разобрать файл). Текст темы — в колонке Text.
func topicCommentLine(doc ssaDoc, text string) string {
	n := doc.fieldCount
	if n <= 0 {
		n = 10
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "0"
	}
	if idx, ok := doc.cols["start"]; ok && idx < n {
		parts[idx] = "0:00:00.00"
	}
	if idx, ok := doc.cols["end"]; ok && idx < n {
		parts[idx] = "0:00:00.00"
	}
	if idx, ok := doc.cols["style"]; ok && idx < n {
		parts[idx] = "Default"
	}
	if idx, ok := doc.cols["name"]; ok && idx < n {
		parts[idx] = ""
	}
	if idx, ok := doc.cols["effect"]; ok && idx < n {
		parts[idx] = ""
	}
	if idx, ok := doc.cols["text"]; ok && idx < n {
		parts[idx] = text
	}
	return "Comment: " + strings.Join(parts, ",")
}

func writeEmptyChatFiles() {
	emptyChat := Chat{Chat: []ChatLine{}}
	jsonData, err := json.MarshalIndent(emptyChat, "", "  ")
	if err == nil {
		_ = os.WriteFile(chatJsonFile, jsonData, 0644)
	}
	jsonDataSearch, err := json.MarshalIndent(emptyChat.Chat, "", "  ")
	if err == nil {
		_ = os.WriteFile(chatSearchFile, jsonDataSearch, 0644)
	}
}

// chatKey — ключ сопоставления реплик чата по содержимому.
// Используется, чтобы при пересоздании N_chat.json (--force-chat) id реплик
// оставались стабильными (git-friendly).
type chatKey struct {
	datetime int64
	nickname string
	name     string
	text     string
}

// chatKeyFromLine строит ключ сопоставления по полям реплики чата.
func chatKeyFromLine(c ChatLine) chatKey {
	return chatKey{datetime: c.DateTime, nickname: c.AuthorNickname, name: c.AuthorName, text: c.Text}
}

// loadExistingChatIDsIndex строит индекс id по содержимому из уже сгенерированного N_chat.json.
// Значения хранятся списками: корректно обрабатывает одинаковые реплики (в порядке появления).
func loadExistingChatIDsIndex(path string) map[chatKey][]ObjectID {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var chat Chat
	if err := json.Unmarshal(data, &chat); err != nil {
		return nil
	}
	index := make(map[chatKey][]ObjectID, len(chat.Chat))
	for _, c := range chat.Chat {
		if c.Id.IsZero() {
			continue
		}
		k := chatKeyFromLine(c)
		index[k] = append(index[k], c.Id)
	}
	return index
}

func createChatFile(issue int) {
	const loc = "createChatFile"
	infoLog.Printf("(%s) Сбор данных чата для выпуска %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)

	// Шаг идемпотентен: если N_chat.json уже есть и содержит реплики, повторно его
	// не собираем. Пересоздание — только с флагом --force-chat/-fc (id реплик при
	// этом сохраняются). Пустой/битый файл (артефакт неудачного скрейпа) собираем заново.
	if !forceChat {
		if data, err := os.ReadFile(chatJsonFile); err == nil {
			var existing Chat
			if json.Unmarshal(data, &existing) == nil && len(existing.Chat) > 0 {
				infoLog.Printf("(%s) Файл чата %s уже существует (%d реплик), шаг пропущен (для пересоздания: --force-chat или -fc)", loc, chatJsonFile, len(existing.Chat))
				return
			}
			warnLog.Printf("(%s) Файл чата %s существует, но пуст или повреждён — выполняется повторный сбор", loc, chatJsonFile)
		}
	}

	// Шаг выполняется: N_chat.json и tmp/meili_chat.json будут пересобраны,
	// поэтому индекс chat_msgs нужно перезалить (см. updateSearchData).
	chatStepExecuted = true

	var hasError bool
	defer func() {
		if r := recover(); r != nil {
			errLog.Printf("(%s) Паника при обработке чата: %v. Создаются пустые файлы чата", loc, r)
			writeEmptyChatFiles()
		} else if hasError {
			warnLog.Printf("(%s) Процесс завершился с ошибкой. Создаются пустые файлы чата", loc)
			writeEmptyChatFiles()
		}
	}()

	if timezone != "" {
		location, err := time.LoadLocation(timezone)
		if err != nil {
			errLog.Printf("(%s) Ошибка загрузки таймзоны %s: %v", loc, timezone, err)
			hasError = true
			return
		}
		time.Local = location
	}

	descRawData, err := os.ReadFile(descFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения описания выпуска: %v", loc, err)
		hasError = true
		return
	}
	var descIssue DescIssue
	err = json.Unmarshal(descRawData, &descIssue)
	if err != nil {
		errLog.Printf("(%s) Ошибка декодирования json описания выпуска: %v", loc, err)
		hasError = true
		return
	}

	datetimeNoon, err := dateparse.ParseLocal(descIssue.Date + " 12:00:00")
	if err != nil {
		errLog.Printf("(%s) Ошибка парсинга полудня: %v", loc, err)
		hasError = true
		return
	}
	datetimeNoonUnix := datetimeNoon.Unix()

	c := colly.NewCollector(
		colly.AllowedDomains("chat.radio-t.com"),
	)

	// Чтение настроек прокси из переменных окружения HTTP_PROXY или HTTPS_PROXY
	proxyURL := os.Getenv("HTTP_PROXY")
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTPS_PROXY")
	}
	if proxyURL != "" {
		if err := c.SetProxy(proxyURL); err != nil {
			warnLog.Printf("(%s) Ошибка настройки прокси %s: %v", loc, proxyURL, err)
		} else {
			infoLog.Printf("(%s) Использование прокси для загрузки: %s", loc, proxyURL)
		}
	}

	imgRegexp := regexp.MustCompile(`<img([\w\W]+?)/>`)
	startTimeRegexp := regexp.MustCompile(`.*Вещание подкаста началось.*`)

	// Индекс id из уже существующего N_chat.json — для стабильных id при пересоздании
	// (--force-chat). Сопоставление по содержимому реплики, а не по позиции.
	existingIndex := loadExistingChatIDsIndex(chatJsonFile)
	indexPos := make(map[chatKey]int, len(existingIndex))
	usedIDs := make(map[ObjectID]bool)

	chatParsed := false

	c.OnHTML("table.table", func(table *colly.HTMLElement) {
		chatParsed = true
		chat := &Chat{Chat: []ChatLine{}}

		table.ForEach("tr", func(_ int, tr *colly.HTMLElement) {
			chatLine := ChatLine{
				Issue:          issue,
				Type:           "chat",
				AuthorType:     "listener",
				AuthorNickname: "",
				AuthorName:     "",
				DateTime:       0,
				Text:           "",
			}

			tr.ForEach("td[align]", func(j int, item *colly.HTMLElement) {
				switch j {
				case 0:
					timeStr := item.Text
					datetime, err := dateparse.ParseLocal(descIssue.Date + " " + timeStr)
					if err != nil {
						warnLog.Printf("(%s) Не удалось распарсить время сообщения %s: %v", loc, timeStr, err)
						return
					}

					datetimeUnix := datetime.Unix()
					if datetimeUnix < datetimeNoonUnix {
						datetimeUnix += 86400
					}

					chatLine.DateTime = datetimeUnix
				case 1:
					chatLine.AuthorNickname = item.ChildAttr("span", "title")
					chatLine.AuthorName = strings.Trim(item.Text, " \n")
				case 2:
					content, _ := item.DOM.Html()

					imgUrl := item.ChildAttr("img", "src")
					if imgUrl != "" {
						chatLine.ImageUrl = "https://chat.radio-t.com/logs/" + imgUrl
						chatLine.ImageWidth, err = strconv.Atoi(item.ChildAttr("img", "width"))
						if err != nil {
							warnLog.Printf("(%s) Невалидная ширина картинки: %s", loc, content)
						}
						chatLine.ImageHeight, err = strconv.Atoi(item.ChildAttr("img", "height"))
						if err != nil {
							warnLog.Printf("(%s) Невалидная высота картинки: %s", loc, content)
						}

						content = imgRegexp.ReplaceAllString(content, "")
					}

					content = strings.Trim(content, " \n")
					content = strings.Replace(content, "src=\""+issueStr+"/", "src=\"https://chat.radio-t.com/logs/"+issueStr+"/", -1)
					chatLine.Text = content
				}
			})

			trClasses := strings.Split(tr.Attr("class"), " ")
			if slices.Contains(trClasses, "host") ||
				slices.Contains(hostIds, chatLine.AuthorNickname) ||
				slices.Contains(hostNames, chatLine.AuthorName) {

				chatLine.AuthorType = "host"
			}

			if slices.Contains(trClasses, "bot") ||
				slices.Contains(botIds, chatLine.AuthorNickname) ||
				slices.Contains(botNames, chatLine.AuthorName) {

				chatLine.AuthorType = "bot"
			}

			// Стабильный id: сопоставляем реплику с уже опубликованным N_chat.json
			// по содержимому (время/ник/имя/текст). Списки — для одинаковых реплик.
			chatLine.Id = preservedID(chatKeyFromLine(chatLine), existingIndex, indexPos, usedIDs)

			chat.Chat = append(chat.Chat, chatLine)

			match := startTimeRegexp.FindStringSubmatch(chatLine.Text)
			if chatLine.AuthorNickname == "radiot_superbot" && len(match) > 0 {
				descIssue.StartTime = chatLine.DateTime
				jsonData, err := json.MarshalIndent(descIssue, "", "  ")
				if err != nil {
					errLog.Printf("(%s) Ошибка маршалинга обновленного описания: %v", loc, err)
					return
				}
				_ = os.WriteFile(descFile, jsonData, 0644)
			}
		})

		jsonData, err := json.MarshalIndent(chat, "", "  ")
		if err != nil {
			errLog.Printf("(%s) Ошибка маршалинга JSON чата: %v", loc, err)
			return
		}
		_ = os.WriteFile(chatJsonFile, jsonData, 0644)

		jsonData, err = json.MarshalIndent(chat.Chat, "", "  ")
		if err != nil {
			errLog.Printf("(%s) Ошибка маршалинга JSON поиска чата: %v", loc, err)
			return
		}
		_ = os.WriteFile(chatSearchFile, jsonData, 0644)
	})

	err = c.Visit(chatFileURL)
	if err != nil {
		errLog.Printf("(%s) Ошибка скачивания страницы чата %s: %v", loc, chatFileURL, err)
		hasError = true
		return
	}

	if !chatParsed {
		errLog.Printf("(%s) Таблица чата не найдена на странице %s", loc, chatFileURL)
		hasError = true
	} else {
		infoLog.Printf("(%s) Файлы чата успешно обновлены", loc)
	}
}

func writeEmptyCcFiles() {
	emptySubs := Subs{Subs: []CCLine{}}
	jsonData, err := json.MarshalIndent(emptySubs, "", "  ")
	if err == nil {
		_ = os.WriteFile(ccJsonFile, jsonData, 0644)
	}
	jsonDataSearch, err := json.MarshalIndent(emptySubs.Subs, "", "  ")
	if err == nil {
		_ = os.WriteFile(ccSearchFile, jsonDataSearch, 0644)
	}
}

// ssaDoc — минимальный текстовый разбор SSA для доступа к полям Dialogue по имени колонки.
// Используется, т.к. go-astisub жёстко пишет фиксированный набор полей, а нам нужно
// читать/писать произвольные поля (Style, Effect) и формировать урезанный N_cc.ssa.
type ssaDoc struct {
	lines      []string       // строки файла (без завершающих \r)
	cols       map[string]int // имя колонки (в нижнем регистре) -> индекс в Dialogue
	fieldCount int            // число колонок в Format блока [Events]
	dialogues  []int          // индексы строк-Dialogue в lines
	eol        string         // разделитель строк, определённый по исходнику
}

// parseSSADoc разбирает текст SSA: колонки Format блока [Events] и строки-Dialogue.
func parseSSADoc(text string) ssaDoc {
	d := ssaDoc{cols: map[string]int{}, eol: "\n"}
	if strings.Contains(text, "\r\n") {
		d.eol = "\r\n"
	}

	for _, raw := range strings.Split(text, "\n") {
		d.lines = append(d.lines, strings.TrimSuffix(raw, "\r"))
	}

	inEvents := false
	for i, raw := range d.lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inEvents = strings.EqualFold(line, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}
		if strings.HasPrefix(line, "Format:") {
			names := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "Format:")), ",")
			d.fieldCount = len(names)
			d.cols = make(map[string]int, len(names))
			for idx, n := range names {
				d.cols[strings.ToLower(strings.TrimSpace(n))] = idx
			}
			continue
		}
		if header, _, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(header) == "Dialogue" {
			d.dialogues = append(d.dialogues, i)
		}
	}
	return d
}

// dialogueParts разбивает строку-Dialogue li на префикс (до содержимого) и поля.
// Полей ровно fieldCount; последнее поле (Text) поглощает лишние запятые.
func (d ssaDoc) dialogueParts(li int) (prefix string, parts []string, ok bool) {
	if li < 0 || li >= len(d.lines) {
		return "", nil, false
	}
	line := d.lines[li]
	ci := strings.Index(line, ":")
	if ci < 0 {
		return "", nil, false
	}
	rest := line[ci+1:]
	lead := len(rest) - len(strings.TrimLeft(rest, " \t"))
	prefix = line[:ci+1+lead]
	parts = strings.Split(rest[lead:], ",")
	if d.fieldCount > 0 && len(parts) > d.fieldCount {
		parts[d.fieldCount-1] = strings.Join(parts[d.fieldCount-1:], ",")
		parts = parts[:d.fieldCount]
	}
	return prefix, parts, true
}

// render собирает текст файла из строк lines, используя исходный разделитель строк.
func (d ssaDoc) render(lines []string) string {
	return strings.Join(lines, d.eol)
}

// loadDescTopics читает список тем из N_desc.json (пусто при отсутствии/ошибке).
func loadDescTopics(path string) []DescTopic {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var desc DescIssue
	if json.Unmarshal(data, &desc) != nil {
		return nil
	}
	return desc.Topics
}

// buildMinimalSSA формирует публикуемый N_cc.ssa. Используется **стандартный** набор
// полей SSA-события (Format: Marked, Start, End, Style, Name, MarginL, MarginR,
// MarginV, Effect, Text): содержательно заполнены только Start/End/Style (id
// реплики)/Name/Text, остальные — нулевые/пустые (Aegisub требует непустые числа).
// В начало [Events] добавляются комментарии со списком тем (как в исходном SSA).
func buildMinimalSSA(doc ssaDoc, ids []string, topics []DescTopic) string {
	startIdx, okStart := doc.cols["start"]
	endIdx, okEnd := doc.cols["end"]
	nameIdx, okName := doc.cols["name"]
	textIdx, okText := doc.cols["text"]
	if !okStart || !okEnd || !okName || !okText {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Script Info]")
	b.WriteString(doc.eol)
	b.WriteString("ScriptType: v4.00")
	b.WriteString(doc.eol)
	b.WriteString(doc.eol)
	b.WriteString("[Events]")
	b.WriteString(doc.eol)
	b.WriteString("Format: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text")
	b.WriteString(doc.eol)

	// Список тем — комментариями в начале списка (текст: «время - заголовок»).
	for _, t := range topics {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			continue
		}
		tm := strings.TrimSpace(t.Time)
		if tm == "" {
			tm = "0:00:00"
		}
		b.WriteString(ssaEventLine("Comment", "0:00:00.00", "0:00:00.00", "Default", "", tm+" - "+title))
		b.WriteString(doc.eol)
	}

	for i, li := range doc.dialogues {
		if i >= len(ids) {
			break
		}
		_, parts, ok := doc.dialogueParts(li)
		if !ok || startIdx >= len(parts) || endIdx >= len(parts) || nameIdx >= len(parts) || textIdx >= len(parts) {
			continue
		}
		b.WriteString(ssaEventLine("Dialogue", parts[startIdx], parts[endIdx], ids[i], parts[nameIdx], parts[textIdx]))
		b.WriteString(doc.eol)
	}
	return b.String()
}

// ssaEventLine собирает строку SSA-события (Comment/Dialogue) со стандартным набором
// полей: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text.
// Числовые поля (MarginL/R/V) нельзя оставлять пустыми — Aegisub падает с
// «bad lexical cast: source type value could not be interpreted as target»,
// поэтому они равны 0; Marked=0; пустым остаётся только Effect (строка).
func ssaEventLine(kind, start, end, style, name, text string) string {
	return kind + ": " + strings.Join([]string{"Marked=0", start, end, style, name, "0", "0", "0", "", text}, ",")
}

// cueKey — ключ сопоставления реплик по таймингам (сотые доли секунды).
type cueKey struct {
	start int64
	end   int64
}

// cueKeyFromDuration переводит тайминги astisub в ключ (сотые доли секунды).
func cueKeyFromDuration(start, end time.Duration) cueKey {
	const cs = 10 * time.Millisecond
	return cueKey{start: int64(start / cs), end: int64(end / cs)}
}

// cueKeyFromSeconds переводит секунды из N_cc.json в ключ (сотые доли секунды).
func cueKeyFromSeconds(start, end float64) cueKey {
	return cueKey{start: int64(math.Round(start * 100)), end: int64(math.Round(end * 100))}
}

// loadExistingCcIDsIndex строит индекс id по таймингам из уже сгенерированного N_cc.json.
// Значения хранятся списками: корректно обрабатывает реплики с одинаковыми таймингами.
func loadExistingCcIDsIndex(path string) map[cueKey][]ObjectID {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var subs Subs
	if err := json.Unmarshal(data, &subs); err != nil {
		return nil
	}
	index := make(map[cueKey][]ObjectID, len(subs.Subs))
	for _, s := range subs.Subs {
		if s.Id.IsZero() {
			continue
		}
		k := cueKeyFromSeconds(s.Stime, s.Etime)
		index[k] = append(index[k], s.Id)
	}
	return index
}

// parseObjectID распознаёт непустой hex ObjectID (24 символа). "0" и мусор → false.
func parseObjectID(s string) (ObjectID, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 24 {
		return NilObjectID, false
	}
	id, err := ObjectIDFromHex(s)
	if err != nil || id.IsZero() {
		return NilObjectID, false
	}
	return id, true
}

func createCcFile(issue int) {
	const loc = "createCcFile"
	infoLog.Printf("(%s) Обработка субтитров для выпуска %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)

	var hasError bool
	defer func() {
		if r := recover(); r != nil {
			errLog.Printf("(%s) Паника при обработке субтитров: %v. Создаются пустые файлы субтитров", loc, r)
			writeEmptyCcFiles()
		} else if hasError {
			warnLog.Printf("(%s) Процесс завершился с ошибкой. Создаются пустые файлы субтитров", loc)
			writeEmptyCcFiles()
		}
	}()

	srcFile := ccSourceFile()
	infoLog.Printf("(%s) Исходный SSA-файл: %s", loc, srcFile)
	if _, err := os.Stat(srcFile); err != nil {
		warnLog.Printf("(%s) Файл субтитров %s отсутствует. Создаем файлы с пустым массивом", loc, srcFile)
		hasError = true
		return
	}

	rawData, err := os.ReadFile(srcFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения файла субтитров %s: %v", loc, srcFile, err)
		hasError = true
		return
	}

	// id реплик хранятся в поле Style исходного SSA (hex ObjectID).
	// Стабильные id нужны, чтобы при перегенерации N_cc.json не менялись ключи (git-friendly).
	doc := parseSSADoc(string(rawData))
	styleIdx, hasStyle := doc.cols["style"]
	effectIdx, hasEffect := doc.cols["effect"]
	if !hasStyle {
		warnLog.Printf("(%s) В %s нет колонки Style — id реплик не будут персистентными", loc, srcFile)
	}

	parsed, err := as.ReadFromSSA(strings.NewReader(string(rawData)))
	if err != nil {
		errLog.Printf("(%s) Ошибка парсинга ssa-файла %s: %v", loc, srcFile, err)
		hasError = true
		return
	}

	if len(parsed.Items) != len(doc.dialogues) {
		warnLog.Printf("(%s) Число разобранных реплик %d не совпадает с числом Dialogue-строк %d", loc, len(parsed.Items), len(doc.dialogues))
	}

	existingIndex := loadExistingCcIDsIndex(ccJsonFile)
	indexPos := make(map[cueKey]int, len(existingIndex))

	updated := make([]string, len(doc.lines))
	copy(updated, doc.lines)
	persist := false

	subs := &Subs{Subs: []CCLine{}}
	ids := make([]string, len(parsed.Items))
	usedIDs := make(map[ObjectID]bool, len(parsed.Items))

	for idx, item := range parsed.Items {
		var styleVal string
		if hasStyle && idx < len(doc.dialogues) {
			if _, parts, ok := doc.dialogueParts(doc.dialogues[idx]); ok && styleIdx < len(parts) {
				styleVal = strings.TrimSpace(parts[styleIdx])
			}
		}
		styleID, styleIsID := parseObjectID(styleVal)

		var id ObjectID
		var ok bool

		// (1) Совпадение по таймингам с уже опубликованным N_cc.json: сохраняем ранее
		//     выданный id. Сопоставление по времени, а не по позиции — вставка/удаление
		//     реплики не сдвигает id остальных.
		key := cueKeyFromDuration(item.StartAt, item.EndAt)
		if list, found := existingIndex[key]; found && indexPos[key] < len(list) {
			id, ok = list[indexPos[key]], true
			indexPos[key]++
		}
		// (2) Иначе — id из поля Style (например, если тайминги реплики изменили).
		if !ok && styleIsID {
			id, ok = styleID, true
		}
		// (3) Иначе (первый запуск) — новый id.
		if !ok {
			id = NewObjectID()
		}
		// Уникальность id (важно для doc-id в Meilisearch).
		for usedIDs[id] {
			id = NewObjectID()
		}
		usedIDs[id] = true

		// Синхронизируем поле Style исходного SSA с выбранным id; старое
		// значение Style (если это не id) уносим в начало Effect.
		if hasStyle && idx < len(doc.dialogues) && styleVal != id.Hex() {
			li := doc.dialogues[idx]
			if prefix, parts, okp := doc.dialogueParts(li); okp {
				if hasEffect && effectIdx < len(parts) && styleVal != "" && !styleIsID {
					parts[effectIdx] = strings.TrimSpace(styleVal + " " + parts[effectIdx])
				}
				parts[styleIdx] = id.Hex()
				updated[li] = prefix + strings.Join(parts, ",")
				persist = true
			}
		}

		ids[idx] = id.Hex()

		var author string
		if len(item.Lines) > 0 {
			author = item.Lines[0].VoiceName
		}

		subs.Subs = append(subs.Subs, CCLine{
			Id:     id,
			Issue:  issue,
			Type:   "cc",
			Author: author,
			Stime:  item.StartAt.Seconds(),
			Etime:  item.EndAt.Seconds(),
			Text:   fmt.Sprintf("%s", item),
		})
	}

	// Сохраняем id (и перенос старых Style в Effect) обратно в исходный SSA-файл
	if persist {
		if err := os.WriteFile(srcFile, []byte(doc.render(updated)), 0644); err != nil {
			warnLog.Printf("(%s) Не удалось сохранить id в %s: %v", loc, srcFile, err)
		} else {
			infoLog.Printf("(%s) id реплик сохранены в поле Style файла %s", loc, srcFile)
		}
	}

	// Публикуемый N_cc.ssa: только Start, End, Style (id), Name, Text + темы комментариями
	if minimal := buildMinimalSSA(doc, ids, loadDescTopics(descFile)); minimal != "" {
		_ = os.WriteFile(ccSsaFile, []byte(minimal), 0644)
	} else {
		warnLog.Printf("(%s) Не удалось сформировать минимальный N_cc.ssa (нет нужных колонок)", loc)
	}

	jsonData, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка маршалинга JSON субтитров: %v", loc, err)
		hasError = true
		return
	}
	_ = os.WriteFile(ccJsonFile, jsonData, 0644)

	jsonData, err = json.MarshalIndent(subs.Subs, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка маршалинга JSON поиска субтитров: %v", loc, err)
		hasError = true
		return
	}
	_ = os.WriteFile(ccSearchFile, jsonData, 0644)
	infoLog.Printf("(%s) Файлы субтитров успешно созданы", loc)
}

func updateListFile(issue int) {
	const loc = "updateListFile"
	infoLog.Printf("(%s) Обновление общего списка выпусков %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)

	listRawData, err := os.ReadFile(listFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения списка выпусков %s: %v", loc, listFile, err)
		panic(err)
	}

	var listData List
	err = json.Unmarshal(listRawData, &listData)
	if err != nil {
		errLog.Printf("(%s) Ошибка декодирования json списка выпусков: %v", loc, err)
		panic(err)
	}

	descRawData, err := os.ReadFile(descFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения описания выпуска %s: %v", loc, descFile, err)
		panic(err)
	}

	var descData DescIssue
	err = json.Unmarshal(descRawData, &descData)
	if err != nil {
		errLog.Printf("(%s) Ошибка декодирования json описания выпуска: %v", loc, err)
		panic(err)
	}

	var listLine = ListLine{
		Id:   descData.Issue,
		Date: descData.Date,
	}

	listData.List = append(listData.List, listLine)

	sort.Slice(listData.List, func(i, j int) bool {
		return listData.List[i].Id > listData.List[j].Id
	})

	var uniqueListData List
	var index = make(map[int]bool)
	for _, item := range listData.List {
		if _, ok := index[item.Id]; !ok {
			index[item.Id] = true
			uniqueListData.List = append(uniqueListData.List, item)
		}
	}

	listJsonData, err := json.MarshalIndent(uniqueListData, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка сериализации списка: %v", loc, err)
		return
	}

	err = os.WriteFile(listFile, listJsonData, 0644)
	if err != nil {
		errLog.Printf("(%s) Ошибка записи файла списка %s: %v", loc, listFile, err)
	} else {
		infoLog.Printf("(%s) Список выпусков успешно обновлен", loc)
	}
}

func updateSearchData(issueNumber int) {
	const loc = "updateSearchData"
	infoLog.Printf("(%s) Обновление индексов Meilisearch для выпуска %d", loc, issueNumber)

	meiliURL := os.Getenv("MEILI_URL")
	meiliKey := os.Getenv("MEILI_KEY")

	if meiliURL == "" {
		warnLog.Printf("(%s) Переменная окружения MEILI_URL не задана, обновление Meilisearch пропущено", loc)
		return
	}

	filesMap := []struct {
		filePath  string
		indexName string
		enabled   bool
	}{
		{
			filePath:  topicsSearchFile,
			indexName: "topics",
			enabled:   descStepExecuted,
		},
		{
			filePath:  chatSearchFile,
			indexName: "chat_msgs",
			enabled:   chatStepExecuted,
		},
		{
			filePath:  ccSearchFile,
			indexName: "cc_msgs",
			enabled:   true,
		},
	}

	// Клонируем стандартный HTTP-транспорт и принудительно отключаем прокси (Proxy: nil)
	// для отправки запросов напрямую на хост Meilisearch.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	for _, item := range filesMap {
		if !item.enabled {
			infoLog.Printf("(%s) Индекс %s: соответствующий шаг публикации пропущен, обновление отменено (ни удаление, ни загрузка не выполняются)", loc, item.indexName)
			continue
		}

		data, err := os.ReadFile(item.filePath)
		var docs []interface{}

		if err != nil {
			warnLog.Printf("(%s) Файл %s не существует или поврежден: %v. Создаем пустой массив в файле", loc, item.filePath, err)
			emptyData := []byte("[]")
			_ = os.WriteFile(item.filePath, emptyData, 0644)
			data = emptyData
		} else {
			if err := json.Unmarshal(data, &docs); err != nil {
				warnLog.Printf("(%s) Ошибка парсинга JSON в файле %s: %v. Перезапись пустым массивом", loc, item.filePath, err)
				emptyData := []byte("[]")
				_ = os.WriteFile(item.filePath, emptyData, 0644)
				data = emptyData
				docs = nil
			}
		}

		if len(docs) == 0 {
			infoLog.Printf("(%s) Индекс %s: данные отсутствуют (пустой массив), отправка запросов в Meilisearch пропущена", loc, item.indexName)
			continue
		}

		// 1. Удаление старых документов
		deleteURL := fmt.Sprintf("%s/indexes/%s/documents/delete", strings.TrimSuffix(meiliURL, "/"), item.indexName)
		deletePayload, err := json.Marshal(map[string]string{
			"filter": fmt.Sprintf("issue = %d", issueNumber),
		})
		if err != nil {
			errLog.Printf("(%s) Не удалось подготовить Payload для удаления из %s: %v", loc, item.indexName, err)
			continue
		}

		reqDel, err := http.NewRequest("POST", deleteURL, bytes.NewBuffer(deletePayload))
		if err != nil {
			errLog.Printf("(%s) Не удалось создать запрос удаления для %s: %v", loc, item.indexName, err)
			continue
		}
		reqDel.Header.Set("Content-Type", "application/json")
		if meiliKey != "" {
			reqDel.Header.Set("Authorization", "Bearer "+meiliKey)
		}

		respDel, err := client.Do(reqDel)
		if err != nil {
			errLog.Printf("(%s) Ошибка отправки запроса удаления в %s: %v", loc, item.indexName, err)
			continue
		}
		respDelBody, _ := io.ReadAll(respDel.Body)
		respDel.Body.Close()

		if respDel.StatusCode >= 200 && respDel.StatusCode < 300 {
			infoLog.Printf("(%s) Запрос на удаление отправлен в индекс %s. Ответ: %s", loc, item.indexName, string(respDelBody))
		} else {
			errLog.Printf("(%s) Ошибка удаления старых данных из индекса %s. Код: %s, Ответ: %s", loc, item.indexName, respDel.Status, string(respDelBody))
		}

		// 2. Загрузка новых документов
		uploadURL := fmt.Sprintf("%s/indexes/%s/documents", strings.TrimSuffix(meiliURL, "/"), item.indexName)
		reqUp, err := http.NewRequest("POST", uploadURL, bytes.NewBuffer(data))
		if err != nil {
			errLog.Printf("(%s) Не удалось создать запрос загрузки для %s: %v", loc, item.indexName, err)
			continue
		}
		reqUp.Header.Set("Content-Type", "application/json")
		if meiliKey != "" {
			reqUp.Header.Set("Authorization", "Bearer "+meiliKey)
		}

		respUp, err := client.Do(reqUp)
		if err != nil {
			errLog.Printf("(%s) Ошибка отправки запроса загрузки в %s: %v", loc, item.indexName, err)
			continue
		}
		respUpBody, _ := io.ReadAll(respUp.Body)
		respUp.Body.Close()

		if respUp.StatusCode >= 200 && respUp.StatusCode < 300 {
			infoLog.Printf("(%s) Данные успешно добавлены в индекс %s. Ответ: %s", loc, item.indexName, string(respUpBody))
		} else {
			errLog.Printf("(%s) Ошибка загрузки данных в индекс %s. Код: %s, Ответ: %s", loc, item.indexName, respUp.Status, string(respUpBody))
		}
	}
}

// loadEnvFile подгружает переменные из .env в корне репозитория.
// Формат: строки KEY=VALUE; пустые строки и строки, начинающиеся с '#', игнорируются.
// Значения в кавычках раздеваются; уже заданные в окружении переменные не перезаписываются.
func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // .env нет — работаем только с переменными окружения
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Использование: go run main.go <номер_выпуска> [--force-desc|-fd] [--force-chat|-fc]")
		os.Exit(1)
	}

	issueNumber, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Printf("Неверный формат номера выпуска: %v\n", err)
		os.Exit(1)
	}

	// Флаги: --force-desc/-fd — пересоздать N_desc.json из Hugo;
	// --force-chat/-fc — пересоздать N_chat.json (id реплик сохраняются).
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--force-desc", "-fd":
			forceDesc = true
		case "--force-chat", "-fc":
			forceChat = true
		default:
			fmt.Printf("Неизвестный аргумент: %s\n", arg)
		}
	}

	// Загрузка общего .env проекта (корень репозитория)
	loadEnvFile(envFile)

	// Инициализация логгеров
	initLogger(issueNumber)
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	const loc = "main"
	infoLog.Printf("(%s) Начало процесса публикации выпуска %d", loc, issueNumber)

	createIssueDir(issueNumber)
	createDescFile(issueNumber)
	createChatFile(issueNumber)
	createCcFile(issueNumber)
	updateListFile(issueNumber)
	updateSearchData(issueNumber)

	infoLog.Printf("(%s) Процесс публикации выпуска %d завершен", loc, issueNumber)
}
