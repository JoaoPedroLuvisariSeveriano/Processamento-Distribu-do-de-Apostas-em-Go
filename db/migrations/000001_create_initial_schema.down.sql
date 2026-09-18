-- =============================================================================
-- Migration: 000001_create_initial_schema.down.sql
-- Reversao do schema inicial.
-- ATENCAO: Esta migration destroi todos os dados.
-- Use apenas em ambiente de desenvolvimento ou testes.
-- =============================================================================

-- Remover triggers primeiro (dependem das funcoes)
DROP TRIGGER IF EXISTS trg_prevent_ledger_delete ON wallet_ledger_entries;
DROP TRIGGER IF EXISTS trg_prevent_ledger_update ON wallet_ledger_entries;
DROP FUNCTION IF EXISTS prevent_ledger_modification();

-- Remover tabelas em ordem inversa de dependencia (FK constraints)
DROP TABLE IF EXISTS inbox_messages;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS wallet_ledger_entries;
DROP TABLE IF EXISTS wager_transactions;
DROP TABLE IF EXISTS wallets;
