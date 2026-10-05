package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	DriverRegisterTopic = "driver/register" // cadastrar condutor
	DriverGetTopic      = "driver/get"      // consulta por CPF (usado pelos outros serviços)
)

// Payload de "driver/register"
type DriverPayload struct {
	CPF  string `json:"cpf"`
	Nome string `json:"nome"`
}

// Payload de "driver/get"
type GetPayload struct {
	CPF string `json:"cpf"`
}

// Service agrupa as dependências do microsserviço
type Service struct {
	db *Database
}

// Cadastra um condutor
func (s *Service) register(ctx context.Context, _ mqtt.Client, p DriverPayload) (any, error) {
	p.CPF = strings.TrimSpace(p.CPF)
	p.Nome = strings.TrimSpace(p.Nome)
	if p.CPF == "" || p.Nome == "" {
		return nil, errors.New("cpf and nome are required")
	}

	if err := s.db.addDriver(ctx, p.CPF, p.Nome); err != nil {
		return nil, err
	}

	log.Printf("Success: Driver %s (CPF: %s) registered in database.", p.Nome, p.CPF)
	return p, nil
}

// Retorna os dados de um condutor a partir do CPF
func (s *Service) get(ctx context.Context, _ mqtt.Client, p GetPayload) (any, error) {
	cpf := strings.TrimSpace(p.CPF)
	if cpf == "" {
		return nil, errors.New("cpf is required")
	}
	return s.db.getDriverByCPF(ctx, cpf)
}

// Função principal do serviço
func main() {
	// Inicializa o banco de dados PostgreSQL
	db := connectWithRetry(
		getenv("DB_PORT", "5432"),
		getenv("DB_HOST", "driver_db"),
		getenv("DB_USER", "postgres"),
		getenv("DB_PASSWORD", "postgres"),
		getenv("DB_NAME", "driver_db"),
	)
	log.Println("Connected to PostgreSQL")

	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := db.init(initCtx)
	cancel()
	if err != nil {
		log.Fatalf("Failed to initialize DB tables: %v", err)
	}

	svc := &Service{db: db}

	// Conecta ao broker MQTT e inscreve o serviço nos seus tópicos
	client := connectMQTT("driver_service", func(c mqtt.Client) {
		subscribe(c, DriverRegisterTopic, handle(svc.register))
		subscribe(c, DriverGetTopic, handle(svc.get))
	})
	log.Println("Connected to MQTT Broker")

	// Mantém o servidor rodando e lida com via Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down driver service...")
	client.Disconnect(250)
}
