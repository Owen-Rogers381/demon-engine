package main

import (
	"fmt"
	"log"
)

func testTelegraph() {
	// Тестируем парсинг HTML и создание Telegra.ph страницы
	testHTML := `<p>Это первый абзац с текстом.</p>
<p>Это второй абзац <b>с жирным текстом</b> и <a href="https://example.com">ссылкой</a>.</p>
<p>Третий абзац для проверки парсера.</p>`

	log.Println("\n=== ТЕСТ ПАРСЕРА HTML ===")
	nodes, err := htmlToTelegraphNodes(testHTML)
	if err != nil {
		fmt.Printf("❌ Ошибка парсинга: %v\n", err)
		return
	}

	fmt.Printf("✅ Успешно спарсено! Получено %d узлов\n", len(nodes))
	
	// Теперь пробуем создать страницу
	log.Println("\n=== ТЕСТ СОЗДАНИЯ СТРАНИЦЫ ===")
	pageURL, err := CreateTelegraphPage("Тестовая страница", "Инди-хакер", testHTML)
	if err != nil {
		fmt.Printf("❌ Ошибка создания страницы: %v\n", err)
		return
	}
	fmt.Printf("✅ Страница создана: %s\n", pageURL)
}
