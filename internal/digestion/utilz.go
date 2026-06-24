package digestion

import (
	"fmt"
	"os"
)

// openFiles is a shared helper to safely open streams and handle initial errors
func openFiles(inPath, outPath string) (*os.File, *os.File, error) {
	// Fast-fail if the cleaned output file already exists
	if _, err := os.Stat(outPath); err == nil {
		return nil, nil, os.ErrExist
	}

	inFile, err := os.Open(inPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open input file: %w", err)
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		inFile.Close() // prevent memory leak
		return nil, nil, fmt.Errorf("failed to create output file: %w", err)
	}

	return inFile, outFile, nil
}
