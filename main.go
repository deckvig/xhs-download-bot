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

func main() {
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
				log.Printf("Received message from chat %d: %s", update.Message.Chat.ID, update.Message.Text)
				// 在这里处理消息
			}

			if err = download(update.Message.Text); err != nil {
				sendMessage(update.Message.Chat.ID, fmt.Sprintf("下载失败, url: %s,err: %v", getUrl(update.Message.Text), err))
			} else {
				sendMessage(update.Message.Chat.ID, fmt.Sprintf("下载成功, url: %s", getUrl(update.Message.Text)))
			}

			// 更新最后处理的 update_id
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

func download(message string) error {
	payload := strings.NewReader(fmt.Sprintf(`{
        "url": "%s",
 "download": true
    }`, message))

	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodPost, backendURL, payload)

	if err != nil {
		fmt.Println(err)
		return err
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return err
	}
	fmt.Println(string(body))
	return nil
}

var re = regexp.MustCompile(`https?://[^\s，]+`)

func getUrl(message string) string {
	// 查找匹配的 URL
	urls := re.FindAllString(message, -1)
	if len(urls) == 0 {
		return message
	}
	return urls[0]
}
