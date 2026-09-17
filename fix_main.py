import os

# Corrigir o import do fxevent que estava errado no main.go
f = open('cmd/server/main.go', 'w', encoding='utf-8')
f.write("""// Package main e o ponto de entrada da aplicacao.
//
// Aqui inicializamos o Uber Fx, que e o nosso container de injecao de
// dependencias. O Fx cuida de:
//   - Instanciar todos os componentes (config, db, http, workers) na ordem certa
//   - Gerenciar o ciclo de vida (OnStart/OnStop) de cada componente
//   - Fazer o shutdown graceful quando receber SIGTERM/SIGINT
//
// Nenhuma logica de negocio aqui: este arquivo apenas monta o grafo de
// dependencias e delega tudo ao Fx.
package main

import (
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

func main() {
	// fx.New constroi o grafo de dependencias declarado nos modulos.
	//
	// Ordem de execucao do Fx:
	//   1. Resolucao do grafo de deps (verificacao estatica antes de rodar)
	//   2. Chamada dos construtores na ordem topologica
	//   3. Execucao dos hooks OnStart (HTTP server, workers SQS, outbox worker)
	//   4. Aguarda sinal de shutdown (SIGTERM / SIGINT)
	//   5. Execucao dos hooks OnStop em ordem REVERSA (graceful shutdown)
	app := fx.New(
		// TODO (Passos futuros): adicionar modulos conforme implementacao avanca.
		// Cada modulo sera adicionado aqui na ordem correta de dependencia:
		//
		// fx.Module("config",  config.Module),      -- carrega .env e valida vars
		// fx.Module("infra",   infra.Module),        -- db pool, sqs client, logger
		// fx.Module("domain",  domain.Module),       -- repositorios
		// fx.Module("app",     application.Module),  -- casos de uso
		// fx.Module("http",    httpserver.Module),   -- chi router + handlers
		// fx.Module("worker",  worker.Module),       -- sqs consumer + outbox worker

		// Configurar logger do Fx para usar zap estruturado (JSON).
		// Em producao, zap.NewProduction() emite logs JSON.
		fx.WithLogger(func() fxevent.Logger {
			logger, _ := zap.NewDevelopment()
			return &fxevent.ZapLogger{Logger: logger}
		}),
	)

	// Run bloqueia ate receber SIGTERM/SIGINT.
	// O Fx automaticamente registra handlers de sinal do SO.
	app.Run()
}
""")
f.close()
print('cmd/server/main.go reescrito corretamente')
