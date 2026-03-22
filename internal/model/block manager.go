package model

type Manager struct {
	idToInt     map[string]uint32 // for object id
	readBuffer  []uint32
	writeBuffer []uint32
}

func NewManager(rawData someType) (*Manager, *Block) {
	// Initialize the Manager struct for the first time nad return the first block
}

func (manager *Manager) Populate(block *Block, rawData someType) {
	// Make a new block with the new data
	// Reuses the same block struct
}
