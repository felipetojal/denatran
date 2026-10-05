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
	ErrReadingFines   = errors.New("unable to read fines")
)

// Fine representa uma multa. O CPF é o do proprietário do veículo
// no momento em que a multa foi lançada (não muda se o veículo for transferido).
type Fine struct {
	ID        int    `json:"id"`
	Ano       int    `json:"ano"`
	Descricao string `json:"descricao"`
	Pontuacao int    `json:"pontuacao"`
	Placa     string `json:"placa"`
	CPF       string `json:"cpf"`
}

// Total de pontos de um condutor (ranking)
type DriverPoints struct {
	CPF    string `json:"cpf"`
	Pontos int    `json:"pontos"`
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

// Inicia a tabela do database se elas ainda não existirem
func (d *Database) init(ctx context.Context) error {
	query := `CREATE TABLE IF NOT EXISTS fines(
		id SERIAL PRIMARY KEY,
		ano INT NOT NULL,
		descricao TEXT NOT NULL,
		pontuacao INT NOT NULL,
		placa VARCHAR(30) NOT NULL,
		cpf VARCHAR(30) NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_fines_placa ON fines(placa);
	CREATE INDEX IF NOT EXISTS idx_fines_cpf ON fines(cpf);
	CREATE INDEX IF NOT EXISTS idx_fines_ano ON fines(ano);`

	if _, err := d.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("%s: %s", ErrInitDb, err)
	}
	return nil
}

// Adiciona uma multa no database e devolve o id gerado
func (d *Database) addFine(ctx context.Context, f Fine) (int, error) {
	query := `INSERT INTO fines (ano, descricao, pontuacao, placa, cpf)
		VALUES ($1, $2, $3, $4, $5) RETURNING id;`

	var id int
	err := d.db.QueryRowContext(ctx, query, f.Ano, f.Descricao, f.Pontuacao, f.Placa, f.CPF).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("unable to insert fine: %w", err)
	}
	return id, nil
}

// Lê as linhas de uma consulta de multas
func scanFines(rows *sql.Rows) ([]Fine, error) {
	defer rows.Close()

	fines := []Fine{}
	for rows.Next() {
		var f Fine
		if err := rows.Scan(&f.ID, &f.Ano, &f.Descricao, &f.Pontuacao, &f.Placa, &f.CPF); err != nil {
			return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
		}
		fines = append(fines, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
	}
	return fines, nil
}

const fineColumns = `id, ano, descricao, pontuacao, placa, cpf`

// Multas de um veículo (ano = 0 significa "todos os anos")
func (d *Database) finesByPlate(ctx context.Context, placa string, ano int) ([]Fine, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+fineColumns+` FROM fines WHERE placa = $1 AND ($2::int = 0 OR ano = $2::int) ORDER BY ano, id;`,
		placa, ano)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
	}
	return scanFines(rows)
}

// Multas de um condutor (ano = 0 significa "todos os anos")
func (d *Database) finesByDriver(ctx context.Context, cpf string, ano int) ([]Fine, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+fineColumns+` FROM fines WHERE cpf = $1 AND ($2::int = 0 OR ano = $2::int) ORDER BY ano, id;`,
		cpf, ano)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
	}
	return scanFines(rows)
}

// Multas lançadas em um ano
func (d *Database) finesByYear(ctx context.Context, ano int) ([]Fine, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+fineColumns+` FROM fines WHERE ano = $1 ORDER BY id;`, ano)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
	}
	return scanFines(rows)
}

// Condutores com maior pontuação total, em ordem decrescente
func (d *Database) topDrivers(ctx context.Context, limit int) ([]DriverPoints, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT cpf, SUM(pontuacao) AS pontos FROM fines
		 GROUP BY cpf ORDER BY pontos DESC, cpf ASC LIMIT $1;`, limit)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
	}
	defer rows.Close()

	top := []DriverPoints{}
	for rows.Next() {
		var dp DriverPoints
		if err := rows.Scan(&dp.CPF, &dp.Pontos); err != nil {
			return nil, fmt.Errorf("%s: %w", ErrReadingFines, err)
		}
		top = append(top, dp)
	}
	return top, rows.Err()
}
