// Cliente de linha de comando do DENATRAN.
// Publica requisições nos tópicos MQTT dos microsserviços e imprime a resposta.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

/*
	BARRAMENTO
*/

type Response struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

type Bus struct {
	client  mqtt.Client
	topic   string
	mu      sync.Mutex
	pending map[string]chan Response
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func randID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func newBus() (*Bus, error) {
	b := &Bus{
		topic:   "reply/client/" + randID(),
		pending: make(map[string]chan Response),
	}

	opts := mqtt.NewClientOptions().
		AddBroker(getenv("MQTT_BROKER", "tcp://broker:1883")).
		SetClientID("denatran_client_" + randID()).
		SetOrderMatters(false)
	b.client = mqtt.NewClient(opts)

	var err error
	for attempt := 1; attempt <= 15; attempt++ {
		token := b.client.Connect()
		token.Wait()
		if err = token.Error(); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("unable to connect to MQTT broker: %w", err)
	}

	token := b.client.Subscribe(b.topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var resp Response
		if json.Unmarshal(msg.Payload(), &resp) != nil {
			return
		}
		b.mu.Lock()
		ch, ok := b.pending[resp.ID]
		b.mu.Unlock()
		if ok {
			select {
			case ch <- resp:
			default:
			}
		}
	})
	if token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}
	return b, nil
}

func (b *Bus) Close() { b.client.Disconnect(250) }

// Publica a requisição em "topic" e aguarda a resposta.
func (b *Bus) Call(topic string, payload map[string]any) (json.RawMessage, error) {
	id := randID()
	body := map[string]any{"id": id, "reply_to": b.topic}
	for k, v := range payload {
		body[k] = v
	}
	out, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan Response, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
	}()

	if token := b.client.Publish(topic, 1, false, out); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	select {
	case resp := <-ch:
		if !resp.OK {
			return nil, errors.New(resp.Error)
		}
		return resp.Data, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for a response on %s (is the service running?)", topic)
	}
}

/*
	COMANDOS
*/

type flagSpec struct {
	name     string
	kind     string // "string", "int" ou "float"
	required bool
	usage    string
}

type command struct {
	topic string
	desc  string
	flags []flagSpec
}

var commands = map[string]command{
	"driver-register": {"driver/register", "Cadastrar condutor", []flagSpec{
		{"cpf", "string", true, "CPF do condutor"},
		{"nome", "string", true, "nome do condutor"},
	}},
	"vehicle-register": {"vehicle/register", "Emplacar veículo", []flagSpec{
		{"placa", "string", true, "placa"},
		{"modelo", "string", true, "modelo"},
		{"valor", "float", true, "valor do veículo"},
		{"cpf", "string", true, "CPF do condutor"},
		{"ano", "int", false, "ano do emplacamento (padrão: ano corrente)"},
	}},
	"vehicle-ipva": {"vehicle/ipva", "Calcular IPVA (2%)", []flagSpec{
		{"placa", "string", true, "placa"},
	}},
	"vehicle-transfer": {"vehicle/transfer", "Transferir proprietário", []flagSpec{
		{"placa", "string", true, "placa"},
		{"cpf", "string", true, "CPF do novo dono"},
	}},
	"vehicle-year": {"vehicle/query/year", "Veículos emplacados em um ano", []flagSpec{
		{"ano", "int", true, "ano"},
	}},
	"fine-register": {"fine/register", "Lançar multa", []flagSpec{
		{"ano", "int", true, "ano da multa"},
		{"descricao", "string", true, "descrição"},
		{"pontuacao", "int", true, "pontuação"},
		{"placa", "string", true, "placa do veículo"},
	}},
	"fines-plate": {"fines/query/plate", "Multas de um veículo (com dados do condutor)", []flagSpec{
		{"placa", "string", true, "placa"},
		{"ano", "int", false, "ano (padrão: todos)"},
	}},
	"fines-driver": {"fines/query/driver", "Multas de um condutor em um ano", []flagSpec{
		{"cpf", "string", true, "CPF do condutor"},
		{"ano", "int", false, "ano (padrão: todos)"},
	}},
	"fines-year": {"fines/query/year", "Multas lançadas em um ano", []flagSpec{
		{"ano", "int", true, "ano"},
	}},
	"fines-top": {"fines/query/points", "Top 5 condutores por pontuação", nil},
}

func usage() {
	fmt.Fprintln(os.Stderr, "Uso: client <comando> [flags]\n\nComandos:")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-17s %s\n", n, commands[n].desc)
	}
	fmt.Fprintf(os.Stderr, "  %-17s %s\n", "demo", "Executa um cenário completo e verifica os resultados")
	fmt.Fprintln(os.Stderr, "\nUse 'client <comando> -h' para ver as flags de cada comando.")
}

// Lê as flags do comando e monta o payload JSON
func parsePayload(name string, cmd command, args []string) map[string]any {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	strs := map[string]*string{}
	ints := map[string]*int{}
	floats := map[string]*float64{}

	for _, f := range cmd.flags {
		switch f.kind {
		case "string":
			strs[f.name] = fs.String(f.name, "", f.usage)
		case "int":
			ints[f.name] = fs.Int(f.name, 0, f.usage)
		case "float":
			floats[f.name] = fs.Float64(f.name, 0, f.usage)
		}
	}
	_ = fs.Parse(args)

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	payload := map[string]any{}
	for _, f := range cmd.flags {
		if f.required && !set[f.name] {
			fmt.Fprintf(os.Stderr, "flag obrigatória ausente: -%s\n", f.name)
			fs.Usage()
			os.Exit(2)
		}
		switch f.kind {
		case "string":
			payload[f.name] = *strs[f.name]
		case "int":
			payload[f.name] = *ints[f.name]
		case "float":
			payload[f.name] = *floats[f.name]
		}
	}
	return payload
}

func prettyPrint(data json.RawMessage) {
	var v any
	if json.Unmarshal(data, &v) != nil {
		fmt.Println(string(data))
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		os.Exit(2)
	}
	name, args := os.Args[1], os.Args[2:]

	if name != "demo" {
		if _, ok := commands[name]; !ok {
			fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n\n", name)
			usage()
			os.Exit(2)
		}
	}

	var payload map[string]any
	if cmd, ok := commands[name]; ok {
		payload = parsePayload(name, cmd, args)
	}

	bus, err := newBus()
	if err != nil {
		log.Fatal(err)
	}
	defer bus.Close()

	if name == "demo" {
		if !runDemo(bus) {
			bus.Close()
			os.Exit(1)
		}
		return
	}

	data, err := bus.Call(commands[name].topic, payload)
	if err != nil {
		bus.Close()
		log.Fatalf("erro: %v", err)
	}
	prettyPrint(data)
}
