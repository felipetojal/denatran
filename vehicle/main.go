package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	VehicleRegisterTopic  = "vehicle/register"   // emplacar veículo
	VehicleIPVATopic      = "vehicle/ipva"       // calcular IPVA
	VehicleTransferTopic  = "vehicle/transfer"   // transferir proprietário
	VehicleQueryYearTopic = "vehicle/query/year" // veículos emplacados em um ano
	VehicleGetTopic       = "vehicle/get"        // consulta por placa (usado pelo serviço de multas)

	DriverGetTopic = "driver/get" // serviço de condutores

	IPVARate = 0.02 // alíquota de 2%
)

// Payload de "vehicle/register". "ano" é opcional (padrão: ano corrente)
type RegisterPayload struct {
	Placa  string  `json:"placa"`
	Modelo string  `json:"modelo"`
	Valor  float64 `json:"valor"`
	CPF    string  `json:"cpf"`
	Ano    int     `json:"ano"`
}

// Payload de "vehicle/ipva" e "vehicle/get"
type PlatePayload struct {
	Placa string `json:"placa"`
}

// Payload de "vehicle/transfer"
type TransferPayload struct {
	Placa string `json:"placa"`
	CPF   string `json:"cpf"` // CPF do novo dono
}

// Payload de "vehicle/query/year"
type YearPayload struct {
	Ano int `json:"ano"`
}

// Respostas
type IPVAResponse struct {
	Placa    string  `json:"placa"`
	Valor    float64 `json:"valor"`
	Aliquota float64 `json:"aliquota"`
	IPVA     float64 `json:"ipva"`
}

type TransferResponse struct {
	Placa       string `json:"placa"`
	CPFAnterior string `json:"cpf_anterior"`
	CPFNovo     string `json:"cpf_novo"`
}

type YearResponse struct {
	Ano      int       `json:"ano"`
	Total    int       `json:"total"`
	Veiculos []Vehicle `json:"veiculos"`
}

// Service agrupa as dependências do microsserviço
type Service struct {
	db      *Database
	replies *Replies
}

func normalizePlate(p string) string {
	return strings.ToUpper(strings.TrimSpace(p))
}

// Confere, junto ao serviço de condutores, se o CPF está cadastrado
func (s *Service) ensureDriverExists(ctx context.Context, c mqtt.Client, cpf string) error {
	if _, err := s.replies.Call(ctx, c, DriverGetTopic, map[string]string{"cpf": cpf}); err != nil {
		return fmt.Errorf("cannot validate driver %s: %w", cpf, err)
	}
	return nil
}

// Emplaca um veículo
func (s *Service) register(ctx context.Context, c mqtt.Client, p RegisterPayload) (any, error) {
	v := Vehicle{
		Placa:           normalizePlate(p.Placa),
		Modelo:          strings.TrimSpace(p.Modelo),
		Valor:           p.Valor,
		CPF:             strings.TrimSpace(p.CPF),
		AnoEmplacamento: p.Ano,
	}

	if v.Placa == "" || v.Modelo == "" || v.CPF == "" {
		return nil, errors.New("placa, modelo and cpf are required")
	}
	if v.Valor <= 0 {
		return nil, errors.New("valor must be greater than zero")
	}
	if v.AnoEmplacamento == 0 {
		v.AnoEmplacamento = time.Now().Year()
	}

	if err := s.ensureDriverExists(ctx, c, v.CPF); err != nil {
		return nil, err
	}
	if err := s.db.addVehicle(ctx, v); err != nil {
		return nil, err
	}

	log.Printf("Success: vehicle %s (%s) registered for CPF %s in %d", v.Placa, v.Modelo, v.CPF, v.AnoEmplacamento)
	return v, nil
}

// Calcula o IPVA (2% do valor do veículo)
func (s *Service) ipva(ctx context.Context, _ mqtt.Client, p PlatePayload) (any, error) {
	v, err := s.db.getVehicle(ctx, normalizePlate(p.Placa))
	if err != nil {
		return nil, err
	}

	return IPVAResponse{
		Placa:    v.Placa,
		Valor:    v.Valor,
		Aliquota: IPVARate,
		IPVA:     math.Round(v.Valor*IPVARate*100) / 100,
	}, nil
}

// Transfere o veículo para um novo dono
func (s *Service) transfer(ctx context.Context, c mqtt.Client, p TransferPayload) (any, error) {
	placa := normalizePlate(p.Placa)
	novoCPF := strings.TrimSpace(p.CPF)
	if placa == "" || novoCPF == "" {
		return nil, errors.New("placa and cpf are required")
	}

	v, err := s.db.getVehicle(ctx, placa)
	if err != nil {
		return nil, err
	}
	if v.CPF == novoCPF {
		return nil, fmt.Errorf("vehicle %s already belongs to CPF %s", placa, novoCPF)
	}

	if err := s.ensureDriverExists(ctx, c, novoCPF); err != nil {
		return nil, err
	}
	if err := s.db.transferVehicle(ctx, placa, novoCPF); err != nil {
		return nil, err
	}

	log.Printf("Success: vehicle %s transferred from %s to %s", placa, v.CPF, novoCPF)
	return TransferResponse{Placa: placa, CPFAnterior: v.CPF, CPFNovo: novoCPF}, nil
}

// Lista os veículos emplacados em um ano
func (s *Service) queryYear(ctx context.Context, _ mqtt.Client, p YearPayload) (any, error) {
	if p.Ano <= 0 {
		return nil, errors.New("ano is required")
	}

	vehicles, err := s.db.listVehiclesByYear(ctx, p.Ano)
	if err != nil {
		return nil, err
	}
	return YearResponse{Ano: p.Ano, Total: len(vehicles), Veiculos: vehicles}, nil
}

// Consulta um veículo pela placa (uso interno entre serviços)
func (s *Service) get(ctx context.Context, _ mqtt.Client, p PlatePayload) (any, error) {
	return s.db.getVehicle(ctx, normalizePlate(p.Placa))
}

// Função principal do serviço
func main() {
	db := connectWithRetry(
		getenv("DB_PORT", "5432"),
		getenv("DB_HOST", "vehicle_db"),
		getenv("DB_USER", "postgres"),
		getenv("DB_PASSWORD", "postgres"),
		getenv("DB_NAME", "vehicle_db"),
	)
	log.Println("Connected to PostgreSQL")

	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := db.init(initCtx)
	cancel()
	if err != nil {
		log.Fatalf("Failed to initialize DB tables: %v", err)
	}

	svc := &Service{db: db, replies: newReplies("vehicle")}

	client := connectMQTT("vehicle_service", func(c mqtt.Client) {
		subscribe(c, svc.replies.topic, svc.replies.handler)
		subscribe(c, VehicleRegisterTopic, handle(svc.register))
		subscribe(c, VehicleIPVATopic, handle(svc.ipva))
		subscribe(c, VehicleTransferTopic, handle(svc.transfer))
		subscribe(c, VehicleQueryYearTopic, handle(svc.queryYear))
		subscribe(c, VehicleGetTopic, handle(svc.get))
	})
	log.Println("Connected to MQTT Broker")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down vehicle service...")
	client.Disconnect(250)
}
