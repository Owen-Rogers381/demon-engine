package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
)

const (
	telegramToken        = "YOUR_TELEGRAM_BOT_TOKEN"
	channelID            = "-"
	groupID              = "-"
	openRouterAPIBaseURL = "https://api.vsegpt.ru/v1"
	openRouterKey        = "YOUR_OPENROUTER_KEY"
	vseGPTKey            = "YOUR_VSEGPT_API_KEY"
	adminID              = 1171928665
	modelID              = "gpt-3.5-turbo"
	cacheFile            = "cache.txt"
	logFile              = "bot.log"
	watermarkFile        = "watermark.png"
	channelLink          = "https://t.me/go_indie_hack"
	subscriptionChannel  = "@go_indie_hack"
	pdfFilePath          = "data/Go_AI_Dev_Pack.pdf"
	minImageSizeBytes    = 15 * 1024
)

var postScheduleUTC = []int{7, 12, 17}

var moscowLocation = mustLoadLocation("Europe/Moscow")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}

func init() {
	time.Local = moscowLocation
	log.SetFlags(log.Ltime)
}

func nowMSK() time.Time {
	return time.Now().In(moscowLocation)
}

func formatLogTime(t time.Time) string {
	return t.Format("2006/01/02 15:04:05")
}

// lastDiscussionMsgID — message_id автопоста канала в группе обсуждения
// (обновляется listener-ом при каждом новом посте)
var (
	lastDiscussionMsgID int
	lastDiscussionMsgMu sync.Mutex
	newUserAlertMu      sync.Mutex
	newUserAlerted      = make(map[int]bool)
)

type SlotConfig struct {
	HourMSK int
	Tag     string
	Focus   string
	Vibe    string
}

var slots = []SlotConfig{
	{
		HourMSK: 10,
		Tag:     "#кейс",
		Focus:   "важные инфоповоды, глобальные новости IT, интересные истории из мира разработки",
		Vibe:    "аналитический, вдумчивый, с инсайтами",
	},
	{
		HourMSK: 15,
		Tag:     "#инструменты или #обучение",
		Focus:   "библиотеки, фреймворки, гайды, хардкорный код, практические инструменты для Go/Backend",
		Vibe:    "практичный, технический, с кодом и командами",
	},
	{
		HourMSK: 20,
		Tag:     "#мысли или #фан",
		Focus:   "вдохновляющий контент, сторителлинг, инди-хакинг, философия разработки",
		Vibe:    "расслабленный, вдохновляющий, провокационный",
	},
}

var habrSources = []string{
	"https://habr.com/ru/rss/hub/go/all/?fl=ru",
	"https://habr.com/ru/rss/hub/devops/all/?fl=ru",
	"https://habr.com/ru/rss/hub/artificial_intelligence/all/?fl=ru",
	"https://habr.com/ru/rss/hub/career/all/?fl=ru",
	"https://habr.com/ru/rss/hub/open_source/all/?fl=ru",
}

const hnTopURL = "https://hacker-news.firebaseio.com/v0/topstories.json"
const hnItemURL = "https://hacker-news.firebaseio.com/v0/item/%d.json"

type RSS struct {
	XMLName xml.Name   `xml:"rss"`
	Channel RSSChannel `xml:"channel"`
}
type RSSChannel struct {
	Items []Item `xml:"item"`
}
type Item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Score       int
	IsEnglish   bool
}
type HNItem struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Score int    `json:"score"`
	Text  string `json:"text"`
}
type OpenRouterRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type OpenRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}
type AIPostResponse struct {
	Title           string
	PostText        string
	ShortPreview    string
	FullHTMLContent string
	ImagePrompt     string
	Hashtag         string
}

const longPostThreshold = 1000

type TGUpdateResponse struct {
	Ok     bool `json:"ok"`
	Result []struct {
		UpdateID int `json:"update_id"`
		Message  *struct {
			MessageID int    `json:"message_id"`
			Text      string `json:"text"`
			From      *struct {
				ID        int64  `json:"id"`
				FirstName string `json:"first_name"`
				LastName  string `json:"last_name"`
				Username  string `json:"username"`
			} `json:"from,omitempty"`
			Chat struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
			SenderChat *struct {
				ID int64 `json:"id"`
			} `json:"sender_chat,omitempty"`
		} `json:"message,omitempty"`
		CallbackQuery *struct {
			ID   string `json:"id"`
			Data string `json:"data"`
			From struct {
				ID int `json:"id"`
			} `json:"from"`
			Message *struct {
				MessageID int `json:"message_id"`
				Chat      struct {
					ID   int64  `json:"id"`
					Type string `json:"type"`
				} `json:"chat"`
			} `json:"message,omitempty"`
		} `json:"callback_query,omitempty"`
	} `json:"result"`
}
type TGSendResponse struct {
	Ok     bool `json:"ok"`
	Result struct {
		MessageID int `json:"message_id"`
	} `json:"result"`
}
type HabrArticle struct {
	Statistics struct {
		Score         int `json:"score"`
		ReadingCount  int `json:"readingCount"`
		CommentsCount int `json:"commentsCount"`
	} `json:"statistics"`
}

// ---- САНИТАЙЗЕР ----

var (
	reBr       = regexp.MustCompile(`(?i)<br\s*/?>`)
	reLi       = regexp.MustCompile(`(?i)<li>([\s\S]*?)</li>`)
	reList     = regexp.MustCompile(`(?i)</?[uo]l>`)
	reOrphanLi = regexp.MustCompile(`(?i)</?li>`)
	reP        = regexp.MustCompile(`(?i)<p>([\s\S]*?)</p>`)
	reH        = regexp.MustCompile(`(?i)<h[1-6]>([\s\S]*?)</h[1-6]>`)
	reBold     = regexp.MustCompile(`\*\*([\s\S]*?)\*\*`)
	reItalic   = regexp.MustCompile(`\*([\s\S]*?)\*`)
	reBadTags  = regexp.MustCompile(`(?i)</?(div|span|ul|ol|li|p|h[1-6]|br|table|tr|td|blockquote)(\s[^>]*)?>`)
	reAnyTag   = regexp.MustCompile(`(?i)<(/?)([a-z-]+)(\s[^>]*)?>`)
	reMultiNL  = regexp.MustCompile(`\n{3,}`)
	reFixedEnt = regexp.MustCompile(`&amp;(#\d+|#x[0-9a-fA-F]+);`)
)

var allowedTelegramTags = map[string]bool{
	"b": true, "i": true, "u": true, "s": true,
	"code": true, "pre": true, "a": true, "tg-spoiler": true,
}

var reInlineTagOpen = regexp.MustCompile(`(?i)([^\s<])(<(b|i|code|tg-spoiler)\b[^>]*>)`)
var reInlineTagClose = regexp.MustCompile(`(?i)(</(b|i|code|tg-spoiler)>)([^\s<])`)

func preserveInlineTagSpacing(input string) string {
	if strings.TrimSpace(input) == "" {
		return input
	}
	for i := 0; i < 2; i++ {
		input = reInlineTagOpen.ReplaceAllString(input, `$1 $2`)
		input = reInlineTagClose.ReplaceAllString(input, `$1 $3`)
	}
	return input
}

// ensureClosedHTMLTags — гарантирует закрытие всех открытых тегов
// Это предотвращает ошибку: "can't parse entities: Can't find end tag corresponding to start tag"
func ensureClosedHTMLTags(html string) string {
	openTags := make([]string, 0)
	selfClosingTags := map[string]bool{"br": true, "hr": true, "img": true}

	// Находим все теги
	tagRe := regexp.MustCompile(`(?i)<(/?)([a-z-]+)(\s[^>]*)?>`)
	result := tagRe.ReplaceAllStringFunc(html, func(tag string) string {
		m := tagRe.FindStringSubmatch(tag)
		if len(m) < 3 {
			return tag
		}
		isClosing := m[1] == "/"
		tagName := strings.ToLower(m[2])

		// Пропускаем самозакрывающиеся теги
		if selfClosingTags[tagName] {
			return tag
		}

		if !isClosing {
			// Открывающий тег
			openTags = append(openTags, tagName)
			return tag
		} else {
			// Закрывающий тег
			if len(openTags) > 0 && openTags[len(openTags)-1] == tagName {
				openTags = openTags[:len(openTags)-1]
			}
			return tag
		}
	})

	// Закрываем все оставшиеся открытые теги в обратном порядке
	for i := len(openTags) - 1; i >= 0; i-- {
		result += fmt.Sprintf("</%s>", openTags[i])
	}

	return result
}

func escapeBareAmpersands(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "&amp;amp;", "&amp;")
	s = strings.ReplaceAll(s, "&amp;lt;", "&lt;")
	s = strings.ReplaceAll(s, "&amp;gt;", "&gt;")
	s = strings.ReplaceAll(s, "&amp;quot;", "&quot;")
	s = reFixedEnt.ReplaceAllString(s, "&$1;")
	return s
}

func stripDisallowedTags(s string) string {
	return reAnyTag.ReplaceAllStringFunc(s, func(tag string) string {
		m := reAnyTag.FindStringSubmatch(tag)
		if len(m) < 3 {
			return ""
		}
		if allowedTelegramTags[strings.ToLower(m[2])] {
			return tag
		}
		return ""
	})
}

// SanitizeHTML — конвертирует всё что Telegram не поддерживает в безопасный формат
func SanitizeHTML(input string) string {
	// 1) Fix literal escaped newlines: convert "\\n" sequences into real newlines
	result := strings.ReplaceAll(input, "\\n", "\n")

	// existing cleanup: convert simple tags to plaintext-friendly forms
	result = preserveInlineTagSpacing(result)
	result = reBr.ReplaceAllString(result, "\n")

	for i := 0; i < 3; i++ {
		result = reLi.ReplaceAllStringFunc(result, func(s string) string {
			inner := reLi.FindStringSubmatch(s)
			if len(inner) > 1 {
				return "• " + strings.TrimSpace(inner[1]) + "\n"
			}
			return s
		})
	}

	result = reList.ReplaceAllString(result, "\n")
	result = reOrphanLi.ReplaceAllString(result, "")

	result = reP.ReplaceAllStringFunc(result, func(s string) string {
		inner := reP.FindStringSubmatch(s)
		if len(inner) > 1 {
			return strings.TrimSpace(inner[1]) + "\n\n"
		}
		return s
	})

	result = reH.ReplaceAllStringFunc(result, func(s string) string {
		inner := reH.FindStringSubmatch(s)
		if len(inner) > 1 {
			return "<b>" + strings.TrimSpace(inner[1]) + "</b>\n"
		}
		return s
	})

	result = reBold.ReplaceAllString(result, "<b>$1</b>")
	result = reItalic.ReplaceAllString(result, "$1")
	result = strings.ReplaceAll(result, "*", "")

	// span/font: пробел только если слова слипнутся (до reBadTags)
	result = stripInlineTagSmart(result, "span")
	result = stripInlineTagSmart(result, "font")

	// remove explicitly blacklisted tags and keep only allowed ones
	result = reBadTags.ReplaceAllString(result, "")
	result = stripDisallowedTags(result)

	// Protect allowed tags (<a>, <b>, <i>, <code>, <pre>, <tg-spoiler>) by replacing them with placeholders
	allowedRe := regexp.MustCompile(`(?i)</?(a|b|i|code|pre|tg-spoiler)(\s[^>]*)?>`)
	placeholders := make(map[string]string)
	idx := 0
	result = allowedRe.ReplaceAllStringFunc(result, func(tag string) string {
		ph := fmt.Sprintf("___ALWTAG_%d___", idx)
		placeholders[ph] = tag
		idx++
		return ph
	})

	// Escape remaining raw < and > (these are unsafe for Telegram HTML parser)
	result = strings.ReplaceAll(result, "<", "&lt;")
	result = strings.ReplaceAll(result, ">", "&gt;")

	// Restore allowed tags from placeholders (they remain unescaped)
	for ph, tag := range placeholders {
		result = strings.ReplaceAll(result, ph, tag)
	}

	// Remove any leftover placeholder tokens that may have come from malformed AI output
	result = regexp.MustCompile(`<<ALWTAG_[0-9]+>>|___ALWTAG_[0-9]+___`).ReplaceAllString(result, "")

	// Escape ampersands and normalize entities, collapse many newlines
	result = escapeBareAmpersands(result)
	result = reMultiNL.ReplaceAllString(result, "\n\n")

	// Гарантируем закрытие всех открытых тегов перед отправкой в Telegram
	result = ensureClosedHTMLTags(result)

	return strings.TrimSpace(result)
}

// StripHTMLForPlain — убирает все теги для fallback без parse_mode
func StripHTMLForPlain(input string) string {
	reTags := regexp.MustCompile(`<[^>]*>`)
	text := reTags.ReplaceAllString(input, "")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	reMultiNewline := regexp.MustCompile(`\n{3,}`)
	text = reMultiNewline.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// ---- ОСНОВНОЙ ПОТОК ----

func main() {
	f, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	}
	log.SetFlags(log.Ltime)
	startMsg := fmt.Sprintf("[%s] --- БОТ-ДЕМОН ВЕРСИЯ №11.0: РАЗДЕЛЬНЫЙ КОНТЕНТ TELEGRA.PH (MSK TIMEZONE) ---",
		formatLogTime(nowMSK()))
	log.Println(startMsg)

	deleteWebhook()
	go startAdminListener()
	sendDirectMessage(adminID, "🟢 Демон v11.0 на связи. Команды: /logs /force")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("💥 panic в checkAndPost при старте: %v\n", r)
			}
		}()
		checkAndPost(false)
	}()

	for {
		now := nowMSK().UTC()
		nextHour, nextDur := nextScheduledTime(now)
		log.Printf("⏰ Следующая публикация в %02d:00 МСК, через %v\n",
			nextHour+3, nextDur.Round(time.Minute))
		time.Sleep(nextDur)
		checkAndPost(false)
	}
}

func nextScheduledTime(now time.Time) (hour int, dur time.Duration) {
	for _, h := range postScheduleUTC {
		candidate := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, time.UTC)
		if candidate.After(now) {
			return h, candidate.Sub(now)
		}
	}
	tomorrow := now.Add(24 * time.Hour)
	first := postScheduleUTC[0]
	candidate := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), first, 0, 0, 0, time.UTC)
	return first, candidate.Sub(now)
}

func currentSlot() SlotConfig {
	hourMSK := nowMSK().Hour()
	if hourMSK < 13 {
		return slots[0]
	}
	if hourMSK < 18 {
		return slots[1]
	}
	return slots[2]
}

func status(forceMode bool, msg string) {
	timestamp := formatLogTime(nowMSK())
	fullMsg := fmt.Sprintf("[%s] %s", timestamp, msg)
	if forceMode {
		sendDirectMessage(adminID, fullMsg)
	}
	log.Println(fullMsg)
}

// ---- OG:IMAGE ----

func isBlacklistedImageURL(imgURL string) bool {
	urlLower := strings.ToLower(imgURL)
	blacklist := []string{
		"habr.com/share.png",
		"placeholder",
		"default",
		"no-image",
		"ruvds",
	}
	for _, token := range blacklist {
		if strings.Contains(urlLower, token) {
			return true
		}
	}
	return false
}

func fetchOGImage(pageURL string) []byte {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(pageURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil
	}

	imgURL := findOGImage(doc)
	if imgURL == "" {
		log.Println("🖼️ og:image мета-тег не найден")
		return nil
	}
	log.Printf("🖼️ Найден og:image: %s\n", imgURL)

	if isBlacklistedImageURL(imgURL) {
		log.Printf("🗑️ og:image URL в чёрном списке: %s. Пропускаю.\n", imgURL)
		return nil
	}

	headResp, err := client.Head(imgURL)
	if err == nil && headResp.ContentLength > 0 {
		if headResp.ContentLength < minImageSizeBytes {
			log.Printf("🗑️ Картинка весит %d КБ — мусор (< %d КБ). Запускаю генерацию шедевра через ИИ.\n",
				headResp.ContentLength/1024, minImageSizeBytes/1024)
			return nil
		}
		log.Printf("✅ Вес по HEAD: %d КБ — норм, скачиваем.\n", headResp.ContentLength/1024)
	}

	imgResp, err := client.Get(imgURL)
	if err != nil {
		return nil
	}
	defer imgResp.Body.Close()
	data, _ := io.ReadAll(imgResp.Body)

	if len(data) < minImageSizeBytes {
		log.Printf("🗑️ Скачанная картинка весит %d КБ — мусор. Запускаю генерацию шедевра через ИИ.\n",
			len(data)/1024)
		return nil
	}

	converted := convertToJPEG(data)
	if converted == nil {
		log.Println("⚠️ Не удалось конвертировать og:image в JPEG")
		return nil
	}
	log.Printf("✅ og:image готов! Итоговый вес: %d КБ\n", len(converted)/1024)
	return converted
}

func findOGImage(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "meta" {
		var prop, content string
		for _, a := range n.Attr {
			if a.Key == "property" {
				prop = a.Val
			}
			if a.Key == "content" {
				content = a.Val
			}
		}
		if prop == "og:image" && content != "" {
			return content
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findOGImage(c); result != "" {
			return result
		}
	}
	return ""
}

func convertToJPEG(data []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := img.Bounds()
	bg := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			bg.Set(x, y, img.At(x, y))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, bg, &jpeg.Options{Quality: 90}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// ---- ИСТОЧНИКИ ----

func extractHabrID(link string) string {
	parts := strings.Split(strings.TrimRight(link, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if _, err := strconv.Atoi(parts[i]); err == nil {
			return parts[i]
		}
	}
	return ""
}

func getHabrScore(link string) int {
	id := extractHabrID(link)
	if id == "" {
		return 0
	}
	apiURL := fmt.Sprintf("https://habr.com/kek/v2/articles/%s/?fl=ru&hl=ru", id)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var article HabrArticle
	if err := json.Unmarshal(body, &article); err != nil {
		return 0
	}
	return article.Statistics.Score + article.Statistics.ReadingCount/100 + article.Statistics.CommentsCount*2
}

func fetchHabrItems(cache map[string]bool) []Item {
	var all []Item
	for _, rssURL := range habrSources {
		resp, err := http.Get(rssURL)
		if err != nil {
			log.Printf("❌ Ошибка загрузки RSS %s: %v\n", rssURL, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var rss RSS
		if err := xml.Unmarshal(body, &rss); err != nil {
			log.Printf("❌ Ошибка парсинга XML: %v\n", err)
			continue
		}
		for _, item := range rss.Channel.Items {
			if !cache[item.Link] {
				all = append(all, item)
			}
		}
	}
	return all
}

func fetchHNItems(cache map[string]bool) []Item {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(hnTopURL)
	if err != nil {
		log.Printf("❌ Ошибка загрузки HN топа: %v\n", err)
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var ids []int
	json.Unmarshal(body, &ids)

	var items []Item
	limit := 15
	for i := 0; i < len(ids) && len(items) < limit; i++ {
		hnURL := fmt.Sprintf(hnItemURL, ids[i])
		r, err := client.Get(hnURL)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var hn HNItem
		json.Unmarshal(b, &hn)
		if hn.URL == "" || cache[hn.URL] {
			continue
		}
		desc := hn.Text
		if desc == "" {
			desc = hn.Title
		}
		items = append(items, Item{
			Title:       hn.Title,
			Link:        hn.URL,
			Description: desc,
			Score:       hn.Score,
			IsEnglish:   true,
		})
	}
	return items
}

func loadCache() map[string]bool {
	cache := make(map[string]bool)
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return cache
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			cache[line] = true
		}
	}
	return cache
}

func saveToCache(link string) {
	f, err := os.OpenFile(cacheFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(link + "\n")
}

func warmupCacheOnEmpty(cache map[string]bool, items []Item) bool {
	if len(cache) > 0 || len(items) == 0 {
		return false
	}

	log.Println("🔥 Пустой cache.txt — прогреваем текущую ленту. Сохраню текущие ссылки и не отправляю посты.")
	for _, item := range items {
		if !cache[item.Link] {
			saveToCache(item.Link)
			cache[item.Link] = true
		}
	}
	return true
}

func scoreAllParallel(items []Item) []Item {
	var wg sync.WaitGroup
	var mu sync.Mutex
	scored := make([]Item, len(items))
	copy(scored, items)
	for i := range scored {
		if scored[i].IsEnglish {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s := getHabrScore(scored[idx].Link)
			mu.Lock()
			scored[idx].Score = s
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return scored
}

// ---- ВОТЕРМАРК ----

func resizeNearestNeighbor(src image.Image, targetWidth, targetHeight int) image.Image {
	if targetWidth <= 0 || targetHeight <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}

	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == 0 || srcH == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		srcY := y * srcH / targetHeight
		if srcY >= srcH {
			srcY = srcH - 1
		}
		for x := 0; x < targetWidth; x++ {
			srcX := x * srcW / targetWidth
			if srcX >= srcW {
				srcX = srcW - 1
			}
			dst.Set(x, y, src.At(srcBounds.Min.X+srcX, srcBounds.Min.Y+srcY))
		}
	}
	return dst
}

func applyWatermark(imgBytes []byte) []byte {
	wmData, err := os.ReadFile(watermarkFile)
	if err != nil {
		log.Println("⚠️ watermark.png не найден, пропускаем вотермарк")
		return imgBytes
	}

	baseImg, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return imgBytes
	}
	wmImg, err := png.Decode(bytes.NewReader(wmData))
	if err != nil {
		return imgBytes
	}

	baseBounds := baseImg.Bounds()
	baseW := baseBounds.Dx()
	baseH := baseBounds.Dy()
	if baseW == 0 || baseH == 0 {
		return imgBytes
	}

	margin := 20
	targetW := int(float64(baseW) * 0.15)
	if targetW <= 0 {
		return imgBytes
	}
	maxW := baseW - margin*2
	if targetW > maxW {
		targetW = maxW
	}

	wmBounds := wmImg.Bounds()
	wmW := wmBounds.Dx()
	wmH := wmBounds.Dy()
	if wmW == 0 || wmH == 0 {
		return imgBytes
	}

	targetH := int(float64(targetW) * float64(wmH) / float64(wmW))
	if targetH <= 0 {
		return imgBytes
	}

	if targetH > baseH-margin*2 {
		targetH = baseH - margin*2
		if targetH <= 0 {
			return imgBytes
		}
		targetW = int(float64(targetH) * float64(wmW) / float64(wmH))
	}

	resizedWM := resizeNearestNeighbor(wmImg, targetW, targetH)
	x := baseBounds.Min.X + baseW - targetW - margin
	y := baseBounds.Min.Y + baseH - targetH - margin

	result := image.NewRGBA(baseBounds)
	draw.Draw(result, baseBounds, baseImg, baseBounds.Min, draw.Src)
	draw.Draw(result, image.Rect(x, y, x+targetW, y+targetH), resizedWM, image.Point{}, draw.Over)

	var buf bytes.Buffer
	jpeg.Encode(&buf, result, &jpeg.Options{Quality: 90})
	return buf.Bytes()
}

// ---- JSON CLEANER ----

func cleanJSON(data []byte) []byte {
	s := strings.TrimSpace(string(data))
	startIdx := strings.Index(s, "{")
	endIdx := strings.LastIndex(s, "}")
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return data
	}
	return []byte(s[startIdx : endIdx+1])
}

// ---- ИИ-АДВОКАТ ----

func GenerateAdvocateComment(postTitle, postText string) string {
	systemPrompt := `Ты — провокационный IT-адвокат канала "Дневник инди-хакера".
Напиши ОДИН короткий комментарий (1-2 предложения, 50-150 символов).
Задай острый вопрос или поделись неожиданным мнением — вовлеки в дискуссию.
Звучи как живой человек. Максимум 2 эмодзи. Без хэштегов. Без ссылок.
Отвечай ТОЛЬКО текстом комментария.`

	userContent := fmt.Sprintf("Пост: %s\n\n%s", postTitle, postText[:min(300, len(postText))])
	reqBody := OpenRouterRequest{
		Model:     modelID,
		Messages:  []Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userContent}},
		Stream:    false,
		MaxTokens: 1000,
	}
	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", openRouterAPIBaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+vseGPTKey)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("⚠️ ИИ-Адвокат: ошибка: %v\n", err)
		return ""
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("🔍 Ответ OpenRouter (первые 300 символов): %s\n", string(respBody[:min(300, len(respBody))]))

	cleanedBody := cleanJSON(respBody)
	var orResp OpenRouterResponse
	if err := json.Unmarshal(cleanedBody, &orResp); err != nil {
		log.Printf("⚠️ ИИ-Адвокат: невалидный JSON после очистки: %v\n", err)
		return ""
	}
	if len(orResp.Choices) == 0 {
		return ""
	}
	return SanitizeHTML(strings.TrimSpace(orResp.Choices[0].Message.Content))
}

func SendAdvocateComment(channelMsgID int, postTitle, postText string) {
	go func() {
		log.Printf("💬 ИИ-Адвокат: жду 90 сек перед комментарием к посту канала #%d...\n", channelMsgID)
		time.Sleep(90 * time.Second)
		comment := GenerateAdvocateComment(postTitle, postText)
		if comment == "" {
			log.Println("⚠️ ИИ-Адвокат: не удалось сгенерировать комментарий")
			return
		}
		lastDiscussionMsgMu.Lock()
		discussionMsgID := lastDiscussionMsgID
		lastDiscussionMsgMu.Unlock()

		apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
		payload := map[string]interface{}{
			"chat_id":    groupID,
			"text":       comment,
			"parse_mode": "HTML",
		}
		if discussionMsgID > 0 {
			payload["reply_parameters"] = map[string]interface{}{
				"message_id": discussionMsgID,
			}
			log.Printf("💬 ИИ-Адвокат: reply на discussion_msg_id=%d → группа %s\n", discussionMsgID, groupID)
		} else {
			log.Printf("💬 ИИ-Адвокат: discussion_msg_id не найден, шлю без привязки к треду\n")
		}
		jsonData, _ := json.Marshal(payload)
		resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("⚠️ ИИ-Адвокат: ошибка отправки: %v\n", err)
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		if !bytes.Contains(respBody, []byte(`"ok":true`)) {
			log.Printf("❌ ИИ-Адвокат: Telegram отклонил: %s\n", string(respBody[:min(400, len(respBody))]))
			return
		}
		log.Printf("✅ ИИ-Адвокат: комментарий опубликован (discussion_msg_id=%d)\n", discussionMsgID)
	}()
}

// ---- ОСНОВНАЯ ЛОГИКА ----

func checkAndPost(forceMode bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("💥 panic в checkAndPost: %v\n", r)
			if forceMode {
				sendDirectMessage(adminID, fmt.Sprintf("❌ Публикация упала: %v", r))
			}
		}
	}()

	slot := currentSlot()
	status(forceMode, fmt.Sprintf("🎯 Слот: %02d:00 МСК | Фокус: %s | Тег: %s",
		slot.HourMSK, slot.Focus, slot.Tag))

	status(forceMode, "🔄 Собираем статьи со всех источников...")
	cache := loadCache()
	habrItems := fetchHabrItems(cache)
	hnItems := fetchHNItems(cache)
	items := append(habrItems, hnItems...)

	if len(items) == 0 {
		status(forceMode, "😴 Нет новых статей ни в одном источнике.")
		return
	}

	if warmupCacheOnEmpty(cache, items) {
		status(forceMode, "🔥 Кэш прогрет. Первый набор постов помечен как прочитанный, отправки пока нет.")
		return
	}

	status(forceMode, fmt.Sprintf("📊 Найдено %d статей (Хабр: %d, HN: %d) — оцениваем рейтинг...",
		len(items), len(habrItems), len(hnItems)))
	items = scoreAllParallel(items)

	for _, item := range items {
		src := "🇷🇺"
		if item.IsEnglish {
			src = "🌐"
		}
		log.Printf("   %s %s — score: %d | originalURL: %s\n", src, item.Title, item.Score, item.Link)
	}

	best := items[0]
	for _, item := range items[1:] {
		if item.Score > best.Score {
			best = item
		}
	}

	src := "Хабр"
	if best.IsEnglish {
		src = "Hacker News 🌐"
	}
	status(forceMode, fmt.Sprintf("🏆 Победитель [%s]: \"%s\" (score: %d)", src, best.Title, best.Score))

	originalURL := best.Link
	log.Printf("🔗 originalURL: %s\n", originalURL)

	isForeign := isForeignArticle(best)
	sourceLong := plainTextLen(best.Description) > longPostThreshold
	useTelegraph := isForeign || sourceLong

	log.Printf("📋 Режим публикации: foreign=%v, sourceLong=%v → telegraph=%v\n", isForeign, sourceLong, useTelegraph)

	status(forceMode, "🧠 Генерирую пост... (~30-60 сек)")
	postResp, err := askLlamaWithRetry(best.Title, best.Description, originalURL, best.IsEnglish, slot, useTelegraph)
	if err != nil {
		status(forceMode, fmt.Sprintf("❌ Ошибка ИИ после всех попыток: %v", err))
		return
	}

	cleanTitle := cleanContentPrefixes(SanitizeHTML(postResp.Title))
	imagePrompt := postResp.ImagePrompt
	hashtag := normalizeHashtag(postResp.Hashtag, slot)

	var telegraphURL string
	var advocateText string
	var postText string
	var shortPreview string

	if useTelegraph {
		shortPreview = cleanContentPrefixes(stripHashtagsFromHTML(SanitizeHTML(postResp.ShortPreview)))
		fullHTML := cleanContentPrefixes(stripTelegraphCTAs(stripHashtagsFromHTML(postResp.FullHTMLContent)))

		log.Printf("🔗 originalURL (публикация): %s\n", originalURL)
		log.Printf("📝 Заголовок: %s\n", cleanTitle)
		log.Printf("📏 short_preview: %d симв | full_html: %d симв | telegraph=true\n",
			plainTextLen(shortPreview), plainTextLen(fullHTML))
		log.Printf("🎨 Промпт картинки: %s\n", imagePrompt)

		status(forceMode, "📄 Создаю страницу на Telegra.ph (полная статья)...")
		preparedHTML := PrepareHTMLForTelegraph(fullHTML)
		pageURL, err := CreateTelegraphPage(cleanTitle, "Дневник инди-хакера", preparedHTML)
		if err != nil {
			log.Printf("⚠️ Не удалось создать Telegra.ph страницу: %v\n", err)
		} else {
			telegraphURL = pageURL
			log.Printf("✅ Telegra.ph: %s | full_html: %d симв | originalURL: %s\n",
				telegraphURL, plainTextLen(fullHTML), originalURL)
		}
		advocateText = shortPreview
	} else {
		postText = cleanContentPrefixes(stripHashtagsFromHTML(SanitizeHTML(postResp.PostText)))

		log.Printf("🔗 originalURL (публикация): %s\n", originalURL)
		log.Printf("📝 Заголовок: %s\n", cleanTitle)
		log.Printf("📏 Длина post_text: %d символов | telegraph=false\n", plainTextLen(postText))
		log.Printf("🎨 Промпт картинки: %s\n", imagePrompt)
		advocateText = postText
	}

	var imageBytes []byte
	status(forceMode, "🖼️ Ищем og:image в статье...")
	log.Printf("🔗 originalURL (og:image): %s\n", originalURL)
	imageBytes = fetchOGImage(originalURL)

	if len(imageBytes) == 0 {
		status(forceMode, fmt.Sprintf("🎨 Генерирую уникальный арт: \"%s\"", imagePrompt))
		func() {
			encodedPrompt := url.QueryEscape(imagePrompt)
			imgURL := fmt.Sprintf("https://image.pollinations.ai/p/%s?width=1024&height=576&nologo=true", encodedPrompt)
			imgResp, err := http.Get(imgURL)
			if err != nil {
				return
			}
			defer imgResp.Body.Close()
			raw, _ := io.ReadAll(imgResp.Body)
			imageBytes = convertToJPEG(raw)
		}()
	}

	if len(imageBytes) > 0 {
		imageBytes = applyWatermark(imageBytes)
	}

	var channelMsgID int

	if useTelegraph {
		// Telegra.ph: строго ОДИН пост в канал (картинка + анонс)
		caption := buildTelegraphAnnouncement(cleanTitle, shortPreview, telegraphURL, hashtag)
		status(forceMode, "📸 Публикую единственный пост (Telegra.ph)...")
		log.Printf("🔗 originalURL (отправка): %s\n", originalURL)
		if len(imageBytes) > 0 {
			channelMsgID, err = sendPhotoToTelegram(imageBytes, caption)
		} else {
			channelMsgID, err = sendToTelegram(caption)
		}
		if err != nil {
			log.Printf("⚠️ Ошибка публикации: %v | originalURL: %s\n", err, originalURL)
		}
	} else {
		// Прямой режим: двойной пост (обложка + текст)
		firstCaption := cleanTitle
		status(forceMode, "📸 Публикую обложку (сообщение 1/2)...")
		if len(imageBytes) > 0 {
			channelMsgID, err = sendPhotoToTelegram(imageBytes, firstCaption)
		} else {
			channelMsgID, err = sendToTelegram(firstCaption)
		}
		if err != nil {
			log.Printf("⚠️ Ошибка первого сообщения: %v | originalURL: %s\n", err, originalURL)
		}

		secondMessage := buildSecondMessageDirect(postText, originalURL, hashtag)
		status(forceMode, "📝 Публикую текст поста (сообщение 2/2)...")
		log.Printf("🔗 originalURL (отправка): %s\n", originalURL)
		if _, err := sendToTelegram(secondMessage); err != nil {
			log.Printf("⚠️ Ошибка второго сообщения: %v | originalURL: %s\n", err, originalURL)
		}
	}

	if channelMsgID > 0 {
		SendAdvocateComment(channelMsgID, cleanTitle, advocateText)
	}

	saveToCache(originalURL)
}

func isForeignArticle(item Item) bool {
	if item.IsEnglish {
		return true
	}
	return !isRussianTechSource(item.Link)
}

func normalizeHashtag(hashtag string, slot SlotConfig) string {
	hashtag = strings.TrimSpace(hashtag)
	validTags := map[string]bool{
		"#обучение": true, "#кейс": true, "#мысли": true,
		"#инструменты": true, "#фан": true,
	}
	if validTags[hashtag] {
		return hashtag
	}
	tagParts := strings.Fields(slot.Tag)
	if len(tagParts) > 0 {
		return tagParts[0]
	}
	return "#мысли"
}

var (
	reHashtag      = regexp.MustCompile(`(?i)#\p{L}[\p{L}\d_]*`)
	rePrefixTag    = regexp.MustCompile(`(?im)^\s*(▸\s*)?(Тег|тег)\s*[:#]?\s*[^\n]*\n*`)
	reLabelPrefix  = regexp.MustCompile(`(?i)(МИКРО-ГАЙД|ИНСАЙТ)\s*[:—–-]?\s*`)
	reArrowStart   = regexp.MustCompile(`(?m)^\s*▸\s*`)
	reTelegraphCTA = regexp.MustCompile(`(?i)(Хочешь зарыться в исходники\?[^<\n]*|Полный гайд[^<\n]*|Вся подноготная[^<\n]*|по ссылке ниже[^<\n]*|подписывайся[^<\n]*|Подписаться[^<\n]*)`)
)

func stripHashtagsFromHTML(s string) string {
	s = reHashtag.ReplaceAllString(s, "")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func cleanContentPrefixes(s string) string {
	s = rePrefixTag.ReplaceAllString(s, "")
	s = reLabelPrefix.ReplaceAllString(s, "")
	s = reArrowStart.ReplaceAllString(s, "")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func stripTelegraphCTAs(s string) string {
	s = reTelegraphCTA.ReplaceAllString(s, "")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func buildTelegraphAnnouncement(title, preview, telegraphURL, hashtag string) string {
	title = cleanContentPrefixes(strings.TrimSpace(title))
	preview = cleanContentPrefixes(strings.TrimSpace(preview))

	msg := "🚀🔥💻 " + title
	if preview != "" {
		msg += "\n\n" + preview
	}
	if telegraphURL != "" {
		msg += fmt.Sprintf("\n\n🔗 <a href=\"%s\">Читать полностью на Telegra.ph (Instant View)</a>", telegraphURL)
	}
	msg += fmt.Sprintf("\n\n📢 <a href=\"%s\">Подписаться на Дневник инди-хакера</a>", channelLink)
	msg += "\n\n" + hashtag
	return msg
}

func buildSecondMessageDirect(postText, originalURL, hashtag string) string {
	msg := strings.TrimSpace(postText)
	msg += fmt.Sprintf("\n\n%s", buildArticleLinkBlock(originalURL))
	msg += fmt.Sprintf("\n<a href=\"%s\">Подписаться на Дневник инди-хакера</a>", channelLink)
	msg += fmt.Sprintf("\n%s", formatHashtagLink(hashtag))
	return msg
}

// ---- ИИ ----

func askLlamaWithRetry(title, description, link string, isEnglish bool, slot SlotConfig, telegraphMode bool) (*AIPostResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		result, err := askLlama(title, description, link, isEnglish, slot, telegraphMode)
		if err == nil {
			return result, nil
		}
		lastErr = err
		log.Printf("⚠️ Попытка %d/3 не удалась: %v. Ждём 10 секунд...\n", attempt, err)
		if attempt < 3 {
			time.Sleep(10 * time.Second)
		}
	}
	return nil, lastErr
}

func isRussianTechSource(articleURL string) bool {
	u, err := url.Parse(articleURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	return host == "habr.com" || strings.HasSuffix(host, ".habr.com") ||
		host == "vc.ru" || strings.HasSuffix(host, ".vc.ru")
}

func buildArticleLinkBlock(articleURL string) string {
	return fmt.Sprintf("<a href=\"%s\">Погрузиться в детали</a>", articleURL)
}

func formatHashtagLink(hashtag string) string {
	return fmt.Sprintf("<a href=\"%s\">%s</a>", channelLink, hashtag)
}

func plainTextLen(html string) int {
	return len([]rune(StripHTMLForPlain(html)))
}

func PrepareHTMLForTelegraph(fullHTML string) string {
	if strings.Contains(strings.ToLower(fullHTML), "<p>") {
		return collapseHorizontalSpacesSafe(fullHTML)
	}
	parts := regexp.MustCompile(`\n\n+`).Split(strings.TrimSpace(fullHTML), -1)
	var paragraphs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, fmt.Sprintf("<p>%s</p>", p))
		}
	}
	if len(paragraphs) == 0 {
		return collapseHorizontalSpacesSafe(fmt.Sprintf("<p>%s</p>", strings.TrimSpace(fullHTML)))
	}
	return collapseHorizontalSpacesSafe(strings.Join(paragraphs, "\n"))
}

// collapseHorizontalSpacesSafe схлопывает [ \t]+, сохраняя \n и содержимое <pre>/<code>.
func collapseHorizontalSpacesSafe(html string) string {
	protected, blocks := preservePreCodeBlocks(html)
	protected = collapseHorizontalSpaces(protected)
	return restorePreCodeBlocks(protected, blocks)
}

func askLlama(title, description, link string, isEnglish bool, slot SlotConfig, telegraphMode bool) (*AIPostResponse, error) {
	log.Printf("🔗 originalURL (ИИ): %s | telegraphMode=%v\n", link, telegraphMode)

	var systemPrompt string
	if telegraphMode {
		systemPrompt = fmt.Sprintf(`Ты — Демон, автономный ИИ-продюсер русскоязычного IT-канала "Дневник инди-хакера". Аудитория — Go/Backend-разработчики и инди-хакеры.

═══ ТЕКУЩИЙ СЛОТ ═══
Время: %02d:00 МСК | Фокус: %s | Вайб: %s | Тег слота: %s

═══ КРИТИЧЕСКИ ВАЖНО: ФОРМАТ ОТВЕТА ═══
Ты должен вернуть ТОЛЬКО валидный JSON без каких-либо пояснений до или после.
Никакого текста вне JSON. Никаких markdown-блоков. Только чистый JSON.

Структура JSON:
{
  "title": "ЗАГОЛОВОК КАПСОМ С ЭМОДЗИ (до 80 символов)",
  "short_preview": "Короткий сочный тизер для Telegram: 2-3 предложения, с эмодзи, без ссылок и хэштегов",
  "full_html_content": "Полноценная подробная статья на русском для Telegra.ph с разметкой <p>, <code>, <pre>, <b>",
  "hashtag": "ОДИН хэштег из списка: #обучение #кейс #мысли #инструменты #фан",
  "image_prompt": "10-15 слов описание картинки на английском (ТОЛЬКО визуальное описание, NO hashtags, NO links, NO URLs)"
}

═══ ПРАВИЛА ДЛЯ short_preview ═══
• Ровно 2-3 предложения — сочный тизер, интригующий
• ЖЁСТКОЕ ПРАВИЛО: если статья или пост относится к категориям #инструменты или #обучение, ты обязан в самом начале текста выделить 1 ключевой технический термин (главную технологию, библиотеку или концепт) и дать ему максимально простое, понятное и краткое определение "на пальцах" (как для новичка), и только после этого переходить к деталям статьи.
• БЕЗ хэштегов, БЕЗ ссылок, БЕЗ призывов перейти по ссылке
• Начинай сразу с сути, без ярлыков и префиксов
• Допустимы теги: <b>, <code>, <tg-spoiler>
• ЗАПРЕЩЕНЫ: <p>, <br>, <ul>, <li>, звёздочки *, markdown

═══ ПРАВИЛА ДЛЯ full_html_content ═══
• Полноценная подробная статья на русском для Telegra.ph
• Стиль: серьёзный, но понятный «хакерский» язык профессионального бэкендера
• Пиши как опытный разработчик: «под капотом», «архитектура», «инференс», «кэширование», «латентность», «throughput»
• ЗАПРЕЩЁН детский сленг и панибратство — никаких «поморочился», «братья по коду», «в продакшене ради фана»
• Разбивай текст на логические абзацы <p> — каждый абзац = одна мысль или блок рассуждения
• Код: короткие фрагменты и команды через <code>, блоки кода и конфиги через <pre> — чётко отделяй от прозы
• Эмодзи (умеренно): ставь в начале абзацев и пунктов списков для визуальной структуры — 💻 🚀 ⚡ 🛡️ 📦 ⚙️ (1 эмодзи на абзац/пункт, не перегружай)
• Переводи/адаптируй ВСЕ ключевые детали, примеры кода, выводы
• БЕЗ хэштегов, БЕЗ ссылок, БЕЗ призывов подписаться
• ЗАПРЕЩЕНЫ призывы: "Полный гайд — по ссылке ниже", "подписывайся", "по ссылке ниже", "Погрузиться в детали"
• Начинай сразу с сути, без ярлыков "Тег:", "МИКРО-ГАЙД", стрелочек
• Допустимы: <p>, <b>, <i>, <code>, <pre>, <blockquote>, <ul>, <ol>, <li>

═══ ПРАВИЛА ДЛЯ image_prompt ═══
• Только визуальное описание на английском (10-15 слов)
• Стиль: flat illustration, minimalist geometric design, pastel colors, abstract tech concept
• ЗАПРЕЩЕНО: люди, лица, руки, тела, хэштеги, ссылки, URL`,
			slot.HourMSK, slot.Focus, slot.Vibe, slot.Tag)
	} else {
		systemPrompt = fmt.Sprintf(`Ты — Демон, автономный ИИ-продюсер русскоязычного IT-канала "Дневник инди-хакера". Аудитория — Go/Backend-разработчики и инди-хакеры.

═══ ТЕКУЩИЙ СЛОТ ═══
Время: %02d:00 МСК | Фокус: %s | Вайб: %s | Тег слота: %s

═══ КРИТИЧЕСКИ ВАЖНО: ФОРМАТ ОТВЕТА ═══
Ты должен вернуть ТОЛЬКО валидный JSON без каких-либо пояснений до или после.
Никакого текста вне JSON. Никаких markdown-блоков. Только чистый JSON.

Структура JSON:
{
  "title": "ЗАГОЛОВОК КАПСОМ С ЭМОДЗИ (до 80 символов)",
  "post_text": "Основной текст поста в HTML (без хэштегов, без ссылок)",
  "hashtag": "ОДИН хэштег из списка: #обучение #кейс #мысли #инструменты #фан",
  "image_prompt": "10-15 слов описание картинки на английском (ТОЛЬКО визуальное описание, NO hashtags, NO links, NO URLs)"
}

═══ ПРАВИЛА ДЛЯ post_text ═══
• ТОЛЬКО чистый HTML-текст поста
• ЖЁСТКОЕ ПРАВИЛО: если статья или пост относится к категориям #инструменты или #обучение, ты обязан в самом начале текста выделить 1 ключевой технический термин (главную технологию, библиотеку или концепт) и дать ему максимально простое, понятное и краткое определение "на пальцах" (как для новичка), и только после этого переходить к деталям статьи.
• БЕЗ хэштегов — они будут добавлены автоматически
• БЕЗ ссылок — они будут добавлены автоматически
• Начинай сразу с сути, без ярлыков "Тег:", "МИКРО-ГАЙД", стрелочек
• Вступление (1-2 предл.) → суть (3-5 буллетов •) → вывод

═══ ЖЕЛЕЗОБЕТОННЫЕ ПРАВИЛА HTML-РЕЖИМА ═══
ЗАПРЕЩЕНО: символ *, markdown, хэштеги, ссылки, <br>, <ul>, <ol>, <li>, <p>, <h1-h6>, <div>, <span>
ОБЯЗАТЕЛЬНО: русский язык, эмодзи в каждом абзаце, <b>, <code>, <tg-spoiler>, максимум 800-1000 символов
• Айтишный сленг: "поморочился", "братья по коду", "в продакшене"
• Для списков только: • пункт

═══ ПРАВИЛА ДЛЯ image_prompt ═══
• Только визуальное описание на английском (10-15 слов)
• Стиль: flat illustration, minimalist geometric design, pastel colors, abstract tech concept
• ЗАПРЕЩЕНО: люди, лица, руки, тела, хэштеги, ссылки, URL`,
			slot.HourMSK, slot.Focus, slot.Vibe, slot.Tag)
	}

	var userContent string
	if telegraphMode && isEnglish {
		userContent = fmt.Sprintf("ВАЖНО: Сделай качественный адаптированный перевод статьи с английского на русский. short_preview — короткий тизер для Telegram (2-3 предл.). full_html_content — ПОЛНАЯ подробная статья для Telegra.ph со всеми деталями, кодом и разметкой <p>/<pre>/<code>.\n\nЗаголовок: %s\n\nТекст статьи: %s", title, description)
	} else if telegraphMode {
		userContent = fmt.Sprintf("ВАЖНО: short_preview — короткий тизер для Telegram (2-3 предл.). full_html_content — ПОЛНАЯ подробная переработка статьи для Telegra.ph со всеми деталями, кодом и разметкой <p>/<pre>/<code>.\n\nОригинальный заголовок: %s\n\nОригинальный текст: %s", title, description)
	} else if isEnglish {
		userContent = fmt.Sprintf("ВАЖНО: Переведи статью с английского на русский в хакерском стиле для Go/Backend-разработчиков.\n\nЗаголовок: %s\n\nОписание: %s", title, description)
	} else {
		userContent = fmt.Sprintf("ВАЖНО: Переработай русскую статью в хакерском стиле канала 'Дневник инди-хакера'.\n\nОригинальный заголовок: %s\n\nОригинальный анонс: %s", title, description)
	}

	reqBody := OpenRouterRequest{
		Model:     modelID,
		Messages:  []Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userContent}},
		Stream:    false,
		MaxTokens: 1000,
	}
	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", openRouterAPIBaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+vseGPTKey)

	timeout := 60 * time.Second
	if telegraphMode {
		timeout = 120 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("🔍 Ответ OpenRouter (первые 300 символов): %s\n", string(respBody[:min(300, len(respBody))]))

	cleanedBody := cleanJSON(respBody)
	var orResp OpenRouterResponse
	if err := json.Unmarshal(cleanedBody, &orResp); err != nil {
		log.Printf("⚠️ Ошибка парсинга JSON от OpenRouter после очистки: %v\nОчищенный ответ: %s\n", err, string(cleanedBody[:min(300, len(cleanedBody))]))
		return nil, err
	}
	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("пустой ответ от OpenRouter. Тело: %s", string(respBody[:min(300, len(respBody))]))
	}

	rawContent := strings.TrimSpace(orResp.Choices[0].Message.Content)
	rawContent = strings.TrimPrefix(rawContent, "```json")
	rawContent = strings.TrimPrefix(rawContent, "```")
	rawContent = strings.TrimSuffix(rawContent, "```")
	rawContent = strings.TrimSpace(rawContent)

	if telegraphMode {
		var aiJSON struct {
			Title           string `json:"title"`
			ShortPreview    string `json:"short_preview"`
			FullHTMLContent string `json:"full_html_content"`
			Hashtag         string `json:"hashtag"`
			ImagePrompt     string `json:"image_prompt"`
		}
		if err := json.Unmarshal([]byte(rawContent), &aiJSON); err != nil {
			log.Printf("⚠️ Невалидный JSON от ИИ: %v\nСырой ответ: %s\n", err, rawContent)
			return nil, fmt.Errorf("невалидный JSON от ИИ: %v", err)
		}
		hashtag := normalizeHashtag(aiJSON.Hashtag, slot)
		return &AIPostResponse{
			Title:           aiJSON.Title,
			ShortPreview:    aiJSON.ShortPreview,
			FullHTMLContent: aiJSON.FullHTMLContent,
			ImagePrompt:     aiJSON.ImagePrompt,
			Hashtag:         hashtag,
		}, nil
	}

	var aiJSON struct {
		Title       string `json:"title"`
		PostText    string `json:"post_text"`
		Hashtag     string `json:"hashtag"`
		ImagePrompt string `json:"image_prompt"`
	}
	if err := json.Unmarshal([]byte(rawContent), &aiJSON); err != nil {
		log.Printf("⚠️ Невалидный JSON от ИИ: %v\nСырой ответ: %s\n", err, rawContent)
		return nil, fmt.Errorf("невалидный JSON от ИИ: %v", err)
	}
	hashtag := normalizeHashtag(aiJSON.Hashtag, slot)

	return &AIPostResponse{
		Title:       aiJSON.Title,
		PostText:    aiJSON.PostText,
		ImagePrompt: aiJSON.ImagePrompt,
		Hashtag:     hashtag,
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---- TELEGRAM ----

func deleteWebhook() {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook?drop_pending_updates=false", telegramToken)
	resp, err := http.Get(apiURL)
	if err != nil {
		log.Printf("⚠️ deleteWebhook: %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("🔗 deleteWebhook: %s\n", string(body[:min(200, len(body))]))
}

func parseTelegramCommand(text string) (command, args string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "", ""
	}
	command = strings.TrimPrefix(parts[0], "/")
	if idx := strings.Index(command, "@"); idx != -1 {
		command = command[:idx]
	}
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}
	return strings.ToLower(command), strings.TrimSpace(args)
}

func shouldHandleSubscriptionPrompt(command, args, text string) bool {
	if command == "prompts" {
		return true
	}
	if command == "start" && strings.EqualFold(args, "gopdf") {
		return true
	}
	return strings.Contains(strings.ToLower(text), "промпт")
}

func isSubscriptionActive(status string) bool {
	switch strings.ToLower(status) {
	case "member", "administrator", "creator":
		return true
	default:
		return false
	}
}

func checkChannelSubscription(userID int) (string, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getChatMember?chat_id=%s&user_id=%d",
		telegramToken, url.QueryEscape(subscriptionChannel), userID)
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var chatMemberResp struct {
		Ok     bool `json:"ok"`
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
		Error string `json:"description"`
	}
	if err := json.Unmarshal(body, &chatMemberResp); err != nil {
		return "", err
	}
	if !chatMemberResp.Ok {
		return "", fmt.Errorf("getChatMember failed: %s", chatMemberResp.Error)
	}
	return chatMemberResp.Result.Status, nil
}

func sendTextMessage(chatID int64, text string, replyMarkup map[string]interface{}) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
	data := map[string]interface{}{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if replyMarkup != nil {
		data["reply_markup"] = replyMarkup
	}
	jsonData, _ := json.Marshal(data)
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func sendPDFToTelegram(chatID int64) error {
	if _, err := os.Stat(pdfFilePath); err != nil {
		return fmt.Errorf("pdf file not found: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", telegramToken)
	body := &bytes.Buffer{}
	boundary := "xxxxPDFBOUNDARYxxxx"
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"chat_id\"\r\n\r\n%d\r\n", boundary, chatID)
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"caption\"\r\n\r\n%s\r\n", boundary, "Твой Dev-Пак успешно разблокирован! 🚀 Внутри — чистая выжимка промптов и Docker-команд. Прокачивай свой код, инди-хакер!")
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"parse_mode\"\r\n\r\nHTML\r\n", boundary)
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"document\"; filename=\"%s\"\r\nContent-Type: application/pdf\r\n\r\n", boundary, filepath.Base(pdfFilePath))
	fileBytes, err := os.ReadFile(pdfFilePath)
	if err != nil {
		return err
	}
	body.Write(fileBytes)
	fmt.Fprintf(body, "\r\n--%s--\r\n", boundary)

	req, err := http.NewRequest("POST", apiURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func answerCallbackQuery(callbackQueryID, text string, showAlert bool) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", telegramToken)
	payload := map[string]interface{}{"callback_query_id": callbackQueryID}
	if text != "" {
		payload["text"] = text
	}
	if showAlert {
		payload["show_alert"] = true
	}
	jsonData, _ := json.Marshal(payload)
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func deleteMessage(chatID int64, messageID int) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage?chat_id=%d&message_id=%d", telegramToken, chatID, messageID)
	resp, err := http.Get(apiURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func buildNewUserAdminAlert(firstName, lastName, username string, userID int) string {
	nameParts := []string{}
	if strings.TrimSpace(firstName) != "" {
		nameParts = append(nameParts, strings.TrimSpace(firstName))
	}
	if strings.TrimSpace(lastName) != "" {
		nameParts = append(nameParts, strings.TrimSpace(lastName))
	}
	displayName := strings.TrimSpace(strings.Join(nameParts, " "))
	if displayName == "" {
		displayName = "неизвестно"
	}
	if strings.TrimSpace(username) == "" {
		username = "нет username"
	} else {
		username = "@" + strings.TrimSpace(username)
	}
	return fmt.Sprintf("🔔 Новый пользователь в боте!\nИмя: %s\nUsername: %s\nID: %d", displayName, username, userID)
}

func maybeNotifyAdminAboutNewUser(userID int, firstName, lastName, username string) {
	if userID == adminID {
		return
	}
	newUserAlertMu.Lock()
	alreadyNotified := newUserAlerted[userID]
	if !alreadyNotified {
		newUserAlerted[userID] = true
	}
	newUserAlertMu.Unlock()
	if alreadyNotified {
		return
	}
	sendDirectMessage(adminID, buildNewUserAdminAlert(firstName, lastName, username, userID))
}

func handleSubscriptionPrompt(chatID int64, userID int) {
	maybeNotifyAdminAboutNewUser(userID, "", "", "")

	status, err := checkChannelSubscription(userID)
	if err != nil || !isSubscriptionActive(status) {
		text := "Васап, бро! Чтобы скачать наш секретный неоновый мануал по Go, Docker и AI-промптам, тебе нужно подписаться на наш канал \"Дневник инди-хакера\". Это топливо для Демона! 🦾"
		replyMarkup := map[string]interface{}{
			"inline_keyboard": [][]map[string]string{
				{{"text": "Открыть канал", "url": channelLink}},
				{{"text": "Проверить подписку", "callback_data": "check_subscription"}},
			},
		}
		_ = sendTextMessage(chatID, text, replyMarkup)
		return
	}
	if err := sendPDFToTelegram(chatID); err != nil {
		_ = sendTextMessage(chatID, "⚠️ Не удалось отправить PDF. Проверь, что файл лежит по пути data/Go_AI_Dev_Pack.pdf", nil)
	} else if userID != adminID {
		sendDirectMessage(adminID, fmt.Sprintf("📥 Мануал разблокирован!\nИмя: %s\nUsername: %s\nID: %d успешно скачал Go_AI_Dev_Pack.pdf! 🎉", "неизвестно", "нет username", userID))
	}
}

func sendPhotoToTelegram(photoBytes []byte, caption string) (int, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", telegramToken)
	body := &bytes.Buffer{}
	boundary := "xxxxHACKERBOUNDARYxxxx"
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"chat_id\"\r\n\r\n%s\r\n", boundary, channelID)
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"parse_mode\"\r\n\r\nHTML\r\n", boundary)
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"caption\"\r\n\r\n%s\r\n", boundary, caption)
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"photo\"; filename=\"cover.jpg\"\r\nContent-Type: image/jpeg\r\n\r\n", boundary)
	body.Write(photoBytes)
	fmt.Fprintf(body, "\r\n--%s--\r\n", boundary)

	req, err := http.NewRequest("POST", apiURL, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	client := &http.Client{Timeout: 40 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("код ответа ТГ: %d, инфо: %s", resp.StatusCode, string(respBody))
	}
	var tgResp TGSendResponse
	json.Unmarshal(respBody, &tgResp)
	return tgResp.Result.MessageID, nil
}

func sendToTelegram(text string) (int, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
	data := map[string]interface{}{
		"chat_id":                  channelID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	jsonData, _ := json.Marshal(data)
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("📨 Ответ Telegram на sendMessage: %s\n", string(respBody[:min(200, len(respBody))]))

	if bytes.Contains(respBody, []byte(`"ok":true`)) {
		var tgResp TGSendResponse
		json.Unmarshal(respBody, &tgResp)
		return tgResp.Result.MessageID, nil
	}

	// Фоллбек: без parse_mode и без сырых тегов
	log.Printf("⚠️ HTML-парсинг упал: %s\n", string(respBody[:min(400, len(respBody))]))
	plainText := StripHTMLForPlain(text)
	plainData := map[string]interface{}{
		"chat_id":                  channelID,
		"text":                     plainText,
		"disable_web_page_preview": true,
	}
	plainJSON, _ := json.Marshal(plainData)
	resp2, err2 := http.Post(apiURL, "application/json", bytes.NewBuffer(plainJSON))
	if err2 != nil {
		return 0, fmt.Errorf("telegram plain fallback: %w", err2)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	log.Printf("📨 Повторная отправка (plain): %s\n", string(body2[:min(200, len(body2))]))
	if !bytes.Contains(body2, []byte(`"ok":true`)) {
		return 0, fmt.Errorf("telegram send failed: %s", string(respBody[:min(400, len(respBody))]))
	}
	var tgResp TGSendResponse
	json.Unmarshal(body2, &tgResp)
	return tgResp.Result.MessageID, nil
}

func startAdminListener() {
	offset := 0
	log.Println("👂 Admin listener запущен, жду команды в личке боту...")
	for {
		apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=10", telegramToken, offset)
		resp, err := http.Get(apiURL)
		if err != nil {
			log.Printf("⚠️ getUpdates сеть: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			log.Printf("💀 getUpdates 409: уже крутится второй экземпляр бота! pkill -f './main'\n")
			time.Sleep(15 * time.Second)
			continue
		}
		var updates TGUpdateResponse
		if err := json.Unmarshal(body, &updates); err != nil {
			log.Printf("⚠️ getUpdates JSON: %v | body: %s\n", err, string(body[:min(200, len(body))]))
			time.Sleep(5 * time.Second)
			continue
		}
		if !updates.Ok {
			log.Printf("⚠️ getUpdates ok=false: %s\n", string(body[:min(300, len(body))]))
			time.Sleep(5 * time.Second)
			continue
		}
		for _, update := range updates.Result {
			offset = update.UpdateID + 1
			if update.CallbackQuery != nil {
				cb := update.CallbackQuery
				if cb.Data == "check_subscription" && cb.Message != nil {
					status, err := checkChannelSubscription(cb.From.ID)
					if err != nil || !isSubscriptionActive(status) {
						_ = answerCallbackQuery(cb.ID, "❌ ДОСТУП ЗАБЛОКИРОВАН! Демон не видит твоей подписки. Сначала подпишись на канал! 🦾", true)
						continue
					}
					_ = answerCallbackQuery(cb.ID, "", false)
					if err := sendPDFToTelegram(cb.Message.Chat.ID); err == nil {
						if cb.From.ID != adminID {
							sendDirectMessage(adminID, fmt.Sprintf("📥 Мануал разблокирован!\nИмя: %s\nUsername: %s\nID: %d успешно скачал Go_AI_Dev_Pack.pdf! 🎉", "неизвестно", "нет username", cb.From.ID))
						}
						_ = deleteMessage(cb.Message.Chat.ID, cb.Message.MessageID)
					} else {
						_ = sendTextMessage(cb.Message.Chat.ID, "⚠️ Не удалось отправить PDF. Проверь, что файл лежит по пути data/Go_AI_Dev_Pack.pdf", nil)
					}
				}
				continue
			}
			if update.Message == nil {
				continue
			}

			// Ловим автопост канала в группе обсуждения (SenderChat = наш канал)
			msg := update.Message
			if (msg.Chat.Type == "group" || msg.Chat.Type == "supergroup") &&
				msg.SenderChat != nil && fmt.Sprintf("%d", msg.SenderChat.ID) == channelID {
				lastDiscussionMsgMu.Lock()
				lastDiscussionMsgID = msg.MessageID
				lastDiscussionMsgMu.Unlock()
				log.Printf("📌 Зафиксирован автопост канала в группе: discussion_msg_id=%d\n", msg.MessageID)
				continue
			}

			// Остальные сообщения из групп — игнорируем (защита от адвоката)
			chatType := msg.Chat.Type
			if chatType == "group" || chatType == "supergroup" {
				continue
			}
			if msg.Text == "" {
				continue
			}

			userID := int(msg.Chat.ID)
			if chatType == "private" && userID != adminID {
				firstName := ""
				lastName := ""
				username := ""
				if msg.From != nil {
					firstName = msg.From.FirstName
					lastName = msg.From.LastName
					username = msg.From.Username
				}
				maybeNotifyAdminAboutNewUser(userID, firstName, lastName, username)
			}
			text := strings.TrimSpace(msg.Text)

			// --- НАШ ХИРУРГИЧЕСКИЙ, БЕЗОПАСНЫЙ ИНЛАЙН-ПАРСЕР (ВМЕСТО ИИ-ФУНКЦИЙ) ---
			cmd := ""
			args := ""
			if strings.HasPrefix(text, "/") {
				cleanText := strings.TrimPrefix(text, "/")
				parts := strings.Fields(cleanText)
				if len(parts) > 0 {
					cmd = strings.ToLower(parts[0])
					if len(parts) > 1 {
						args = strings.Join(parts[1:], " ")
					}
				}
			}

			lowerText := strings.ToLower(strings.TrimSpace(msg.Text))
			isLeadMagnet := (cmd == "prompts") ||
				(cmd == "promts") ||
				(cmd == "promt") ||
				(cmd == "start" && strings.EqualFold(args, "gopdf")) ||
				(lowerText == "промпт") ||
				(lowerText == "промпты") ||
				(lowerText == "промт") ||
				(lowerText == "prompt") ||
				(lowerText == "prompts") ||
				(lowerText == "promt") ||
				(lowerText == "promts")

			// 1. Сначала проверяем лид-магнит для всех пользователей, включая админа.
			if chatType == "private" && isLeadMagnet {
				handleSubscriptionPrompt(msg.Chat.ID, userID)
				continue
			}

			// 2. Приветствие для обычного старта бота.
			if chatType == "private" && strings.HasPrefix(text, "/start") {
				welcomeText := "Привет, инди-хакер! 🚀 Я — Демон, твой автономный ИИ-продюсер.\n\nЧтобы забрать наш секретный неоновый мануал по Go, Docker и AI-промптам, просто используй меню или введи команду: /prompts 🦾"
				_ = sendTextMessage(msg.Chat.ID, welcomeText, nil)
				continue
			}

			// 3. Админские команды.
			if userID == adminID {
				switch text {
				case "/logs":
					sendDirectMessage(adminID, getLatestLogs(20))
				case "/force":
					sendDirectMessage(adminID, "⚡️ Запускаю публикацию, буду докладывать о каждом шаге...")
					go checkAndPost(true)
				default:
					sendDirectMessage(adminID, "🤖 Команды:\n/logs — Логи\n/force — Вынудить пост")
				}
				continue
			}

			// 4. Защита от чужаков.
			if chatType == "private" {
				_ = sendTextMessage(msg.Chat.ID, "⚠️ [ACCESS DENIED] Обнаружена попытка несанкционированного доступа. Системные логи заблокированы. Зайди позже, когда выучишь Go. 🏎️", nil)
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func getLatestLogs(linesCount int) string {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return "❌ Не удалось прочитать логи."
	}
	lines := strings.Split(string(data), "\n")
	start := len(lines) - linesCount - 1
	if start < 0 {
		start = 0
	}
	return "📋 ЛОГИ СЕРВЕРА:\n\n" + strings.Join(lines[start:], "\n")
}

func sendDirectMessage(chatID int, text string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
	data := map[string]interface{}{"chat_id": chatID, "text": text}
	jsonData, _ := json.Marshal(data)
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
	if err == nil {
		resp.Body.Close()
	}
}
