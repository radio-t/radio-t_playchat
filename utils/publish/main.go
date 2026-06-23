package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"golang.org/x/exp/slices"

	"github.com/araddon/dateparse"
	"github.com/gocolly/colly"

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

	ccSrcFile = "../../data/" + issueStr + "/tmp/06_manual.ssa"
	ccSsaFile = "../../data/" + issueStr + "/" + issueStr + "_cc.ssa"
	// jsonFile = "../../data/" + issueStr + "/src/rt_podcast" + issueStr + ".json"
	ccJsonFile   = "../../data/" + issueStr + "/" + issueStr + "_cc.json"
	ccSearchFile = "../../data/" + issueStr + "/tmp/meili_cc.json"

	listFile = "../../data/list.json"

	timezone  = "Europe/Moscow"
	issueDate string
	hostIds   = []string{"umputun", "bobuk", "grayodesa", "alek_sys"}
	hostNames = []string{"Ksenia"}
	botIds    = []string{"radiot_superbot"}
	botNames  = []string{}

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
	Id    primitive.ObjectID `json:"id,omitempty"`
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
	Id             primitive.ObjectID `json:"id"`
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
	Id     primitive.ObjectID `json:"id"`
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

func createDescFile(issue int) {
	const loc = "createDescFile"
	infoLog.Printf("(%s) Обработка описания для выпуска %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)

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

	var issueDesc = DescIssue{
		Issue:     issue,
		Date:      date.Format("2006-01-02"),
		Audio:     "https://cdn.radio-t.com/" + data.Filename + ".mp3",
		Cover:     data.Image,
		StartTime: 0,
		Topics:    []DescTopic{},
	}

	var searchTopics = []DescTopic{}

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
			issueDesc.Topics = append(issueDesc.Topics, topic)

			topic.Id = primitive.NewObjectID()
			topic.Issue = issue

			searchTopics = append(searchTopics, topic)
		}
	}

	jsonData, err := json.MarshalIndent(issueDesc, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка маршалинга json для описания: %v", loc, err)
		return
	}
	_ = os.WriteFile(descFile, jsonData, 0644)

	jsonData, err = json.MarshalIndent(searchTopics, "", "  ")
	if err != nil {
		errLog.Printf("(%s) Ошибка маршалинга json для тем поиска: %v", loc, err)
		return
	}
	_ = os.WriteFile(topicsSearchFile, jsonData, 0644)
	infoLog.Printf("(%s) Файлы описания успешно созданы", loc)
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

func createChatFile(issue int) {
	const loc = "createChatFile"
	infoLog.Printf("(%s) Сбор данных чата для выпуска %d", loc, issue)

	issueStr = fmt.Sprintf("%d", issue)

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

	chatParsed := false

	c.OnHTML("table.table", func(table *colly.HTMLElement) {
		chatParsed = true
		chat := &Chat{Chat: []ChatLine{}}

		table.ForEach("tr", func(_ int, tr *colly.HTMLElement) {
			chatLine := ChatLine{
				Id:             primitive.NewObjectID(),
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

	if _, err := os.Stat(ccSrcFile); err != nil {
		warnLog.Printf("(%s) Файл субтитров %s отсутствует. Создаем файлы с пустым массивом", loc, ccSrcFile)
		hasError = true
		return
	}

	data, err := os.ReadFile(ccSrcFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка чтения файла субтитров %s: %v", loc, ccSrcFile, err)
		hasError = true
		return
	}
	_ = os.WriteFile(ccSsaFile, data, 0644)

	ssa, err := as.OpenFile(ccSrcFile)
	if err != nil {
		errLog.Printf("(%s) Ошибка парсинга ssa-файла %s: %v", loc, ccSrcFile, err)
		hasError = true
		return
	}

	subs := &Subs{Subs: []CCLine{}}

	for _, item := range ssa.Items {
		var author string
		if len(item.Lines) > 0 {
			author = item.Lines[0].VoiceName
		}
		ccLine := &CCLine{
			Id:     primitive.NewObjectID(),
			Issue:  issue,
			Type:   "cc",
			Author: author,
			Stime:  item.StartAt.Seconds(),
			Etime:  item.EndAt.Seconds(),
			Text:   fmt.Sprintf("%s", item),
		}

		subs.Subs = append(subs.Subs, *ccLine)
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
	}{
		{
			filePath:  topicsSearchFile,
			indexName: "topics",
		},
		{
			filePath:  chatSearchFile,
			indexName: "chat_msgs",
		},
		{
			filePath:  ccSearchFile,
			indexName: "cc_msgs",
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

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Использование: go run main.go <номер_выпуска>")
		os.Exit(1)
	}

	issueNumber, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Printf("Неверный формат номера выпуска: %v\n", err)
		os.Exit(1)
	}

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
