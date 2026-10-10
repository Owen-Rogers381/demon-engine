package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

const telegraphAPIBase = "https://api.telegra.ph"
const telegraphShortName = "GoIndieHackBot"

func getTelegraphAccessToken(authorName string) (string, error) {
	if token := os.Getenv("TELEGRAPH_ACCESS_TOKEN"); token != "" {
		log.Printf("[TELEGRAPH] Используется токен из переменной окружения\n")
		return token, nil
	}

	requestBody := map[string]string{
		"short_name":  telegraphShortName,
		"author_name": authorName,
	}

	log.Printf("[TELEGRAPH] Запрашиваю токен для автора: %s\n", authorName)

	var resp struct {
		Ok     bool `json:"ok"`
		Result struct {
			AccessToken string `json:"access_token"`
		} `json:"result"`
		Error string `json:"error"`
	}

	if err := postJSON(telegraphAPIBase+"/createAccount", requestBody, &resp); err != nil {
		log.Printf("[TELEGRAPH] ❌ Ошибка HTTP при createAccount: %v\n", err)
		return "", err
	}
	if !resp.Ok {
		log.Printf("[TELEGRAPH] ❌ API ошибка createAccount: %s\n", resp.Error)
		return "", fmt.Errorf("telegraph createAccount failed: %s", resp.Error)
	}

	log.Printf("[TELEGRAPH] ✅ Токен успешно получен: %s\n", resp.Result.AccessToken[:20]+"...")
	return resp.Result.AccessToken, nil
}

func CreateTelegraphPage(title, authorName, htmlContent string) (string, error) {
	log.Printf("[TELEGRAPH] === НАЧАЛО СОЗДАНИЯ СТРАНИЦЫ ===")
	log.Printf("[TELEGRAPH] Заголовок: %s\n", title)
	log.Printf("[TELEGRAPH] Автор: %s\n", authorName)
	log.Printf("[TELEGRAPH] Длина HTML контента: %d символов\n", len(htmlContent))

	accessToken, err := getTelegraphAccessToken(authorName)
	if err != nil {
		log.Printf("[TELEGRAPH] ❌ Не удалось получить токен: %v\n", err)
		return "", err
	}

	cleanHTML := CleanHTMLForTelegraph(htmlContent)
	if strings.TrimSpace(cleanHTML) == "" {
		log.Printf("[TELEGRAPH] ❌ Ошибка: Контент пуст после очистки!\n")
		return "", fmt.Errorf("telegraph content empty after cleaning")
	}
	log.Printf("[TELEGRAPH] Длина HTML после очистки: %d символов\n", len(cleanHTML))

	log.Printf("[TELEGRAPH] Конвертирую HTML в формат Telegra.ph Nodes...\n")
	content, err := htmlToTelegraphNodes(cleanHTML)
	if err != nil {
		log.Printf("[TELEGRAPH] ❌ Ошибка при парсинге HTML: %v\n", err)
		return "", err
	}
	log.Printf("[TELEGRAPH] ✅ Получено %d узлов\n", len(content))

	requestBody := map[string]interface{}{
		"access_token": accessToken,
		"title":        title,
		"author_name":  authorName,
		"content":      content,
	}

	log.Printf("[TELEGRAPH] Отправляю POST /createPage\n")
	var resp struct {
		Ok     bool `json:"ok"`
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
		Error string `json:"error"`
	}

	if err := postJSON(telegraphAPIBase+"/createPage", requestBody, &resp); err != nil {
		log.Printf("[TELEGRAPH] ❌ Ошибка HTTP при createPage: %v\n", err)
		return "", err
	}
	if !resp.Ok {
		log.Printf("[TELEGRAPH] ❌ API ошибка createPage: %s\n", resp.Error)
		return "", fmt.Errorf("telegraph createPage failed: %s", resp.Error)
	}

	log.Printf("[TELEGRAPH] ✅ Страница успешно создана: %s\n", resp.Result.URL)
	log.Printf("[TELEGRAPH] === КОНЕЦ СОЗДАНИЯ СТРАНИЦЫ ===")
	return resp.Result.URL, nil
}

// preservePreCodeBlocks временно выносит <pre>/<code> из HTML, чтобы не ломать переносы строк при очистке.
func preservePreCodeBlocks(html string) (string, map[string]string) {
	placeholders := make(map[string]string)
	idx := 0
	re := regexp.MustCompile(`(?is)<(pre|code)\b[^>]*>.*?</(pre|code)>`)
	protected := re.ReplaceAllStringFunc(html, func(block string) string {
		ph := fmt.Sprintf("\x00BLOCK%d\x00", idx)
		placeholders[ph] = block
		idx++
		return ph
	})
	return protected, placeholders
}

func restorePreCodeBlocks(html string, placeholders map[string]string) string {
	for ph, block := range placeholders {
		html = strings.ReplaceAll(html, ph, block)
	}
	return html
}

// collapseHorizontalSpaces схлопывает только пробелы и табы, переносы строк \n сохраняет.
func collapseHorizontalSpaces(html string) string {
	return regexp.MustCompile(`[ \t]+`).ReplaceAllString(html, " ")
}

// stripInlineTagSmart удаляет inline-тег, добавляя пробел только если слова слипаются.
func stripInlineTagSmart(html, tag string) string {
	reOpenGlue := regexp.MustCompile(`(?i)([^\s])<` + tag + `\b[^>]*>`)
	html = reOpenGlue.ReplaceAllString(html, `$1 `)
	html = regexp.MustCompile(`(?i)<`+tag+`\b[^>]*>`).ReplaceAllString(html, "")

	reCloseGlue := regexp.MustCompile(`(?i)</` + tag + `>([^\s])`)
	html = reCloseGlue.ReplaceAllString(html, ` $1`)
	html = regexp.MustCompile(`(?i)</`+tag+`>`).ReplaceAllString(html, "")
	return html
}

func CleanHTMLForTelegraph(htmlContent string) string {
	// Удаляем iframe полностью
	iframeRe := regexp.MustCompile(`(?is)<iframe\b[^>]*>.*?</iframe>`)
	cleaned := iframeRe.ReplaceAllString(htmlContent, "")

	// Сохраняем пробелы вокруг inline-тегов, чтобы текст не склеивался при последующей очистке
	cleaned = preserveInlineTagSpacing(cleaned)

	// Заменяем <div> на <p>, сохраняя абзацы
	cleaned = regexp.MustCompile(`(?i)<div\b[^>]*>`).ReplaceAllString(cleaned, "<p>")
	cleaned = regexp.MustCompile(`(?i)</div>`).ReplaceAllString(cleaned, "</p>")

	// Заголовки h1-h6 → жирный абзац для Telegra.ph
	cleaned = regexp.MustCompile(`(?i)<h[1-6]\b[^>]*>`).ReplaceAllString(cleaned, "<p><b>")
	cleaned = regexp.MustCompile(`(?i)</h[1-6]>`).ReplaceAllString(cleaned, "</b></p>")

	// Неподдерживаемые inline-теги: пробел только если слова слипнутся
	cleaned = stripInlineTagSmart(cleaned, "span")
	cleaned = stripInlineTagSmart(cleaned, "font")

	// Удаляем атрибуты class, style, id — структурные теги (<p>, <ul>, <li> и т.д.) сохраняются
	cleaned = regexp.MustCompile(`(?i)[ \t]+(class|style|id)[ \t]*=[ \t]*("[^"]*"|'[^']*'|[^\s>]+)`).ReplaceAllString(cleaned, "")

	commentRe := regexp.MustCompile(`(?s)<!--.*?-->`)
	cleaned = commentRe.ReplaceAllString(cleaned, "")

	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")

	// Защищаем <pre>/<code> от схлопывания пробелов и склейки между тегами
	protected, blocks := preservePreCodeBlocks(cleaned)

	// Между тегами схлопываем только горизонтальные пробелы, \n сохраняем
	protected = regexp.MustCompile(`>[ \t]+<`).ReplaceAllString(protected, "><")

	// Множественные пробелы/табы — в один, переносы строк не трогаем
	protected = collapseHorizontalSpaces(protected)

	protected = regexp.MustCompile(`\n{3,}`).ReplaceAllString(protected, "\n\n")

	// Пустые абзацы убираем, но валидные <p>...</p> оставляем
	protected = regexp.MustCompile(`(?i)<p>[ \t]*</p>`).ReplaceAllString(protected, "")

	cleaned = restorePreCodeBlocks(protected, blocks)

	return strings.TrimSpace(cleaned)
}

func htmlToTelegraphNodes(htmlContent string) ([]interface{}, error) {
	log.Printf("[TELEGRAPH-PARSER] Парсирую HTML фрагмент, размер: %d байт\n", len(htmlContent))
	fragment, err := html.ParseFragment(strings.NewReader(htmlContent), nil)
	if err != nil {
		log.Printf("[TELEGRAPH-PARSER] ❌ Ошибка парсинга HTML: %v\n", err)
		return nil, err
	}

	log.Printf("[TELEGRAPH-PARSER] Получено %d узлов из парсера\n", len(fragment))
	var nodes []interface{}
	for i, n := range fragment {
		log.Printf("[TELEGRAPH-PARSER] Обрабатываю узел %d: тип=%d, data=%s\n", i, n.Type, n.Data)
		converted := telegraphNodeToJSON(n)
		if converted == nil {
			log.Printf("[TELEGRAPH-PARSER]   ⚠️ Узел пропущен (nil результат)\n")
			continue
		}

		switch v := converted.(type) {
		case []interface{}:
			for _, child := range v {
				nodes = append(nodes, child)
			}
		case string:
			nodes = append(nodes, map[string]interface{}{"tag": "p", "children": []interface{}{v}})
		default:
			nodes = append(nodes, v)
		}
		log.Printf("[TELEGRAPH-PARSER]   ✅ Узел успешно преобразован\n")
	}
	log.Printf("[TELEGRAPH-PARSER] Итого преобразовано %d узлов\n", len(nodes))
	return nodes, nil
}

func telegraphNodeToJSON(n *html.Node) interface{} {
	switch n.Type {
	case html.TextNode:
		text := n.Data
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		log.Printf("[TELEGRAPH-PARSER]   TextNode: %q (длина: %d)\n", text[:min(len(text), 50)], len(text))
		return text
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		allowedTags := map[string]bool{
			"p": true, "b": true, "strong": true, "i": true,
			"em": true, "a": true, "code": true, "pre": true,
			"u": true, "s": true, "blockquote": true, "ul": true,
			"ol": true, "li": true, "br": true, "hr": true, "img": true,
		}
		if !allowedTags[tag] {
			log.Printf("[TELEGRAPH-PARSER]   ElementNode <%s> - тег не разрешен, рекурсим в дети\n", tag)
			var children []interface{}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if child := telegraphNodeToJSON(c); child != nil {
					children = append(children, child)
				}
			}
			if len(children) == 0 {
				return nil
			}
			if len(children) == 1 {
				return children[0]
			}
			return children
		}

		log.Printf("[TELEGRAPH-PARSER]   ElementNode <%s> - разрешенный тег\n", tag)
		node := map[string]interface{}{"tag": tag}
		if len(n.Attr) > 0 {
			attrs := make(map[string]string)
			for _, a := range n.Attr {
				// Для тегов img нам нужен src
				if tag == "img" && a.Key == "src" {
					attrs[a.Key] = a.Val
				} else if a.Key == "href" {
					// Для тегов a нам нужен href
					attrs[a.Key] = a.Val
				}
			}
			if len(attrs) > 0 {
				node["attrs"] = attrs
			}
		}

		var children []interface{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if child := telegraphNodeToJSON(c); child != nil {
				children = append(children, child)
			}
		}
		if len(children) > 0 {
			node["children"] = children
		}
		return node
	default:
		return nil
	}
}

func postJSON(url string, body interface{}, result interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		log.Printf("[TELEGRAPH-HTTP] ❌ Ошибка при marshal JSON: %v\n", err)
		return err
	}

	log.Printf("[TELEGRAPH-HTTP] POST %s\n", url)
	log.Printf("[TELEGRAPH-HTTP] Размер payload: %d байт\n", len(payload))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("[TELEGRAPH-HTTP] ❌ Ошибка создания запроса: %v\n", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[TELEGRAPH-HTTP] ❌ Ошибка при выполнении HTTP запроса: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	log.Printf("[TELEGRAPH-HTTP] HTTP статус: %d\n", resp.StatusCode)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[TELEGRAPH-HTTP] ❌ Ошибка чтения ответа: %v\n", err)
		return err
	}

	log.Printf("[TELEGRAPH-HTTP] Ответ (%d байт): %s\n", len(respBody), string(respBody))

	if err := json.Unmarshal(respBody, result); err != nil {
		log.Printf("[TELEGRAPH-HTTP] ❌ Ошибка парсинга JSON ответа: %v\n", err)
		return fmt.Errorf("failed to decode telegraph response: %w, body=%s", err, string(respBody))
	}

	return nil
}
