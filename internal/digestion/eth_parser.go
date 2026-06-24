package digestion

import (
	"encoding/json"
	"fmt"
)

const (
	EthRawFilePath   = "internal/digestion/data/Eth_raw_21297000_21297999.json"
	EthCleanFilePath = "internal/digestion/data/Eth_cleaned_blocks_21297000_21297999.json"
)

// --- Raw Schema Definitions ---

type EthBlockRecord struct {
	BlockNumber int64
	Touched     []EthTxState // Index 1: All accounts touched
	Diffs       []EthTxDiff  // Index 2: Accounts that changed
}

// EthTxState captures the absolute state keys to find pure reads
type EthTxState struct {
	TxHash string                     `json:"txHash"`
	Result map[string]json.RawMessage `json:"result"` // RawMessage prevents decoding heavy nested objects
}

// EthTxDiff captures the state modifications
type EthTxDiff struct {
	TxHash string `json:"txHash"`
	Result struct {
		Pre  map[string]json.RawMessage `json:"pre"`
		Post map[string]json.RawMessage `json:"post"`
	} `json:"result"`
}

// UnmarshalJSON safely extracts Index 0, 1, and 2
func (b *EthBlockRecord) UnmarshalJSON(data []byte) error {
	var tuple []json.RawMessage
	if err := json.Unmarshal(data, &tuple); err != nil {
		return err
	}
	if len(tuple) < 3 {
		return nil
	}
	_ = json.Unmarshal(tuple[0], &b.BlockNumber)
	_ = json.Unmarshal(tuple[1], &b.Touched) // Grab ALL touched accounts
	_ = json.Unmarshal(tuple[2], &b.Diffs)   // Grab the state diffs
	return nil
}

// --- Parser Logic ---

func ParseEthereumStream() error {
	inFile, outFile, err := openFiles(EthRawFilePath, EthCleanFilePath)
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
		var rawBlock EthBlockRecord
		if err := decoder.Decode(&rawBlock); err != nil {
			continue
		}

		cleanBlock := processEthBlock(rawBlock)

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

func processEthBlock(rawBlock EthBlockRecord) CleanedBlock {
	cleanBlock := CleanedBlock{
		BlockNumber:     rawBlock.BlockNumber,
		NumTransactions: len(rawBlock.Diffs),
		Transactions:    make([]CleanedTransaction, 0, len(rawBlock.Diffs)),
	}

	// Map the Touched accounts by TxHash for safe lookup
	touchedMap := make(map[string]map[string]json.RawMessage)
	for _, t := range rawBlock.Touched {
		if t.Result != nil {
			touchedMap[t.TxHash] = t.Result
		}
	}

	for _, diff := range rawBlock.Diffs {
		cleanTx := CleanedTransaction{
			ID:       diff.TxHash,
			WriteSet: make([]string, 0),
			ReadSet:  make([]string, 0),
		}

		writeMap := make(map[string]bool)

		// Any address in the diff was modified
		if diff.Result.Post != nil {
			for addr := range diff.Result.Post {
				cleanTx.WriteSet = append(cleanTx.WriteSet, addr)
				writeMap[addr] = true
			}
		}
		if diff.Result.Pre != nil {
			for addr := range diff.Result.Pre {
				if !writeMap[addr] {
					cleanTx.WriteSet = append(cleanTx.WriteSet, addr)
					writeMap[addr] = true
				}
			}
		}

		// Any address that was touched but NOT written is a pure Read
		if touched, exists := touchedMap[diff.TxHash]; exists {
			for addr := range touched {
				if !writeMap[addr] {
					cleanTx.ReadSet = append(cleanTx.ReadSet, addr)
				}
			}
		}

		cleanBlock.Transactions = append(cleanBlock.Transactions, cleanTx)
	}

	return cleanBlock
}
