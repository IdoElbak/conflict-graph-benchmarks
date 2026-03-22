package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := "."

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Skip this program itself
		if d.Name() == "main.go" {
			return nil
		}

		// Check for .go files
		if strings.HasSuffix(d.Name(), ".go") {
			printFile(path)
		}

		return nil
	})

	if err != nil {
		fmt.Println("Error:", err)
	}
}

func printFile(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Could not read file %s: %v\n", path, err)
		return
	}

	// Print separator
	fmt.Println("--------------------------------------------------")

	// Print file path
	fmt.Printf("FILE: %s\n\n", path)

	// Print content
	fmt.Println(string(content))
}
