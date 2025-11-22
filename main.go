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
	"time"
)

var (
	telegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	backendURL       = os.Getenv("BACKEND_URL")
	lastUpdateIDFile = "last_update_id.txt" // 用于存储最后一个处理的 update_id
	// 匹配 http 或 https 开头，后面跟着非空格或非中文逗号的字符
	urlRegex = regexp.MustCompile(`https?://[^\s，]+`)
)

// Update represents a Telegram update structure
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message represents a Telegram message structure
type Message struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
}
type Result struct {
	Ok          bool     `json:"ok"`
	ErrorCode   int      `json:"error_code"`
	Description string   `json:"description"`
	Result      []Update `json:"result"`
}

// getUpdates fetches new updates from Telegram
func getUpdates(lastUpdateID int64) ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", telegramBotToken, lastUpdateID+1)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result Result

	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	if !result.Ok {
		return nil, fmt.Errorf("failed to get updates: %s", body)
	}

	return result.Result, nil
}

// getLastUpdateID reads the last processed update ID from a file
func getLastUpdateID() (int64, error) {
	if _, err := os.Stat(lastUpdateIDFile); os.IsNotExist(err) {
		// If the file does not exist, start from update ID 0
		return 0, nil
	}
	data, err := os.ReadFile(lastUpdateIDFile)
	if err != nil {
		// If the file does not exist, start from update ID 0
		return 0, nil
	}

	var lastUpdateID int64
	_, err = fmt.Sscanf(string(data), "%d", &lastUpdateID)
	if err != nil {
		return 0, err
	}

	return lastUpdateID, nil
}

// saveLastUpdateID saves the last processed update ID to a file
func saveLastUpdateID(lastUpdateID int64) error {
	return os.WriteFile(lastUpdateIDFile, []byte(fmt.Sprintf("%d", lastUpdateID)), 0644)
}

// sendMessage sends a message to a specified chat
func sendMessage(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramBotToken)
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	log.Printf("Response from sendMessage: %s\n", body)
	return nil
}

// extractUrls 从消息文本中提取所有匹配的 URL 地址
func extractUrls(message string) []string {
	return urlRegex.FindAllString(message, -1)
}

// download 发送单个 URL 到后端进行下载
func download(downloadURL string) error {
	// 注意：这里使用 downloadURL，而不是整个 message
	payload := strings.NewReader(fmt.Sprintf(`{
		"url": "%s",
		"download": true
	}`, downloadURL))

	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodPost, backendURL, payload)

	if err != nil {
		log.Printf("Error creating request for URL %s: %v", downloadURL, err)
		return err
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		log.Printf("Error performing request for URL %s: %v", downloadURL, err)
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("Error reading response body for URL %s: %v", downloadURL, err)
		return err
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("backend returned status code %d, body: %s", res.StatusCode, string(body))
	}
	
	log.Printf("Backend response for URL %s: %s", downloadURL, string(body))
	return nil
}

func main() {
	if telegramBotToken == "" || backendURL == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN or BACKEND_URL environment variable is not set.")
	}
	
	lastUpdateID, err := getLastUpdateID()
	if err != nil {
		log.Fatalf("Failed to read last update ID: %v", err)
	}

	for {
		fmt.Println("start get update message ...")
		updates, err := getUpdates(lastUpdateID)
		if err != nil {
			log.Printf("Failed to get updates: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		fmt.Printf("get [%d] message\n", len(updates))
		for _, update := range updates {
			if update.Message != nil {
				messageText := update.Message.Text
				chatID := update.Message.Chat.ID
				log.Printf("Received message from chat %d: %s", chatID, messageText)
				
				// 1. 提取所有 URL
				urlsToDownload := extractUrls(messageText)

				if len(urlsToDownload) == 0 {
					log.Println("No URLs found in the message, sending notification.")
					sendMessage(chatID, "消息中未找到任何可识别的 URL 地址，请确保链接以 http:// 或 https:// 开头。")
				} else {
					sendMessage(chatID, fmt.Sprintf("发现 %d 个 URL，开始按顺序下载...", len(urlsToDownload)))
				}

				// 2. 循环下载所有提取的 URL
				for _, url := range urlsToDownload {
					log.Printf("Attempting to download URL: %s", url)
					
					// 调用 download 函数，传入单个 URL
					if err := download(url); err != nil {
						sendMessage(chatID, fmt.Sprintf("下载失败: \nURL: %s\n错误: %v", url, err))
					} else {
						sendMessage(chatID, fmt.Sprintf("下载成功: \nURL: %s", url))
					}
				}
			}

			// 3. 更新最后处理的 update_id
			if update.UpdateID > lastUpdateID {
				lastUpdateID = update.UpdateID
			}
		}

		// 保存最后处理的 update_id
		err = saveLastUpdateID(lastUpdateID)
		if err != nil {
			log.Printf("Failed to save last update ID: %v", err)
		}

		// 休眠一段时间再继续轮询
		fmt.Println("go to sleep 2s")
		time.Sleep(2 * time.Second)
	}
}