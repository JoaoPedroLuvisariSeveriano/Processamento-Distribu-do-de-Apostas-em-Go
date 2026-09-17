// Package main e o ponto de entrada da aplicacao.
// Aqui inicializamos o Uber Fx, que e o nosso container de injecao de
// dependencias. O Fx cuida de:
//   - Instanciar todos os componentes (config, db, http, workers) na ordem certa
//   - Gerenciar o ciclo de vida (OnStart / OnStop) de cada componente
//   - Fazer o shutdown graceful quando receber SIGTERM/SIGINT
//
// Nada de logica de negocio aqui: este arquivo apenas monta o grafo de
// dependencias e delega tudo ao Fx.
package main

import (
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func main() {
	// fx.New constroi o grafo de dependencias declarado nos modulos.
	// Cada fx.Module agrupa providers relacionados por camada (infra, app, domain).
	//
	// A ordem de execucao do Fx:
	//   1. Resolucao do grafo de dependencias (compile-time-like)
	//   2. Chamada dos construtores na ordem topologica
	//   3. Execucao dos hooks OnStart (servidor HTTP, workers SQS, etc.)
	//   4. Aguarda sinal de shutdown (SIGTERM / SIGINT / fx.ShutdownSignal)
	//   5. Execucao dos hooks OnStop em ordem reversa (graceful shutdown)
	app := fx.New(
		// TODO (Passo 2+): adicionar modulos conforme implementacao avanca
		// fx.Module("config", config.Module),
		// fx.Module("infra",  infra.Module),
		// fx.Module("app",    application.Module),
		// fx.Module("http",   httpserver.Module),
		// fx.Module("worker", worker.Module),

		// Logger padrao do Fx (sera substituido pelo zap estruturado nos proximos passos)
		fx.WithLogger(func() fxevent.Logger {
			return &fxevent.ZapLogger{Logger: zap.NewExample()}
		}),
	)

	// Run bloqueia ate receber um sinal de shutdown.
	// Internamente, o Fx registra handlers para SIGINT e SIGTERM.
	app.Run()
}
