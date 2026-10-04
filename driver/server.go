package driver

// Estrutura do servidor
type Server struct {
	addr string
	port string
	db *Database
}

// Construtor para o Server
func newServer(addr, port string, db *Database) (*Server) {
	return &Server{
		addr: addr,
		port: port,
		db: db,
	}
}