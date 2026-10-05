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
	ErrConnectingToDb = errors.New("unable to connect to database")
	ErrPingToDb       = errors.New("unable to ping database")
	ErrInitDb         = errors.New("unable to init database")
	ErrReadingDriver  = errors.New("unable to read driver")
	ErrDriverExists   = errors.New("driver already registered")
	ErrDriverNotFound = errors.New("driver not found")
)

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

	return &Database{
		db: db,
	}, nil
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

// Inicia a tabela do database se elas ainda não existirem
func (d *Database) init(ctx context.Context) error {
	query := `CREATE TABLE IF NOT EXISTS drivers(
    	id SERIAL PRIMARY KEY,
    	cpf VARCHAR(30) UNIQUE NOT NULL,
    	nome VARCHAR(100) NOT NULL
	);`

	_, err := d.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("%s: %s", ErrInitDb, err)
	}

	return nil
}

// Adiciona um novo condutor ao banco de dados
func (d *Database) addDriver(ctx context.Context, cpf, nome string) error {
	query := `INSERT INTO drivers (cpf, nome) VALUES ($1, $2) ON CONFLICT (cpf) DO NOTHING;`

	res, err := d.db.ExecContext(ctx, query, cpf, nome)
	if err != nil {
		return fmt.Errorf("unable to insert driver: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: cpf %s", ErrDriverExists, cpf)
	}

	return nil
}

// Driver representa o modelo de dados do condutor
type Driver struct {
	ID   int    `json:"id"`
	CPF  string `json:"cpf"`
	Nome string `json:"nome"`
}

// Busca um condutor no banco de dados utilizando o CPF
func (d *Database) getDriverByCPF(ctx context.Context, cpf string) (*Driver, error) {
	query := `SELECT id, cpf, nome FROM drivers WHERE cpf = $1;`

	var driver Driver
	err := d.db.QueryRowContext(ctx, query, cpf).Scan(&driver.ID, &driver.CPF, &driver.Nome)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: cpf %s", ErrDriverNotFound, cpf)
		}
		return nil, fmt.Errorf("%s: %w", ErrReadingDriver, err)
	}

	return &driver, nil
}
