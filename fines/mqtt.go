package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Lê uma variável de ambiente, usando um valor padrão caso ela não exista
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Metadados presentes em toda requisição: para onde (e com qual id) responder
type Meta struct {
	ReplyTo string `json:"reply_to"`
	ID      string `json:"id"`
}

// Envelope de resposta publicado no tópico "reply_to" do solicitante
type Response struct {
	ID    string          `json:"id,omitempty"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Publica a resposta (ou o erro) para quem fez a requisição
func reply(client mqtt.Client, meta Meta, data any, handlerErr error) {
	if meta.ReplyTo == "" {
		return // quem enviou não espera resposta
	}

	resp := Response{ID: meta.ID}
	if handlerErr != nil {
		resp.Error = handlerErr.Error()
	} else if b, err := json.Marshal(data); err != nil {
		resp.Error = fmt.Sprintf("unable to encode response: %v", err)
	} else {
		resp.OK = true
		resp.Data = b
	}

	out, _ := json.Marshal(resp)
	client.Publish(meta.ReplyTo, 1, false, out)
}

// Adapta uma função de negócio tipada para um mqtt.MessageHandler:
// decodifica o JSON, executa a função e responde ao solicitante.
func handle[Req any](fn func(ctx context.Context, c mqtt.Client, req Req) (any, error)) mqtt.MessageHandler {
	return func(client mqtt.Client, msg mqtt.Message) {
		var meta Meta
		if err := json.Unmarshal(msg.Payload(), &meta); err != nil {
			log.Printf("[%s] invalid payload: %v", msg.Topic(), err)
			return
		}

		var req Req
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			reply(client, meta, nil, fmt.Errorf("invalid payload: %w", err))
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		data, err := fn(ctx, client, req)
		if err != nil {
			log.Printf("[%s] error: %v", msg.Topic(), err)
		}
		reply(client, meta, data, err)
	}
}

// Inscreve o cliente em um tópico
func subscribe(c mqtt.Client, topic string, h mqtt.MessageHandler) {
	if token := c.Subscribe(topic, 1, h); token.Wait() && token.Error() != nil {
		log.Printf("Failed to subscribe to topic %s: %v", topic, token.Error())
		return
	}
	log.Printf("Listening for messages on topic: %s", topic)
}

// Conecta ao broker.
// As inscrições são feitas no OnConnect para serem refeitas em reconexões.
func connectMQTT(clientID string, onConnect func(c mqtt.Client)) mqtt.Client {
	opts := mqtt.NewClientOptions().
		AddBroker(getenv("MQTT_BROKER", "tcp://broker:1883")).
		SetClientID(clientID).
		SetAutoReconnect(true).
		// Handlers em goroutines separadas: necessário para que um handler possa
		// aguardar a resposta de outro serviço sem travar o recebimento.
		SetOrderMatters(false).
		SetOnConnectHandler(onConnect)

	client := mqtt.NewClient(opts)
	for attempt := 1; ; attempt++ {
		token := client.Connect()
		token.Wait()
		if token.Error() == nil {
			return client
		}
		if attempt >= 30 {
			log.Fatalf("Failed to connect to MQTT broker: %v", token.Error())
		}
		log.Printf("MQTT broker not ready (attempt %d/30): %v", attempt, token.Error())
		time.Sleep(2 * time.Second)
	}
}
