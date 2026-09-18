# Arquitetura do Serviço de Processamento de Apostas

Este documento detalha as decisões arquiteturais fundamentais adotadas no serviço de processamento de apostas (Wagering API), desenhado para suportar alta concorrência e garantir integridade financeira absoluta.

## 1. Precisão Monetária (Int64 / Decimal)

**Decisão:** Proibição do uso de tipos de ponto flutuante (`float32`/`float64`) para representação de valores monetários.

**Justificativa:** Ponto flutuante sofre de imprecisão na representação binária (ex: `0.1 + 0.2 = 0.30000000000000004`). Em sistemas financeiros, isso causa perda de centavos que, em escala, resultam em rombos financeiros inauditáveis. 
No domínio, modelamos o `Money` como um **Value Object**, utilizando a biblioteca `shopspring/decimal` (ou representação em centavos via `int64` puro). Isso garante que toda operação de soma, subtração e validação (como a de saldo suficiente) seja matematicamente exata e segura contra arredondamentos indesejados.

## 2. Lock Pessimista no PostgreSQL

**Decisão:** Uso da cláusula `SELECT ... FOR UPDATE` ao buscar a carteira (`Wallet`) durante o processamento de apostas.

**Justificativa:** Alta concorrência significa que um mesmo jogador pode tentar realizar apostas simultâneas em milissegundos. Se usássemos uma abordagem ingênua ou puramente otimista (sem controle de versão forte no banco de dados), correríamos o risco do *Lost Update*: duas requisições leem saldo `100`, debitam `50`, e ambas atualizam para `50` (o saldo final deveria ser `0`).
Ao aplicar o **Lock Pessimista**, a primeira transação que inicia o `FindByIDWithLock` "trava" a linha da carteira no PostgreSQL. A segunda transação é forçada a aguardar até que a primeira faça o *Commit* ou *Rollback*. Isso serializa operações na mesma carteira, garantindo integridade de saldo (a invariante `balance >= 0` é preservada a nível de banco) sem bloquear operações de outros jogadores.

## 3. Padrão Transactional Outbox

**Decisão:** Criação de uma tabela `outbox_events` e atualização na mesma transação atômica das operações financeiras.

**Justificativa:** Precisamos notificar sistemas externos (via SQS) que uma aposta foi processada (para fins de analytics, gamificação, etc). Uma abordagem ingênua seria "atualizar o banco e depois enviar para o SQS", mas se o envio falhar (ou o pod morrer no meio do processo), o sistema entra em um estado inconsistente (Dual Write problem).
Com o **Transactional Outbox**, persistimos o evento na tabela `outbox_events` *junto* com o saldo e a transação de aposta (`WagerTransaction`). Sendo atômico: ou salva tudo ou não salva nada.
Um **Worker em Background** varre a tabela em batches utilizando `SELECT FOR UPDATE SKIP LOCKED` (para paralelismo horizontal seguro), envia as mensagens para o SQS e só as marca como concluídas caso a AWS responda com sucesso.

## 4. Injeção de Dependências com Uber Fx

**Decisão:** Uso do framework **Uber Fx** para orquestrar dependências e ciclos de vida.

**Justificativa:** À medida que o projeto escalou para DDD (Domain-Driven Design), dividindo-se entre `infrastructure`, `application` (ports/usecases) e `presentation`, o grafo de dependências se tornou complexo.
O Uber Fx resolve isso de forma elegante:
- **Modularidade:** `fx.Provide` injeta as abstrações do banco e middlewares nos Handlers e Casos de Uso.
- **Graceful Shutdown:** Através do `fx.Lifecycle`, pudemos gerenciar o encerramento da aplicação limpamente. Hooks `OnStop` dão tempo para o servidor HTTP (`chi`) drenar requests ativos e para os workers (SQS Consumer, Outbox Publisher) finalizarem suas goroutines antes que a conexão primária com o Postgres seja destruída.
