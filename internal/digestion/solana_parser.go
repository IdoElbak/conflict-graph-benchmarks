package digestion

import (
	"encoding/json"
	"fmt"
)

const (
	SolanaRawFilePath   = "internal/digestion/data/Solana_raw_391000000_391000998.json"
	SolanaCleanFilePath = "internal/digestion/data/Solana_cleaned_blocks_391000000_391000998.json"
)

// --- Raw Schema Definitions ---

type SolanaBlockRecord struct {
	BlockNumber int64
	BlockData   SolanaBlockData
}

type SolanaBlockData struct {
	Result struct {
		Transactions []SolanaTx `json:"transactions"`
	} `json:"result"`
}

type SolanaTx struct {
	Transaction struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			AccountKeys []SolanaAccountKey `json:"accountKeys"`
		} `json:"message"`
	} `json:"transaction"`
}

type SolanaAccountKey struct {
	Pubkey   string `json:"pubkey"`
	Writable bool   `json:"writable"`
}

// UnmarshalJSON extracts the [BlockNum, BlockData] array into the struct
func (s *SolanaBlockRecord) UnmarshalJSON(data []byte) error {
	var tuple []json.RawMessage
	err := json.Unmarshal(data, &tuple)
	if err != nil {
		return err
	}
	if len(tuple) < 2 {
		return nil // skip invalid or incomplete tuples
	}
	_ = json.Unmarshal(tuple[0], &s.BlockNumber)
	_ = json.Unmarshal(tuple[1], &s.BlockData)
	return nil
}

// --- Parser Logic ---

// ParseSolanaStream orchestrates the digestion of a raw Solana trace
func ParseSolanaStream() error {
	inFile, outFile, err := openFiles(SolanaRawFilePath, SolanaCleanFilePath)
	if err != nil {
		return err
	}
	defer inFile.Close()
	defer outFile.Close()

	decoder := json.NewDecoder(inFile)
	encoder := json.NewEncoder(outFile)

	// Consume the root JSON array opening bracket '['
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("failed to read opening token: %w", err)
	}

	if _, err := outFile.WriteString("[\n"); err != nil {
		return err
	}

	isFirst := true

	for decoder.More() {
		var rawBlock SolanaBlockRecord
		if err := decoder.Decode(&rawBlock); err != nil {
			continue // Skip malformed blocks
		}

		cleanBlock := processSolanaBlock(rawBlock)

		if !isFirst {
			if _, err := outFile.WriteString(",\n"); err != nil {
				return err
			}
		}

		if err := encoder.Encode(cleanBlock); err != nil {
			return err
		}
		isFirst = false
	}

	_, err = outFile.WriteString("]\n")
	return err
}

// processSolanaBlock converts a raw Solana block into a CleanedBlock
func processSolanaBlock(rawBlock SolanaBlockRecord) CleanedBlock {
	txns := rawBlock.BlockData.Result.Transactions
	return CleanedBlock{
		BlockNumber:     rawBlock.BlockNumber,
		NumTransactions: len(txns),
		Transactions:    extractSolanaTransactions(txns),
	}
}

// extractSolanaTransactions maps a slice of Solana transactions to CleanedTransactions
func extractSolanaTransactions(txns []SolanaTx) []CleanedTransaction {
	cleaned := make([]CleanedTransaction, 0, len(txns))
	for _, tx := range txns {
		if len(tx.Transaction.Signatures) == 0 {
			continue // Skip incomplete transactions
		}
		cleaned = append(cleaned, parseSolanaTx(tx))
	}
	return cleaned
}

// parseSolanaTx evaluates the writable flags on account keys to map Read/Write sets
func parseSolanaTx(tx SolanaTx) CleanedTransaction {
	cleanTx := CleanedTransaction{
		ID:       tx.Transaction.Signatures[0],
		ReadSet:  make([]string, 0),
		WriteSet: make([]string, 0),
	}

	for _, account := range tx.Transaction.Message.AccountKeys {
		if account.Writable {
			cleanTx.WriteSet = append(cleanTx.WriteSet, account.Pubkey)
		} else {
			cleanTx.ReadSet = append(cleanTx.ReadSet, account.Pubkey)
		}
	}

	return cleanTx
}
