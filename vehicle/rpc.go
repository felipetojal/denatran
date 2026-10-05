package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Gera um identificador aleatório curto
func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Replies implementa chamadas request/response sobre o MQTT: cada requisição
// leva o tópico de resposta deste serviço ("reply_to") e um id de correlação.
type Replies struct {
	topic   string
	mu      sync.Mutex
	pending map[string]chan Response
}

func newReplies(service string) *Replies {
	return &Replies{
		topic:   "reply/" + service + "/" + randID(),
		pending: make(map[string]chan Response),
	}
}

// Handler do tópico de respostas: entrega a resposta a quem está esperando
func (r *Replies) handler(_ mqtt.Client, msg mqtt.Message) {
	var resp Response
	if err := json.Unmarshal(msg.Payload(), &resp); err != nil {
		return
	}

	r.mu.Lock()
	ch, ok := r.pending[resp.ID]
	r.mu.Unlock()

	if ok {
		select {
		case ch <- resp:
		default:
		}
	}
}

// Publica uma requisição em "topic" e aguarda a resposta do outro serviço
func (r *Replies) Call(ctx context.Context, c mqtt.Client, topic string, payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	id := randID()
	body["id"] = id
	body["reply_to"] = r.topic

	out, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan Response, 1)
	r.mu.Lock()
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}()

	if token := c.Publish(topic, 1, false, out); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	select {
	case resp := <-ch:
		if !resp.OK {
			return nil, errors.New(resp.Error)
		}
		return resp.Data, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("no response from %s: %w", topic, ctx.Err())
	}
}
