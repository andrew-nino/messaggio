// Имитируем обработку данных клиента, полученных через Kafka и отправляем результат
// через HTTP клиента обратно для внесения результата в БД.
// Обработка заключается в условном прохождении проверки (Approve = 1) или в отказе (Approve = -1)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"math/rand"

	"github.com/segmentio/kafka-go"
)

type Answer struct {
	ID      int `db:"id" json:"id"`
	Approve int `db:"approve" json:"approve"`
}

type Candidate struct {
	ID     int
	Client Client
}

type Client struct {
	Surname    string `bd:"surname" json:"surname" binding:"required"`
	Name       string `bd:"name" json:"name"`
	Patronymic string `bd:"patronymic" json:"patronymic"`
	Email      string `bd:"email" json:"email" binding:"required"`
	Approve    int    `bd:"approve" json:"approve" default:"0"`
}

func main() {
	// Getting the values ​​for the configuration
	brokers := os.Getenv("KAFKA_BROKERS")
	group := os.Getenv("CONSUMER_GROUP")
	topic := os.Getenv("KAFKA_TOPIC")
	recipient_host := os.Getenv("RECIPIENT_HOST")

	if len(brokers) == 0 {
		panic("no Kafka bootstrap brokers defined, please set the -brokers flag")
	}
	if len(topic) == 0 {
		panic("no topic given to be consumed, please set the -topic flag")
	}
	if len(group) == 0 {
		panic("no Kafka consumer group defined, please set the -group flag")
	}
	if len(recipient_host) == 0 {
		panic("no recipient_host defined, please set the -recipient_host flag")
	}

	// make a new reader that consumes from topic
	addrs := strings.Split(brokers, ",")

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  addrs,
		GroupID:  group,
		Topic:    topic,
		MinBytes: 10e2, // 1KB
		MaxBytes: 10e6, // 10MB
	})

	fmt.Println("Starting inspector...")

	var workerChan = make(chan Candidate)
	defer close(workerChan)

	go func() {
		for {
			go ReadMessage(r, workerChan)
			answer := CheckCandidate(workerChan)
			SendMessage(answer, recipient_host)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	fmt.Println("\nShutting Down inspector...")

	if err := r.Close(); err != nil {
		fmt.Printf("failed to close reader: %v\n", err)
	}
}

// ReadMessage reads messages from the bus, processes them and returns them in the channel
func ReadMessage(reader *kafka.Reader, ch chan Candidate) {
	var client = Client{}
	var candidate = Candidate{}

	for {
		m, err := reader.ReadMessage(context.Background())
		if err != nil {
			break
		}
		err = json.Unmarshal(m.Value, &client)
		if err != nil {
			fmt.Printf("Error unmarshalling: %v\n", err)
		}
		id, _ := strconv.Atoi(string(m.Key))
		candidate.ID = id
		candidate.Client = client
		ch <- candidate
	}
}

// CheckCandidate simulates a check and returns a structure ready to be sent
func CheckCandidate(ch chan Candidate) Answer {

	workerData := <-ch
	// Имитируем работу.
	time.After(3 * time.Second)
	// Генерируем значение (-1 или 1) с соотношением 30/70
	var num int
	if rand.Intn(10) < 3 {
		num = -1
	} else {
		num = 1
	}

	answer := Answer{
		ID:      workerData.ID,
		Approve: num,
	}
	return answer
}

// SendMessage sends the response back via HTTP
func SendMessage(answer Answer, recipient_host string) {

	dataJson, err := json.Marshal(answer)
	if err != nil {
		fmt.Printf("Error marshalling : %v\n", err)
	}

	fmt.Printf("Sending a solution to id  %d\n", answer.ID)

	buffer := bytes.NewBuffer(dataJson)

	clientHTTP := &http.Client{}

	req, err := http.NewRequest(http.MethodPost, "http://"+recipient_host+"/approval/", buffer)
	if err != nil {
		fmt.Printf("Error creating HTTP request: %v\n", err)
	}

	resp, err := clientHTTP.Do(req)
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		fmt.Printf("Error sending HTTP request: %v\n", err)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading HTTP response body: %v\n", err)
	}
	fmt.Printf("response: %s\n", string(bodyBytes))
}
