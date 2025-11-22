package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
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

// download executes the external gallery-dl command for a single URL
func download(downloadURL string) error {
	// 获取环境变量中的代理设置
	proxyURL := os.Getenv("HTTP_PROXY")
	if proxyURL == "" {
		// 如果未设置代理，返回错误
		return fmt.Errorf("HTTP_PROXY environment variable is not set")
	}

	// 构造命令: gallery-dl --proxy <proxy> <url>
	// 假设 gallery-dl 在 PATH 中可用
	cmd := exec.Command("gallery-dl", "--proxy", proxyURL, downloadURL)

	// 设置命令的标准输出和标准错误流
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 运行命令
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run gallery-dl: %v", err)
	}

	return nil
}

// runDownloadWithRetry attempts to download a URL up to maxRetries times, notifying the user on success/failure of each attempt.
func runDownloadWithRetry(url string, chatID int64) error {
	const maxRetries = 3
	const delay = 5 * time.Second
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		log.Printf("Attempt %d/%d: Downloading URL: %s", i+1, maxRetries, url)

		if err := download(url); err == nil {
			// 成功：发送通知并返回
			sendMessage(chatID, fmt.Sprintf("下载成功 (第 %d 次尝试): \nURL: %s", i+1, url))
			return nil
		} else {
			lastErr = err
			log.Printf("Download attempt %d failed for URL %s: %v", i+1, url, err)

			// 失败：如果是最后一次尝试，则不休眠
			if i < maxRetries-1 {
				sendMessage(chatID, fmt.Sprintf("下载失败 (第 %d 次尝试，重试中...): \nURL: %s\n错误: %v", i+1, url, err))
				time.Sleep(delay) // 等待后重试
			}
		}
	}

	// 所有重试均失败后，返回最终错误
	return fmt.Errorf("下载最终失败, 经过 %d 次尝试: %w", maxRetries, lastErr)
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
					// 调用带有重试逻辑的下载函数
					if err := runDownloadWithRetry(url, chatID); err != nil {
						// 仅在所有重试都失败后发送最终失败通知
						sendMessage(chatID, fmt.Sprintf("下载任务终止: \nURL: %s\n错误: %v", url, err))
					}
					// 成功消息已在 runDownloadWithRetry 内部发送
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