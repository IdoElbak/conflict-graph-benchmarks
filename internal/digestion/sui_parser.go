package digestion

import (
	"encoding/json"
	"fmt"
)

const (
	SuiRawFilePath   = "internal/digestion/data/Sui_raw_150001000_150002999.json"
	SuiCleanFilePath = "internal/digestion/data/Sui_cleaned_blocks_150001000_150002999.json"
)

// --- Raw Schema Definitions ---

// SuiCheckpointRecord is one [checkpoint number, checkpoint summary, transactions] tuple.
type SuiCheckpointRecord struct {
	CheckpointNumber int64
	Transactions     []SuiTx
}

// SuiTx keeps only the fields that define a transaction's read and write sets.
type SuiTx struct {
	Digest      string `json:"digest"`
	Transaction struct {
		Data struct {
			Transaction struct {
				Inputs []SuiInput `json:"inputs"`
			} `json:"transaction"`
		} `json:"data"`
	} `json:"transaction"`
	Effects struct {
		SharedObjects []SuiObjectRef `json:"sharedObjects"`
	} `json:"effects"`
	ObjectChanges []SuiObjectRef `json:"objectChanges"`
}

// SuiInput is one transaction input; only Type == "object" inputs name objects.
type SuiInput struct {
	Type     string `json:"type"`
	ObjectID string `json:"objectId"`
}

// SuiObjectRef is any entry carrying an object ID (empty for e.g. package publishes).
type SuiObjectRef struct {
	ObjectID string `json:"objectId"`
}

// UnmarshalJSON extracts the checkpoint number (index 0) and transactions (index 2)
func (s *SuiCheckpointRecord) UnmarshalJSON(data []byte) error {
	var tuple []json.RawMessage
	if err := json.Unmarshal(data, &tuple); err != nil {
		return err
	}
	if len(tuple) < 3 {
		return nil // skip invalid or incomplete tuples
	}
	if err := json.Unmarshal(tuple[0], &s.CheckpointNumber); err != nil {
		return fmt.Errorf("failed to decode checkpoint number: %w", err)
	}
	if err := json.Unmarshal(tuple[2], &s.Transactions); err != nil {
		return fmt.Errorf("failed to decode checkpoint %d transactions: %w", s.CheckpointNumber, err)
	}
	return nil
}

// --- Parser Logic ---

// ParseSuiStream digests the raw Sui checkpoint trace (one checkpoint = one block)
func ParseSuiStream() error {
	inFile, outFile, err := openFiles(SuiRawFilePath, SuiCleanFilePath)
	if err != nil {
		return err
	}
	defer inFile.Close()
	defer outFile.Close()

	decoder := json.NewDecoder(inFile)
	encoder := json.NewEncoder(outFile)

	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("failed to read opening token: %w", err)
	}
	if _, err := outFile.WriteString("[\n"); err != nil {
		return err
	}

	isFirst := true
	for decoder.More() {
		var rawCheckpoint SuiCheckpointRecord
		if err := decoder.Decode(&rawCheckpoint); err != nil {
			return fmt.Errorf("failed to decode checkpoint: %w", err)
		}

		if !isFirst {
			if _, err := outFile.WriteString(",\n"); err != nil {
				return err
			}
		}
		if err := encoder.Encode(processSuiCheckpoint(rawCheckpoint)); err != nil {
			return err
		}
		isFirst = false
	}

	_, err = outFile.WriteString("]\n")
	return err
}

// processSuiCheckpoint converts a raw checkpoint into a CleanedBlock
func processSuiCheckpoint(raw SuiCheckpointRecord) CleanedBlock {
	cleanBlock := CleanedBlock{
		BlockNumber:     raw.CheckpointNumber,
		NumTransactions: len(raw.Transactions),
		Transactions:    make([]CleanedTransaction, 0, len(raw.Transactions)),
	}
	for _, tx := range raw.Transactions {
		cleanBlock.Transactions = append(cleanBlock.Transactions, parseSuiTx(tx))
	}
	return cleanBlock
}

// parseSuiTx applies the Sui statistics paper's (PRDC) definition:
// write set = every object ID in objectChanges (created, mutated, deleted,
// wrapped, ...); read set = (object inputs ∪ effects.sharedObjects) minus the
// write set. Reads are therefore a conservative superset of the true reads.
func parseSuiTx(tx SuiTx) CleanedTransaction {
	cleanTx := CleanedTransaction{
		ID:       tx.Digest,
		WriteSet: make([]string, 0, len(tx.ObjectChanges)),
		ReadSet:  make([]string, 0),
	}

	seen := make(map[string]bool)
	for _, change := range tx.ObjectChanges {
		if change.ObjectID != "" && !seen[change.ObjectID] {
			cleanTx.WriteSet = append(cleanTx.WriteSet, change.ObjectID)
			seen[change.ObjectID] = true
		}
	}

	addRead := func(objectID string) {
		if objectID != "" && !seen[objectID] {
			cleanTx.ReadSet = append(cleanTx.ReadSet, objectID)
			seen[objectID] = true
		}
	}
	for _, input := range tx.Transaction.Data.Transaction.Inputs {
		if input.Type == "object" {
			addRead(input.ObjectID)
		}
	}
	for _, shared := range tx.Effects.SharedObjects {
		addRead(shared.ObjectID)
	}
	return cleanTx
}
