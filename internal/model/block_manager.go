package model

// Manager handles the conversion of transaction raw data and maintains the global object-to-ID mapping
type Manager struct {
	IDToInt     map[string]uint32 // for object id
	readBuffer  []uint32
	writeBuffer []uint32
}

// NewManager initializes a Manager and performs the initial ingestion to return the first block of work
func NewManager(rawData someType) (*Manager, *Block) {
	// Initialize the Manager struct for the first time nad return the first block
}

// Populate updates an existing Block struct with new transaction data to minimize memory reallocations
func (manager *Manager) Populate(block *Block, rawData someType) {
	// Make a new block with the new data
	// Reuses the same block struct
}
