package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	FineRegisterTopic   = "fine/register"      // lançar multa
	FinesByPlateTopic   = "fines/query/plate"  // multas de um veículo
	FinesByDriverTopic  = "fines/query/driver" // multas de um condutor
	FinesByYearTopic    = "fines/query/year"   // multas lançadas em um ano
	FinesTopPointsTopic = "fines/query/points" // top 5 condutores por pontuação

	VehicleGetTopic = "vehicle/get" // serviço de veículos
	DriverGetTopic  = "driver/get"  // serviço de condutores

	TopDriversLimit = 5
)

// Payload de "fine/register"
type RegisterPayload struct {
	Ano       int    `json:"ano"`
	Descricao string `json:"descricao"`
	Pontuacao int    `json:"pontuacao"`
	Placa     string `json:"placa"`
}

// Payload de "fines/query/plate" ("ano" opcional: 0 = todos os anos)
type PlateQuery struct {
	Placa string `json:"placa"`
	Ano   int    `json:"ano"`
}

// Payload de "fines/query/driver" ("ano" opcional: 0 = todos os anos)
type DriverQuery struct {
	CPF string `json:"cpf"`
	Ano int    `json:"ano"`
}

// Payload de "fines/query/year"
type YearQuery struct {
	Ano int `json:"ano"`
}

// Dados do condutor, obtidos no serviço de condutores
type DriverInfo struct {
	CPF  string `json:"cpf"`
	Nome string `json:"nome"`
}

// Multa acompanhada dos dados do condutor que a recebeu
type FineWithDriver struct {
	Fine
	Condutor *DriverInfo `json:"condutor"`
}

// Resposta das consultas de multas
type FinesResponse struct {
	Total          int              `json:"total"`
	PontuacaoTotal int              `json:"pontuacao_total"`
	Multas         []FineWithDriver `json:"multas"`
}

// Posição do ranking de pontuação
type RankingEntry struct {
	Posicao int    `json:"posicao"`
	CPF     string `json:"cpf"`
	Nome    string `json:"nome"`
	Pontos  int    `json:"pontos"`
}

type RankingResponse struct {
	Condutores []RankingEntry `json:"condutores"`
}

// Service agrupa as dependências do microsserviço
type Service struct {
	db      *Database
	replies *Replies
}

func normalizePlate(p string) string {
	return strings.ToUpper(strings.TrimSpace(p))
}

// Busca os dados do condutor no serviço de condutores (com cache por requisição).
// Se a consulta falhar, devolve apenas o CPF para não perder o restante da resposta.
func (s *Service) driverInfo(ctx context.Context, c mqtt.Client, cache map[string]*DriverInfo, cpf string) *DriverInfo {
	if d, ok := cache[cpf]; ok {
		return d
	}

	d := &DriverInfo{CPF: cpf}
	raw, err := s.replies.Call(ctx, c, DriverGetTopic, map[string]string{"cpf": cpf})
	if err != nil {
		log.Printf("warning: unable to fetch driver %s: %v", cpf, err)
	} else if err := json.Unmarshal(raw, d); err != nil {
		log.Printf("warning: invalid driver data for %s: %v", cpf, err)
	}

	cache[cpf] = d
	return d
}

// Monta a resposta das consultas anexando os dados do condutor a cada multa
func (s *Service) buildResponse(ctx context.Context, c mqtt.Client, fines []Fine) FinesResponse {
	cache := map[string]*DriverInfo{}
	resp := FinesResponse{Total: len(fines), Multas: make([]FineWithDriver, 0, len(fines))}

	for _, f := range fines {
		resp.PontuacaoTotal += f.Pontuacao
		resp.Multas = append(resp.Multas, FineWithDriver{
			Fine:     f,
			Condutor: s.driverInfo(ctx, c, cache, f.CPF),
		})
	}
	return resp
}

// Lança uma multa. O condutor multado é o proprietário atual do veículo,
// descoberto consultando o serviço de veículos.
func (s *Service) register(ctx context.Context, c mqtt.Client, p RegisterPayload) (any, error) {
	placa := normalizePlate(p.Placa)
	descricao := strings.TrimSpace(p.Descricao)

	if placa == "" || descricao == "" {
		return nil, errors.New("placa and descricao are required")
	}
	if p.Ano <= 0 {
		return nil, errors.New("ano is required")
	}
	if p.Pontuacao <= 0 {
		return nil, errors.New("pontuacao must be greater than zero")
	}

	raw, err := s.replies.Call(ctx, c, VehicleGetTopic, map[string]string{"placa": placa})
	if err != nil {
		return nil, fmt.Errorf("cannot find vehicle %s: %w", placa, err)
	}

	var vehicle struct {
		CPF string `json:"cpf"`
	}
	if err := json.Unmarshal(raw, &vehicle); err != nil || vehicle.CPF == "" {
		return nil, fmt.Errorf("invalid vehicle data for %s", placa)
	}

	fine := Fine{Ano: p.Ano, Descricao: descricao, Pontuacao: p.Pontuacao, Placa: placa, CPF: vehicle.CPF}
	id, err := s.db.addFine(ctx, fine)
	if err != nil {
		return nil, err
	}
	fine.ID = id

	log.Printf("Success: fine #%d (%d pts) registered for vehicle %s / CPF %s", id, fine.Pontuacao, placa, fine.CPF)
	return fine, nil
}

// Multas de um veículo (e dados do condutor que levou cada multa)
func (s *Service) byPlate(ctx context.Context, c mqtt.Client, q PlateQuery) (any, error) {
	placa := normalizePlate(q.Placa)
	if placa == "" {
		return nil, errors.New("placa is required")
	}

	fines, err := s.db.finesByPlate(ctx, placa, q.Ano)
	if err != nil {
		return nil, err
	}
	return s.buildResponse(ctx, c, fines), nil
}

// Multas de um condutor em um dado ano
func (s *Service) byDriver(ctx context.Context, c mqtt.Client, q DriverQuery) (any, error) {
	cpf := strings.TrimSpace(q.CPF)
	if cpf == "" {
		return nil, errors.New("cpf is required")
	}

	fines, err := s.db.finesByDriver(ctx, cpf, q.Ano)
	if err != nil {
		return nil, err
	}
	return s.buildResponse(ctx, c, fines), nil
}

// Multas lançadas em um dado ano
func (s *Service) byYear(ctx context.Context, c mqtt.Client, q YearQuery) (any, error) {
	if q.Ano <= 0 {
		return nil, errors.New("ano is required")
	}

	fines, err := s.db.finesByYear(ctx, q.Ano)
	if err != nil {
		return nil, err
	}
	return s.buildResponse(ctx, c, fines), nil
}

// Os 5 condutores com maior pontuação, em ordem decrescente
func (s *Service) topPoints(ctx context.Context, c mqtt.Client, _ struct{}) (any, error) {
	top, err := s.db.topDrivers(ctx, TopDriversLimit)
	if err != nil {
		return nil, err
	}

	cache := map[string]*DriverInfo{}
	resp := RankingResponse{Condutores: make([]RankingEntry, 0, len(top))}
	for i, dp := range top {
		d := s.driverInfo(ctx, c, cache, dp.CPF)
		resp.Condutores = append(resp.Condutores, RankingEntry{
			Posicao: i + 1,
			CPF:     dp.CPF,
			Nome:    d.Nome,
			Pontos:  dp.Pontos,
		})
	}
	return resp, nil
}

// Função principal do serviço
func main() {
	db := connectWithRetry(
		getenv("DB_PORT", "5432"),
		getenv("DB_HOST", "fines_db"),
		getenv("DB_USER", "postgres"),
		getenv("DB_PASSWORD", "postgres"),
		getenv("DB_NAME", "fines_db"),
	)
	log.Println("Connected to PostgreSQL")

	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := db.init(initCtx)
	cancel()
	if err != nil {
		log.Fatalf("Failed to initialize DB tables: %v", err)
	}

	svc := &Service{db: db, replies: newReplies("fines")}

	client := connectMQTT("fines_service", func(c mqtt.Client) {
		subscribe(c, svc.replies.topic, svc.replies.handler)
		subscribe(c, FineRegisterTopic, handle(svc.register))
		subscribe(c, FinesByPlateTopic, handle(svc.byPlate))
		subscribe(c, FinesByDriverTopic, handle(svc.byDriver))
		subscribe(c, FinesByYearTopic, handle(svc.byYear))
		subscribe(c, FinesTopPointsTopic, handle(svc.topPoints))
	})
	log.Println("Connected to MQTT Broker")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down fines service...")
	client.Disconnect(250)
}
