package model

import (
	"slices"

	"conflict-graph-benchmarks/internal/digestion"
)

// Manager handles the conversion of transaction raw data and maintains memory-safe buffers
type Manager struct {
	objectIDMap map[string]uint32
	readBuffer  []uint32
	writeBuffer []uint32
}

// NewManager initializes the pre-allocated contiguous buffers
func NewManager() *Manager {
	return &Manager{
		objectIDMap: make(map[string]uint32, 10000),
		readBuffer:  make([]uint32, 0, 100000),
		writeBuffer: make([]uint32, 0, 100000),
	}
}

// Populate translates a JSON CleanedBlock into the highly optimized integer Block format
func (m *Manager) Populate(block *Block, cleanBlock digestion.CleanedBlock) {
	// Wipe the dictionary and reset buffer bounds (keeping capacity intact)
	clear(m.objectIDMap)
	m.readBuffer = m.readBuffer[:0]
	m.writeBuffer = m.writeBuffer[:0]

	var objectCounter uint32 = 0

	// Helper to handle dense ID mapping
	getID := func(hash string) uint32 {
		if id, exists := m.objectIDMap[hash]; exists {
			return id
		}
		id := objectCounter
		m.objectIDMap[hash] = id
		objectCounter++
		return id
	}

	// Preallocate the transaction array
	numTxns := len(cleanBlock.Transactions)
	if cap(block.Transactions) < numTxns {
		block.Transactions = make([]Transaction, numTxns)
	}
	block.Transactions = block.Transactions[:numTxns]

	// Map strings, pack buffers, and sort arrays inline
	for i, cleanTx := range cleanBlock.Transactions {
		block.Transactions[i].ID = uint32(i) // Sequential execution ID

		// Pack ReadSet
		readStart := len(m.readBuffer)
		for _, objStr := range cleanTx.ReadSet {
			m.readBuffer = append(m.readBuffer, getID(objStr))
		}
		readEnd := len(m.readBuffer)
		block.Transactions[i].ReadSet = m.readBuffer[readStart:readEnd]
		slices.Sort(block.Transactions[i].ReadSet)

		// Pack WriteSet
		writeStart := len(m.writeBuffer)
		for _, objStr := range cleanTx.WriteSet {
			m.writeBuffer = append(m.writeBuffer, getID(objStr))
		}
		writeEnd := len(m.writeBuffer)
		block.Transactions[i].WriteSet = m.writeBuffer[writeStart:writeEnd]
		slices.Sort(block.Transactions[i].WriteSet)
	}

	block.NumUniqueObjects = objectCounter
}
