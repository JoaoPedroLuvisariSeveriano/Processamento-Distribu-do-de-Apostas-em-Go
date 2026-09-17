import os

# scripts/init-localstack.sh
os.makedirs('scripts', exist_ok=True)
f = open('scripts/init-localstack.sh', 'w', encoding='utf-8', newline='\n')
f.write("""#!/bin/bash
# =============================================================================
# init-localstack.sh - Provisionamento das filas SQS no LocalStack
# =============================================================================
# Este script e executado automaticamente pelo LocalStack quando o container
# termina de inicializar (diretorio /etc/localstack/init/ready.d/).
#
# Criamos tres filas FIFO:
#   1. wager-transactions-dlq.fifo : Dead Letter Queue (criada PRIMEIRO)
#   2. wager-transactions.fifo     : Fila principal com redrive para a DLQ
#   3. wager-events.fifo           : Fila de saida dos eventos da outbox
#
# MessageGroupId garante ordenacao dentro do grupo.
# MessageDeduplicationId previne duplicatas pelo SQS (defesa extra).
# =============================================================================

set -e

REGION="us-east-1"
ENDPOINT="http://localhost:4566"
ACCOUNT_ID="000000000000"

echo "Criando filas SQS FIFO no LocalStack..."

# 1. Criar a DLQ primeiro (a fila principal referencia ela no redrive policy)
awslocal sqs create-queue \\
  --queue-name wager-transactions-dlq.fifo \\
  --attributes FifoQueue=true,ContentBasedDeduplication=false \\
  --region 

echo "Criada: wager-transactions-dlq.fifo"

# 2. Fila principal com redrive policy apontando para a DLQ
# maxReceiveCount=5: apos 5 falhas, a mensagem vai para a DLQ
DLQ_ARN="arn:aws:sqs:::wager-transactions-dlq.fifo"

awslocal sqs create-queue \\
  --queue-name wager-transactions.fifo \\
  --attributes \\
    FifoQueue=true \\
    ContentBasedDeduplication=false \\
    VisibilityTimeout=30 \\
    "RedrivePolicy={\\\"deadLetterTargetArn\\\":\\\"\\\",\\\"maxReceiveCount\\\":5}" \\
  --region 

echo "Criada: wager-transactions.fifo (com redrive para DLQ)"

# 3. Fila de saida dos eventos da outbox
awslocal sqs create-queue \\
  --queue-name wager-events.fifo \\
  --attributes FifoQueue=true,ContentBasedDeduplication=false \\
  --region 

echo "Criada: wager-events.fifo"
echo "Provisionamento SQS concluido com sucesso!"
""")
f.close()
print('scripts/init-localstack.sh criado')
