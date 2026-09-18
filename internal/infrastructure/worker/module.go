package worker

import (
	"go.uber.org/fx"
)

// Module reune todos os workers assincronos.
var Module = fx.Options(
	fx.Provide(
		NewSQSConsumer,
		NewOutboxPublisher,
		NewPendingReferencesWorker,
	),
	fx.Invoke(
		// O fx.Invoke forca o Fx a instanciar estes componentes durante o startup.
		// Isso executa os construtores e pendura os hooks no fx.Lifecycle (OnStart/OnStop).
		func(c *SQSConsumer) {},
		func(p *OutboxPublisher) {},
		func(w *PendingReferencesWorker) {},
	),
)
