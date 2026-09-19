package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/joaoluvisari/backend-challenge-go/internal/application/port"
	"github.com/joaoluvisari/backend-challenge-go/internal/application/usecase"
	domaininbox "github.com/joaoluvisari/backend-challenge-go/internal/domain/inbox"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/money"
	"github.com/joaoluvisari/backend-challenge-go/internal/domain/transaction"
	appconfig "github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
	"github.com/joaoluvisari/backend-challenge-go/internal/presentation/http/handler" // para reaproveitar o ProcessWagerRequest
)

type SQSConsumer struct {
	client     *sqs.Client
	queueURL   string
	uc         *usecase.ProcessWagerUseCase
	inboxRepo  port.InboxRepository
	runInTx    port.RunInTxFunc
	log        *zap.Logger
	wg         sync.WaitGroup
	ctx        context.Context
	cancelFunc context.CancelFunc
}

func NewSQSConsumer(
	lc fx.Lifecycle,
	cfg *appconfig.Config,
	uc *usecase.ProcessWagerUseCase,
	inboxRepo port.InboxRepository,
	runInTx port.RunInTxFunc,
	client *sqs.Client,
	log *zap.Logger,
) (*SQSConsumer, error) {

	ctx, cancel := context.WithCancel(context.Background())

	consumer := &SQSConsumer{
		client:     client,
		queueURL:   cfg.SQS.WagerTransactionsURL,
		uc:         uc,
		inboxRepo:  inboxRepo,
		runInTx:    runInTx,
		log:        log,
		ctx:        ctx,
		cancelFunc: cancel,
	}

	lc.Append(fx.Hook{
		OnStart: func(c context.Context) error {
			if consumer.queueURL == "" {
				log.Warn("SQS WagerTransactionsURL not set, skipping consumer")
				return nil
			}
			consumer.wg.Add(1)
			go consumer.start()
			return nil
		},
		OnStop: func(c context.Context) error {
			consumer.log.Info("Gracefully shutting down SQS consumer...")
			consumer.cancelFunc()
			consumer.wg.Wait()
			return nil
		},
	})

	return consumer, nil
}

func (c *SQSConsumer) start() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			// Continuar
		}

		out, err := c.client.ReceiveMessage(c.ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20, // Long polling
		})

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			c.log.Error("Failed to receive messages from SQS", zap.Error(err))
			time.Sleep(5 * time.Second) // backoff
			continue
		}

		for _, msg := range out.Messages {
			c.wg.Add(1)
			go func(m types.Message) {
				defer c.wg.Done()
				c.processMessage(m)
			}(msg)
		}
	}
}

func (c *SQSConsumer) processMessage(msg types.Message) {
	if msg.Body == nil || msg.MessageId == nil {
		return
	}

	messageID := *msg.MessageId
	body := *msg.Body
	
	hashBytes := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(hashBytes[:])

	inboxMsg := domaininbox.NewInboxMessage("wager_consumer", messageID, hash)

	// 1. Inbox Deduplication
	inserted := false
	err := c.runInTx(c.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var innerErr error
		inserted, innerErr = c.inboxRepo.TryInsert(ctx, tx, inboxMsg)
		return innerErr
	})

	if err != nil {
		c.log.Error("Failed to access inbox", zap.Error(err), zap.String("messageId", messageID))
		return // Nao deleta, SQS fara retry (ate maxReceiveCount -> DLQ)
	}

	if !inserted {
		c.log.Info("Duplicate message ignored", zap.String("messageId", messageID))
		c.deleteMessage(msg)
		return
	}

	// 2. Parser Payload
	var req handler.ProcessWagerRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		c.log.Error("Invalid JSON payload", zap.Error(err), zap.String("messageId", messageID))
		c.deleteMessage(msg)
		return
	}

	// 3. Monta Use Case Input
	playerID, err := uuid.Parse(req.PlayerID)
	if err != nil {
		c.deleteMessage(msg)
		return
	}
	walletID, err := uuid.Parse(req.WalletID)
	if err != nil {
		c.deleteMessage(msg)
		return
	}
	corrID, err := uuid.Parse(req.CorrelationID)
	if err != nil {
		corrID = uuid.New()
	}
	amount, err := money.Parse(req.Amount, req.Currency)
	if err != nil {
		c.deleteMessage(msg)
		return
	}

	providerID := "sqs_provider"

	input := usecase.ProcessWagerInput{
		ExternalTransactionID: req.TransactionID,
		ProviderID:            providerID,
		IdempotencyKey:        req.IdempotencyKey,
		PlayerID:              playerID,
		WalletID:              walletID,
		RoundID:               req.RoundID,
		GameID:                req.GameID,
		Kind:                  transaction.Kind(req.Kind),
		Amount:                amount,
		ReferenceExternalID:   req.ReferenceExternalID,
		CorrelationID:         corrID,
	}

	// 4. Executar Caso de Uso
	_, err = c.uc.Execute(c.ctx, input)
	if err != nil {
		if errors.Is(err, usecase.ErrIdempotencyConflict) || errors.Is(err, usecase.ErrInvalidOpeningKind) || errors.Is(err, usecase.ErrWalletOwnerMismatch) {
			c.log.Warn("Business error processing message, discarding", zap.Error(err), zap.String("messageId", messageID))
			c.deleteMessage(msg)
			return
		}
		
		c.log.Error("Temporary error processing message, leaving in queue", zap.Error(err), zap.String("messageId", messageID))
		return
	}

	c.deleteMessage(msg)
}

func (c *SQSConsumer) deleteMessage(msg types.Message) {
	_, err := c.client.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	if err != nil {
		c.log.Error("Failed to delete message", zap.Error(err), zap.String("messageId", *msg.MessageId))
	}
}
