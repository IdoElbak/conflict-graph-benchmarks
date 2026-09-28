package digestion

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
)

const (
	// EthNoHubsCleanFilePath is the hub-removed variation of the cleaned trace.
	EthNoHubsCleanFilePath = "internal/digestion/data/Eth_cleaned_blocks_nohubs_21297000_21297999.json"

	// UbiquityThreshold is the fraction of a block's transactions an object
	// must be touched by to count as that block's hub and be removed.
	// Detection is per block because the ubiquitous fee-recipient account
	// differs per block builder (beaverbuild, Titan, rsync, ...), so no single
	// address is ubiquitous across the whole trace.
	UbiquityThreshold = 0.90

	// ubiquityReportTopN caps the removed-address summary printed at the end.
	ubiquityReportTopN = 15
)

// GenerateEthereumNoHubTrace derives a hub-removed variation of the cleaned
// Ethereum trace in one streaming pass: per block, objects touched by
// >= UbiquityThreshold of that block's transactions (the builder fee account
// and peers, which force density 1.0) are stripped from every access set.
// Transactions are kept even when their sets become empty so block sizes and
// transaction indices stay comparable with the original trace.
// Returns os.ErrExist if the output file already exists.
func GenerateEthereumNoHubTrace() error {
	if _, err := os.Stat(EthNoHubsCleanFilePath); err == nil {
		return os.ErrExist
	}

	inFile, outFile, err := openFiles(EthCleanFilePath, EthNoHubsCleanFilePath)
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

	touchCounts := make(map[string]uint32)
	removedBlocks := make(map[string]int) // address -> number of blocks it was removed from
	var totalRemoved, blocksWithoutHubs, emptiedTxns, totalBlocks int

	isFirst := true
	for decoder.More() {
		var block CleanedBlock
		if err := decoder.Decode(&block); err != nil {
			return fmt.Errorf("failed to decode block: %w", err)
		}

		removed := detectBlockHubs(&block, touchCounts)
		for addr := range removed {
			removedBlocks[addr]++
		}
		totalRemoved += len(removed)
		if len(removed) == 0 {
			blocksWithoutHubs++
		}

		for i := range block.Transactions {
			txn := &block.Transactions[i]
			txn.ReadSet = filterAddresses(txn.ReadSet, removed)
			txn.WriteSet = filterAddresses(txn.WriteSet, removed)
			if len(txn.ReadSet) == 0 && len(txn.WriteSet) == 0 {
				emptiedTxns++
			}
		}

		if !isFirst {
			if _, err := outFile.WriteString(",\n"); err != nil {
				return err
			}
		}
		if err := encoder.Encode(block); err != nil {
			return err
		}
		isFirst = false
		totalBlocks++
	}

	if _, err := outFile.WriteString("]\n"); err != nil {
		return err
	}

	logRemovalSummary(removedBlocks, totalRemoved, totalBlocks, blocksWithoutHubs, emptiedTxns)
	return nil
}

// detectBlockHubs returns the block's ubiquitous objects: addresses touched by
// >= UbiquityThreshold of the block's transactions. touchCounts is a reused
// scratch map, cleared on entry. Read and write sets are disjoint within a
// transaction by parser construction, so one increment per entry equals one
// touching transaction.
func detectBlockHubs(block *CleanedBlock, touchCounts map[string]uint32) map[string]struct{} {
	clear(touchCounts)
	for i := range block.Transactions {
		txn := &block.Transactions[i]
		for _, addr := range txn.ReadSet {
			touchCounts[addr]++
		}
		for _, addr := range txn.WriteSet {
			touchCounts[addr]++
		}
	}

	removed := make(map[string]struct{})
	numTxns := len(block.Transactions)
	if numTxns == 0 {
		return removed
	}
	for addr, count := range touchCounts {
		if float64(count)/float64(numTxns) >= UbiquityThreshold {
			removed[addr] = struct{}{}
		}
	}
	return removed
}

// logRemovalSummary prints the distinct removed addresses ranked by how many
// blocks they were removed from, plus aggregate statistics.
func logRemovalSummary(removedBlocks map[string]int, totalRemoved, totalBlocks, blocksWithoutHubs, emptiedTxns int) {
	type removedAddr struct {
		addr   string
		blocks int
	}
	ranked := make([]removedAddr, 0, len(removedBlocks))
	for addr, blocks := range removedBlocks {
		ranked = append(ranked, removedAddr{addr, blocks})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].blocks != ranked[j].blocks {
			return ranked[i].blocks > ranked[j].blocks
		}
		return ranked[i].addr < ranked[j].addr
	})

	log.Printf("[HubFilter] wrote %s", EthNoHubsCleanFilePath)
	log.Printf("[HubFilter] per-block ubiquity threshold %.0f%%: %d distinct hub addresses, avg %.2f removed/block, %d/%d blocks had none, %d transactions left fully empty",
		UbiquityThreshold*100, len(removedBlocks), float64(totalRemoved)/float64(totalBlocks), blocksWithoutHubs, totalBlocks, emptiedTxns)
	limit := len(ranked)
	if limit > ubiquityReportTopN {
		limit = ubiquityReportTopN
	}
	for i := 0; i < limit; i++ {
		log.Printf("[HubFilter]  %2d. %s  removed in %d blocks", i+1, ranked[i].addr, ranked[i].blocks)
	}
}

// filterAddresses removes the block's hub addresses in place, preserving order.
func filterAddresses(addrs []string, removed map[string]struct{}) []string {
	if len(removed) == 0 {
		return addrs
	}
	kept := addrs[:0]
	for _, addr := range addrs {
		if _, isRemoved := removed[addr]; !isRemoved {
			kept = append(kept, addr)
		}
	}
	return kept
}
