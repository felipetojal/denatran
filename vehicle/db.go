package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

// Erros globais
var (
	ErrConnectingToDb  = errors.New("unable to connect to database")
	ErrPingToDb        = errors.New("unable to ping database")
	ErrInitDb          = errors.New("unable to init database")
	ErrVehicleExists   = errors.New("vehicle already registered")
	ErrVehicleNotFound = errors.New("vehicle not found")
)

// Vehicle representa o modelo de dados do veículo
type Vehicle struct {
	Placa           string  `json:"placa"`
	Modelo          string  `json:"modelo"`
	Valor           float64 `json:"valor"`
	CPF             string  `json:"cpf"` // CPF do proprietário atual
	AnoEmplacamento int     `json:"ano_emplacamento"`
}

// Encapsulando a conexão para o database
type Database struct {
	db *sql.DB
}

// Construtor para o Database
func newDatabase(port, host, user, password, dbname string) (*Database, error) {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", ErrConnectingToDb, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %s", ErrPingToDb, err)
	}

	return &Database{db: db}, nil
}

// Tenta conectar ao banco várias vezes (o Postgres pode demorar a ficar pronto)
func connectWithRetry(port, host, user, password, dbname string) *Database {
	for attempt := 1; ; attempt++ {
		d, err := newDatabase(port, host, user, password, dbname)
		if err == nil {
			return d
		}
		if attempt >= 30 {
			log.Fatalf("Failed to connect to DB: %v", err)
		}
		log.Printf("Database not ready (attempt %d/30): %v", attempt, err)
		time.Sleep(2 * time.Second)
	}
}

// Inicia a tabela do database se ela ainda não existir
func (d *Database) init(ctx context.Context) error {
	query := `CREATE TABLE IF NOT EXISTS vehicles(
		placa VARCHAR(20) PRIMARY KEY,
		modelo VARCHAR(100) NOT NULL,
		valor NUMERIC(14,2) NOT NULL,
		cpf VARCHAR(30) NOT NULL,
		ano_emplacamento INT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_vehicles_ano ON vehicles(ano_emplacamento);`

	if _, err := d.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("%s: %s", ErrInitDb, err)
	}
	return nil
}

// Emplaca (insere) um veículo
func (d *Database) addVehicle(ctx context.Context, v Vehicle) error {
	query := `INSERT INTO vehicles (placa, modelo, valor, cpf, ano_emplacamento)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (placa) DO NOTHING;`

	res, err := d.db.ExecContext(ctx, query, v.Placa, v.Modelo, v.Valor, v.CPF, v.AnoEmplacamento)
	if err != nil {
		return fmt.Errorf("unable to insert vehicle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: placa %s", ErrVehicleExists, v.Placa)
	}
	return nil
}

// Busca um veículo pela placa
func (d *Database) getVehicle(ctx context.Context, placa string) (*Vehicle, error) {
	query := `SELECT placa, modelo, valor, cpf, ano_emplacamento FROM vehicles WHERE placa = $1;`

	var v Vehicle
	err := d.db.QueryRowContext(ctx, query, placa).
		Scan(&v.Placa, &v.Modelo, &v.Valor, &v.CPF, &v.AnoEmplacamento)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: placa %s", ErrVehicleNotFound, placa)
		}
		return nil, fmt.Errorf("unable to read vehicle: %w", err)
	}
	return &v, nil
}

// Transfere a propriedade do veículo para outro CPF
func (d *Database) transferVehicle(ctx context.Context, placa, novoCPF string) error {
	res, err := d.db.ExecContext(ctx, `UPDATE vehicles SET cpf = $1 WHERE placa = $2;`, novoCPF, placa)
	if err != nil {
		return fmt.Errorf("unable to transfer vehicle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: placa %s", ErrVehicleNotFound, placa)
	}
	return nil
}

// Lista os veículos emplacados em um ano
func (d *Database) listVehiclesByYear(ctx context.Context, ano int) ([]Vehicle, error) {
	query := `SELECT placa, modelo, valor, cpf, ano_emplacamento
		FROM vehicles WHERE ano_emplacamento = $1 ORDER BY placa;`

	rows, err := d.db.QueryContext(ctx, query, ano)
	if err != nil {
		return nil, fmt.Errorf("unable to list vehicles: %w", err)
	}
	defer rows.Close()

	vehicles := []Vehicle{}
	for rows.Next() {
		var v Vehicle
		if err := rows.Scan(&v.Placa, &v.Modelo, &v.Valor, &v.CPF, &v.AnoEmplacamento); err != nil {
			return nil, fmt.Errorf("unable to read vehicle: %w", err)
		}
		vehicles = append(vehicles, v)
	}
	return vehicles, rows.Err()
}
