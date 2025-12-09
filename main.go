package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Структуры для чтения JSON файлов
type ConversationInfo struct {
	OperatorName string `json:"operator_name"`
	OperatorID   string `json:"operator_id"`
	ClientName   string `json:"client_name"`
	ClientID     string `json:"client_id"`
	Date         string `json:"date"`
	Direction    bool   `json:"direction_outgoing"`
}

type Message struct {
	UserID    string `json:"user_id"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

type ConversationChat struct {
	Messages []Message `json:"messages"`
}

// Структура для хранения диалога с его типами
type ProblematicDialog struct {
	FolderName string   // Название папки диалога (например, "AAA-11314")
	ID         string   // ID диалога (из названия файлов)
	Types      []string // Типы проблем
	Files      []string // Файлы в папке
	Triggers   []string // Триггеры, которые были найдены
}

// Регулярные выражения для поиска ID диалога из имен файлов
var convFilePattern = regexp.MustCompile(`conv_([A-Z]+-\d+)_`)

func main() {
	// Инициализируем problemTypes если нужно
	if len(problemTypes) == 0 {
		initializeProblemTypes()
	}

	// Очищаем и сортируем триггеры
	CleanTriggers()

	// Пути
	inputDir := "./output/conversations"
	outputBaseDir := "./problematicDialogs"

	// Создаем выходные директории для каждого типа
	for typeKey := range problemTypes {
		typeDir := filepath.Join(outputBaseDir, typeKey)
		if err := os.MkdirAll(typeDir, 0755); err != nil {
			fmt.Printf("Ошибка создания директории %s: %v\n", typeDir, err)
			return
		}
	}

	// Получаем список папок с диалогами
	folders, err := ioutil.ReadDir(inputDir)
	if err != nil {
		fmt.Printf("Ошибка чтения директории: %v\n", err)
		return
	}

	// Канал для сбора проблемных диалогов
	problematicDialogs := make(chan *ProblematicDialog, 100)
	var wg sync.WaitGroup

	// Обрабатываем каждую папку параллельно
	for _, folder := range folders {
		if !folder.IsDir() {
			continue
		}

		wg.Add(1)
		go func(folderName string) {
			defer wg.Done()

			dialogPath := filepath.Join(inputDir, folderName)
			if dialog := analyzeDialogFolder(dialogPath, folderName); dialog != nil {
				problematicDialogs <- dialog
			}
		}(folder.Name())
	}

	// Ждем завершения всех горутин
	go func() {
		wg.Wait()
		close(problematicDialogs)
	}()

	// Собираем статистику
	stats := make(map[string]int)
	allDialogs := make(map[string][]*ProblematicDialog)

	// Обрабатываем проблемные диалоги
	for dialog := range problematicDialogs {
		// Копируем папку диалога в соответствующие папки типов
		for _, typeKey := range dialog.Types {
			stats[typeKey]++

			// Создаем папку для диалога в директории типа
			typeDir := filepath.Join(outputBaseDir, typeKey, dialog.FolderName)
			if err := os.MkdirAll(typeDir, 0755); err != nil {
				fmt.Printf("Ошибка создания директории для диалога: %v\n", err)
				continue
			}

			// Копируем все файлы из исходной папки
			for _, file := range dialog.Files {
				srcPath := filepath.Join(inputDir, dialog.FolderName, file)
				dstPath := filepath.Join(typeDir, file)

				input, err := ioutil.ReadFile(srcPath)
				if err != nil {
					fmt.Printf("Ошибка чтения файла %s: %v\n", file, err)
					continue
				}

				err = ioutil.WriteFile(dstPath, input, 0644)
				if err != nil {
					fmt.Printf("Ошибка копирования файла %s: %v\n", file, err)
				}
			}

			// Создаем файл с информацией о найденных триггерах
			infoFile := filepath.Join(typeDir, "trigger_info.txt")
			infoContent := fmt.Sprintf("Диалог: %s\nТип: %s\nПапка: %s\nНайденные триггеры:\n",
				dialog.ID, problemTypes[typeKey].Name, dialog.FolderName)
			for _, trigger := range dialog.Triggers {
				infoContent += fmt.Sprintf("- %s\n", trigger)
			}

			if err := ioutil.WriteFile(infoFile, []byte(infoContent), 0644); err != nil {
				fmt.Printf("Ошибка создания файла информации: %v\n", err)
			}

			allDialogs[typeKey] = append(allDialogs[typeKey], dialog)
		}
	}

	// Выводим статистику
	printStatistics(stats, allDialogs)

	// Создаем индексный файл
	createIndexFile(outputBaseDir, stats, allDialogs)
}

// Анализирует папку диалога и возвращает информацию о проблемных типах
func analyzeDialogFolder(folderPath, folderName string) *ProblematicDialog {
	// Получаем список файлов в папке
	files, err := ioutil.ReadDir(folderPath)
	if err != nil {
		return nil
	}

	var fileNames []string
	for _, file := range files {
		fileNames = append(fileNames, file.Name())
	}

	// Ищем файлы диалога
	var dialogID string
	foundTypes := make(map[string]bool)
	var foundTriggers []string

	for _, fileName := range fileNames {
		// Проверяем только chat файлы
		if strings.Contains(fileName, "_chat.json") {
			filePath := filepath.Join(folderPath, fileName)

			// Извлекаем ID диалога из имени файла
			matches := convFilePattern.FindStringSubmatch(fileName)
			if len(matches) > 1 {
				dialogID = matches[1]
			} else {
				// Если не удалось извлечь по шаблону, используем имя папки
				dialogID = folderName
			}

			content, err := ioutil.ReadFile(filePath)
			if err != nil {
				continue
			}

			var chat ConversationChat
			if err := json.Unmarshal(content, &chat); err != nil {
				continue
			}

			// Проверяем все сообщения в диалоге
			for _, msg := range chat.Messages {
				// Проверяем только сообщения от клиентов (user_id с префиксом user_)
				if !strings.HasPrefix(msg.UserID, "user_") {
					continue
				}

				text := strings.ToLower(msg.Text)

				// Проверяем триггеры для каждого типа через предкомпилированные паттерны
				for typeKey, typeInfo := range problemTypes {
					matches := FindPatternMatches(text, typeKey)
					if len(matches) == 0 {
						continue
					}

					foundTypes[typeKey] = true
					// Добавляем сработавшие триггеры, избегая дубликатов
					for _, match := range matches {
						triggerExists := false
						for _, t := range foundTriggers {
							if strings.EqualFold(t, match) {
								triggerExists = true
								break
							}
						}
						if !triggerExists {
							foundTriggers = append(foundTriggers, match)
						}
					}
				}
			}
		}
	}

	// Если найдены типы проблем, возвращаем структуру
	if len(foundTypes) > 0 {
		types := make([]string, 0, len(foundTypes))
		for typeKey := range foundTypes {
			types = append(types, typeKey)
		}

		// Сортируем типы для consistency
		sort.Strings(types)

		return &ProblematicDialog{
			FolderName: folderName,
			ID:         dialogID,
			Types:      types,
			Files:      fileNames,
			Triggers:   foundTriggers,
		}
	}

	return nil
}

// Выводит статистику
func printStatistics(stats map[string]int, allDialogs map[string][]*ProblematicDialog) {
	fmt.Println("=== СТАТИСТИКА ПРОБЛЕМНЫХ ДИАЛОГОВ ===")
	fmt.Println()

	// Сортируем типы по количеству диалогов
	type StatsItem struct {
		TypeKey string
		Count   int
	}

	var statsList []StatsItem
	for typeKey, count := range stats {
		statsList = append(statsList, StatsItem{typeKey, count})
	}

	sort.Slice(statsList, func(i, j int) bool {
		return statsList[i].Count > statsList[j].Count
	})

	total := 0
	for _, item := range statsList {
		typeName := problemTypes[item.TypeKey].Name
		fmt.Printf("%s: %d диалогов\n", typeName, item.Count)
		total += item.Count

		// Выводим первые 3 диалога этого типа
		if dialogs, ok := allDialogs[item.TypeKey]; ok && len(dialogs) > 0 {
			fmt.Printf("  Примеры диалогов: ")
			count := 0
			for _, dialog := range dialogs {
				if count >= 3 {
					break
				}
				fmt.Printf("%s ", dialog.FolderName)
				count++
			}
			fmt.Println()
		}
	}

	fmt.Printf("\nВсего проблемных диалогов: %d\n", total)

	// Выводим наиболее частые триггеры
	fmt.Println("\n=== НАИБОЛЕЕ ЧАСТЫЕ ТРИГГЕРЫ ===")

	// Собираем все триггеры и их частоту
	triggerStats := make(map[string]int)
	for _, dialogs := range allDialogs {
		for _, dialog := range dialogs {
			for _, trigger := range dialog.Triggers {
				triggerStats[trigger]++
			}
		}
	}

	// Сортируем триггеры по частоте
	var triggerList []struct {
		Trigger string
		Count   int
	}

	for trigger, count := range triggerStats {
		triggerList = append(triggerList, struct {
			Trigger string
			Count   int
		}{trigger, count})
	}

	sort.Slice(triggerList, func(i, j int) bool {
		return triggerList[i].Count > triggerList[j].Count
	})

	// Выводим топ-10 триггеров
	fmt.Println("Топ-10 самых частых триггеров:")
	for i := 0; i < 10 && i < len(triggerList); i++ {
		fmt.Printf("%d. %s (%d раз)\n", i+1, triggerList[i].Trigger, triggerList[i].Count)
	}
}

// Создает индексный файл со всей статистикой
func createIndexFile(outputBaseDir string, stats map[string]int, allDialogs map[string][]*ProblematicDialog) {
	indexPath := filepath.Join(outputBaseDir, "INDEX.md")

	var content strings.Builder
	content.WriteString("# Индекс проблемных диалогов\n\n")
	content.WriteString("## Статистика по типам проблем\n\n")

	total := 0
	for typeKey, count := range stats {
		typeName := problemTypes[typeKey].Name
		content.WriteString(fmt.Sprintf("### %s\n", typeName))
		content.WriteString(fmt.Sprintf("- **Количество диалогов:** %d\n", count))
		content.WriteString("- **Диалоги:** ")

		// Перечисляем все диалоги этого типа
		if dialogs, ok := allDialogs[typeKey]; ok && len(dialogs) > 0 {
			for i, dialog := range dialogs {
				if i > 0 {
					content.WriteString(", ")
				}
				content.WriteString(dialog.FolderName)
			}
		}
		content.WriteString("\n\n")

		total += count
	}

	content.WriteString(fmt.Sprintf("## Всего проблемных диалогов: %d\n\n", total))

	content.WriteString("## Структура директорий\n\n")
	content.WriteString("```\n")
	content.WriteString("problematicDialogs/\n")
	for typeKey := range problemTypes {
		typeName := problemTypes[typeKey].Name
		content.WriteString(fmt.Sprintf("├── %s/                # %s\n", typeKey, typeName))
		content.WriteString(fmt.Sprintf("│   ├── AAA-11314/    # Папка диалога\n"))
		content.WriteString(fmt.Sprintf("│   │   ├── conv_AAA-11314_info.json\n"))
		content.WriteString(fmt.Sprintf("│   │   ├── conv_AAA-11314_chat.json\n"))
		content.WriteString(fmt.Sprintf("│   │   └── trigger_info.txt    # Найденные триггеры\n"))
		content.WriteString(fmt.Sprintf("│   └── BBB-22345/\n"))
		content.WriteString(fmt.Sprintf("│       └── ...\n"))
	}
	content.WriteString("└── INDEX.md              # Этот файл\n")
	content.WriteString("```\n\n")

	content.WriteString("## Правила фильтрации\n\n")
	content.WriteString("1. Проверяются только сообщения от клиентов (user_id с префиксом `user_`)\n")
	content.WriteString("2. Триггеры ищутся как отдельные слова\n")
	content.WriteString("3. Один диалог может относиться к нескольким типам проблем\n")
	content.WriteString("4. Исходные папки диалогов сохраняются полностью со всеми файлами\n")

	if err := ioutil.WriteFile(indexPath, []byte(content.String()), 0644); err != nil {
		fmt.Printf("Ошибка создания индексного файла: %v\n", err)
	} else {
		fmt.Printf("\nСоздан индексный файл: %s\n", indexPath)
	}
}
