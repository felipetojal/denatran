package main

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Estruturas mínimas para ler as respostas dos serviços
type demoFine struct {
	ID        int    `json:"id"`
	Placa     string `json:"placa"`
	Pontuacao int    `json:"pontuacao"`
	CPF       string `json:"cpf"`
	Condutor  *struct {
		CPF  string `json:"cpf"`
		Nome string `json:"nome"`
	} `json:"condutor"`
}

type demoFines struct {
	Total          int        `json:"total"`
	PontuacaoTotal int        `json:"pontuacao_total"`
	Multas         []demoFine `json:"multas"`
}

type demoVehicles struct {
	Total    int `json:"total"`
	Veiculos []struct {
		Placa string `json:"placa"`
		CPF   string `json:"cpf"`
	} `json:"veiculos"`
}

type demoRunner struct {
	bus    *Bus
	passed int
	failed int
}

func (r *demoRunner) check(name string, ok bool, detail string) {
	if ok {
		r.passed++
		fmt.Printf("  ✔ %s\n", name)
		return
	}
	r.failed++
	fmt.Printf("  ✘ %s — %s\n", name, detail)
}

// Chama um tópico esperando sucesso
func (r *demoRunner) ok(name, topic string, payload map[string]any) json.RawMessage {
	data, err := r.bus.Call(topic, payload)
	r.check(name, err == nil, fmt.Sprint(err))
	return data
}

// Chama um tópico esperando um erro
func (r *demoRunner) fails(name, topic string, payload map[string]any) {
	_, err := r.bus.Call(topic, payload)
	r.check(name, err != nil, "expected an error but the call succeeded")
	if err != nil {
		fmt.Printf("      (erro esperado: %v)\n", err)
	}
}

func countPlates(fines demoFines, plates ...string) int {
	n := 0
	for _, f := range fines.Multas {
		for _, p := range plates {
			if f.Placa == p {
				n++
			}
		}
	}
	return n
}

// Executa um cenário completo contra os microsserviços.
// Usa CPFs e placas únicos por execução, então pode ser repetido.
func runDemo(bus *Bus) bool {
	r := &demoRunner{bus: bus}

	n := time.Now().UnixNano() % 1000000
	cpfA := fmt.Sprintf("1%06d", n)
	cpfB :=  fmt.Sprintf("2%06d", n)
	plateA, plateB := fmt.Sprintf("A%06d", n), fmt.Sprintf("B%06d", n)
	const year, nextYear = 2024, 2025

	fmt.Println("\n[1] Condutores")
	r.ok("cadastrar condutor A", "driver/register", map[string]any{"cpf": cpfA, "nome": "Ana Demo"})
	r.ok("cadastrar condutor B", "driver/register", map[string]any{"cpf": cpfB, "nome": "Bruno Demo"})
	r.fails("rejeitar CPF duplicado", "driver/register", map[string]any{"cpf": cpfA, "nome": "Ana Demo"})

	fmt.Println("\n[2] Emplacamento e IPVA")
	r.ok("emplacar veículo A", "vehicle/register",
		map[string]any{"placa": plateA, "modelo": "Gol", "valor": 50000.0, "cpf": cpfA, "ano": year})
	r.ok("emplacar veículo B", "vehicle/register",
		map[string]any{"placa": plateB, "modelo": "Onix", "valor": 30000.0, "cpf": cpfB, "ano": year})
	r.fails("rejeitar condutor inexistente", "vehicle/register",
		map[string]any{"placa": "ZZZ" + fmt.Sprint(n), "modelo": "Uno", "valor": 1000.0, "cpf": "00000000000", "ano": year})
	r.fails("rejeitar placa duplicada", "vehicle/register",
		map[string]any{"placa": plateA, "modelo": "Gol", "valor": 50000.0, "cpf": cpfA, "ano": year})

	var ipva struct {
		IPVA float64 `json:"ipva"`
	}
	if data := r.ok("calcular IPVA do veículo A", "vehicle/ipva", map[string]any{"placa": plateA}); data != nil {
		_ = json.Unmarshal(data, &ipva)
		r.check("IPVA = 2% de 50000 = 1000", math.Abs(ipva.IPVA-1000) < 0.001, fmt.Sprintf("got %.2f", ipva.IPVA))
	}

	fmt.Println("\n[3] Veículos emplacados em um ano")
	if data := r.ok("consultar veículos de "+fmt.Sprint(year), "vehicle/query/year", map[string]any{"ano": year}); data != nil {
		var vs demoVehicles
		_ = json.Unmarshal(data, &vs)
		found := 0
		for _, v := range vs.Veiculos {
			if v.Placa == plateA || v.Placa == plateB {
				found++
			}
		}
		r.check("lista contém os dois veículos criados", found == 2, fmt.Sprintf("found %d", found))
	}

	fmt.Println("\n[4] Multas")
	r.ok("multa 1 (veículo A, 7 pts)", "fine/register",
		map[string]any{"ano": year, "descricao": "Excesso de velocidade", "pontuacao": 7, "placa": plateA})
	r.ok("multa 2 (veículo A, 3 pts)", "fine/register",
		map[string]any{"ano": year, "descricao": "Estacionamento proibido", "pontuacao": 3, "placa": plateA})
	r.ok("multa 3 (veículo B, 7 pts)", "fine/register",
		map[string]any{"ano": year, "descricao": "Avanço de sinal", "pontuacao": 7, "placa": plateB})
	r.fails("rejeitar multa de placa inexistente", "fine/register",
		map[string]any{"ano": year, "descricao": "Teste", "pontuacao": 1, "placa": "ZZZ" + fmt.Sprint(n)})

	fmt.Println("\n[5] Transferência (multas antigas continuam com o dono da época)")
	r.ok("transferir veículo A para o condutor B", "vehicle/transfer",
		map[string]any{"placa": plateA, "cpf": cpfB})
	r.fails("rejeitar transferência para condutor inexistente", "vehicle/transfer",
		map[string]any{"placa": plateA, "cpf": "00000000000"})
	r.ok("multa 4 (veículo A, já do condutor B, 5 pts, "+fmt.Sprint(nextYear)+")", "fine/register",
		map[string]any{"ano": nextYear, "descricao": "Uso de celular", "pontuacao": 5, "placa": plateA})

	fmt.Println("\n[6] Consultas de multas")
	if data := r.ok("multas do veículo A em "+fmt.Sprint(year), "fines/query/plate",
		map[string]any{"placa": plateA, "ano": year}); data != nil {
		var fs demoFines
		_ = json.Unmarshal(data, &fs)
		r.check("2 multas", fs.Total == 2, fmt.Sprintf("got %d", fs.Total))
		allAna := len(fs.Multas) > 0
		for _, f := range fs.Multas {
			if f.CPF != cpfA || f.Condutor == nil || f.Condutor.Nome != "Ana Demo" {
				allAna = false
			}
		}
		r.check("dados do condutor (Ana Demo) exibidos em cada multa", allAna, "unexpected driver data")
	}
	if data := r.ok("multas do veículo A (todos os anos)", "fines/query/plate",
		map[string]any{"placa": plateA}); data != nil {
		var fs demoFines
		_ = json.Unmarshal(data, &fs)
		r.check("3 multas, 15 pts no total", fs.Total == 3 && fs.PontuacaoTotal == 15,
			fmt.Sprintf("got %d fines / %d pts", fs.Total, fs.PontuacaoTotal))
	}
	if data := r.ok("multas do condutor A em "+fmt.Sprint(year), "fines/query/driver",
		map[string]any{"cpf": cpfA, "ano": year}); data != nil {
		var fs demoFines
		_ = json.Unmarshal(data, &fs)
		r.check("2 multas / 10 pts", fs.Total == 2 && fs.PontuacaoTotal == 10,
			fmt.Sprintf("got %d fines / %d pts", fs.Total, fs.PontuacaoTotal))
	}
	if data := r.ok("multas do condutor B em "+fmt.Sprint(year), "fines/query/driver",
		map[string]any{"cpf": cpfB, "ano": year}); data != nil {
		var fs demoFines
		_ = json.Unmarshal(data, &fs)
		r.check("1 multa (a do veículo B)", fs.Total == 1 && countPlates(fs, plateB) == 1,
			fmt.Sprintf("got %d fines", fs.Total))
	}
	if data := r.ok("multas do condutor B em "+fmt.Sprint(nextYear), "fines/query/driver",
		map[string]any{"cpf": cpfB, "ano": nextYear}); data != nil {
		var fs demoFines
		_ = json.Unmarshal(data, &fs)
		r.check("1 multa (a do veículo A, após a transferência)", fs.Total == 1 && countPlates(fs, plateA) == 1,
			fmt.Sprintf("got %d fines", fs.Total))
	}
	if data := r.ok("multas lançadas em "+fmt.Sprint(year), "fines/query/year",
		map[string]any{"ano": year}); data != nil {
		var fs demoFines
		_ = json.Unmarshal(data, &fs)
		r.check("contém as 3 multas criadas", countPlates(fs, plateA, plateB) == 3,
			fmt.Sprintf("found %d", countPlates(fs, plateA, plateB)))
	}

	fmt.Println("\n[7] Ranking de pontuação")
	if data := r.ok("top 5 condutores por pontuação", "fines/query/points", map[string]any{}); data != nil {
		var rank struct {
			Condutores []struct {
				Posicao int    `json:"posicao"`
				CPF     string `json:"cpf"`
				Nome    string `json:"nome"`
				Pontos  int    `json:"pontos"`
			} `json:"condutores"`
		}
		_ = json.Unmarshal(data, &rank)

		sorted := true
		for i := 1; i < len(rank.Condutores); i++ {
			if rank.Condutores[i].Pontos > rank.Condutores[i-1].Pontos {
				sorted = false
			}
		}
		r.check("no máximo 5 condutores", len(rank.Condutores) >= 1 && len(rank.Condutores) <= 5,
			fmt.Sprintf("got %d", len(rank.Condutores)))
		r.check("ordem decrescente de pontos", sorted, "ranking is not sorted")
		for _, c := range rank.Condutores {
			fmt.Printf("      %d. %-12s CPF %s — %d pts\n", c.Posicao, c.Nome, c.CPF, c.Pontos)
		}
	}

	fmt.Printf("\nResultado: %d verificações OK, %d falhas\n", r.passed, r.failed)
	return r.failed == 0
}
