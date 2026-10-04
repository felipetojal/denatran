package driver

import "database/sql"

type Database struct {
	port string
	db *sql.DB
}

func newDatabase(port string) (*Database) {
	sql.Open()
}