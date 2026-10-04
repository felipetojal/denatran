package driver

var (
	DriverTopic = "driver/register"
)


// Esse é o payload que será enviado/recebido no tópico "driver/register"
type Payload struct {
	Cpf  string `json:"cpf"`
	Nome string `json:"nome"`
}
