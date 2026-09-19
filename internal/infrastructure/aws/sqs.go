package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	appconfig "github.com/joaoluvisari/backend-challenge-go/internal/infrastructure/config"
)

// NewSQSClient cria e retorna o client do SQS configurado de acordo com o appconfig.
func NewSQSClient(cfg *appconfig.Config) (*sqs.Client, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.SQS.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("falha ao carregar configuracao da AWS: %w", err)
	}

	client := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.SQS.EndpointURL != "" {
			o.BaseEndpoint = aws.String(cfg.SQS.EndpointURL)
		}
	})

	return client, nil
}
