package digestion

// CleanedBlock represents the standardized, schema
type CleanedBlock struct {
	BlockNumber     int64                `json:"block_number"`
	NumTransactions int                  `json:"num_transactions"`
	Transactions    []CleanedTransaction `json:"transactions"`
}

// CleanedTransaction holds only the identification and dependencies
type CleanedTransaction struct {
	ID       string   `json:"id"`
	ReadSet  []string `json:"read_set"`
	WriteSet []string `json:"write_set"`
}
