package worker

import (
	"context"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	appconfig "github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
)

type OutboxPublisher struct {
	client     *sqs.Client
	queueURL   string
	outboxRepo port.OutboxRepository
	runInTx    port.RunInTxFunc
	log        *zap.Logger
	wg         sync.WaitGroup
	ctx        context.Context
	cancelFunc context.CancelFunc
}

func NewOutboxPublisher(
	lc fx.Lifecycle,
	cfg *appconfig.Config,
	outboxRepo port.OutboxRepository,
	runInTx port.RunInTxFunc,
	log *zap.Logger,
) (*OutboxPublisher, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.SQS.Region),
	)
	if err != nil {
		return nil, err
	}

	client := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.SQS.EndpointURL != "" {
			o.BaseEndpoint = aws.String(cfg.SQS.EndpointURL)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())

	publisher := &OutboxPublisher{
		client:     client,
		queueURL:   cfg.SQS.WagerEventsURL,
		outboxRepo: outboxRepo,
		runInTx:    runInTx,
		log:        log,
		ctx:        ctx,
		cancelFunc: cancel,
	}

	lc.Append(fx.Hook{
		OnStart: func(c context.Context) error {
			if publisher.queueURL == "" {
				log.Warn("SQS WagerEventsURL not set, skipping outbox publisher")
				return nil
			}
			publisher.wg.Add(1)
			go publisher.start()
			return nil
		},
		OnStop: func(c context.Context) error {
			publisher.log.Info("Gracefully shutting down Outbox Publisher...")
			publisher.cancelFunc()
			publisher.wg.Wait()
			return nil
		},
	})

	return publisher, nil
}

func (p *OutboxPublisher) start() {
	defer p.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.publishPendingEvents()
		}
	}
}

func (p *OutboxPublisher) publishPendingEvents() {
	err := p.runInTx(p.ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Fetch limits using SELECT FOR UPDATE SKIP LOCKED
		events, err := p.outboxRepo.FetchPendingForUpdate(ctx, tx, 50)
		if err != nil {
			return err
		}

		for _, event := range events {
			// Enviar pro SQS
			_, sqsErr := p.client.SendMessage(ctx, &sqs.SendMessageInput{
				QueueUrl:    aws.String(p.queueURL),
				MessageBody: aws.String(string(event.Payload())),
			})

			if sqsErr != nil {
				p.log.Error("Failed to publish outbox event", zap.Error(sqsErr), zap.String("eventId", event.ID().String()))
				if recErr := p.outboxRepo.RecordFailure(ctx, tx, event.ID(), event.Attempts()+1, time.Now().Add(5*time.Second)); recErr != nil {
					p.log.Error("Failed to record outbox failure", zap.Error(recErr))
				}
				continue
			}

			// Sucesso
			if markErr := p.outboxRepo.MarkAsPublished(ctx, tx, event.ID(), time.Now()); markErr != nil {
				p.log.Error("Failed to mark outbox as published", zap.Error(markErr))
			}
		}

		return nil
	})

	if err != nil {
		p.log.Error("Outbox publisher transaction failed", zap.Error(err))
	}
}
