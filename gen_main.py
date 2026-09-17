import os

# cmd/server/main.go
os.makedirs('cmd/server', exist_ok=True)
f = open('cmd/server/main.go', 'w', encoding='utf-8')
f.write("""// Package main e o ponto de entrada da aplicacao.
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
\t"go.uber.org/fx"
\t"go.uber.org/zap"
)

func main() {
\t// fx.New constroi o grafo de dependencias declarado nos modulos.
\t// Cada fx.Module agrupa providers relacionados por camada (infra, app, domain).
\t//
\t// A ordem de execucao do Fx:
\t//   1. Resolucao do grafo de dependencias (compile-time-like)
\t//   2. Chamada dos construtores na ordem topologica
\t//   3. Execucao dos hooks OnStart (servidor HTTP, workers SQS, etc.)
\t//   4. Aguarda sinal de shutdown (SIGTERM / SIGINT / fx.ShutdownSignal)
\t//   5. Execucao dos hooks OnStop em ordem reversa (graceful shutdown)
\tapp := fx.New(
\t\t// TODO (Passo 2+): adicionar modulos conforme implementacao avanca
\t\t// fx.Module("config", config.Module),
\t\t// fx.Module("infra",  infra.Module),
\t\t// fx.Module("app",    application.Module),
\t\t// fx.Module("http",   httpserver.Module),
\t\t// fx.Module("worker", worker.Module),

\t\t// Logger padrao do Fx (sera substituido pelo zap estruturado nos proximos passos)
\t\tfx.WithLogger(func() fxevent.Logger {
\t\t\treturn &fxevent.ZapLogger{Logger: zap.NewExample()}
\t\t}),
\t)

\t// Run bloqueia ate receber um sinal de shutdown.
\t// Internamente, o Fx registra handlers para SIGINT e SIGTERM.
\tapp.Run()
}
""")
f.close()
print('cmd/server/main.go criado')
