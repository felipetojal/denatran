# Denatran

É um sistema de para gerenciar veículos e condutores presentes em todos os estados do país.

## Funcionalidades

Um cliente tera as seguintes funcionalidades:

* Emplacar veículo (`vehicle/register`): Insere placa, modelo, valor e CPF do condutor.
* Calcular IPVA do veículo (`vehicle/ipva`): Insere a placa do veículo e recebe o IPVA.
* Transferir proprietário do veículo (`vehicle/transfer`): Insere placa do veículo, CPF do novo dono.
* Cadastrar condutor (`driver/register`): Insere CPF e nome do novo condutor.
* Lançar multa (`fine/register`): Insere ano, descrição, pontuação e placa do veículo.
* Informar veículos emplacados em um ano (`vehicle/query/year`): Insere o ano e recebe a quantidade de veículos cadastrado nesse ano.
* Informar multas por um veículo no ano (`fines/query/plate`): Insere a placa do veículo e o ano e recebe os dados do condutor que levou a multa.
* Informar as multas de um condutor no ano (`fines/query/driver`): Insere o CPF do condutor e o ano.
* Informar, em ordem decrescente, os 5 condutores com maior pontuação de multas (`fines/query/points`): retorna os dados dos condutores.  

## Serviços
