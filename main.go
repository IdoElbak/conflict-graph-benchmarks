package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// Create or overwrite the output file
	outFile, err := os.Create("projects_code.txt")
	if err != nil {
		fmt.Println("Error creating file:", err)
		return
	}
	defer outFile.Close()

	// MultiWriter outputs to both the terminal and the file
	out := io.MultiWriter(os.Stdout, outFile)

	root := "."

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
			printFile(path, out)
		}

		return nil
	})

	if err != nil {
		fmt.Println("Error:", err)
	}
}

func printFile(path string, out io.Writer) {
	content, err := os.ReadFile(path)
	if err != nil {
		// Keep this one printing to standard error/console only
		fmt.Printf("Could not read file %s: %v\n", path, err)
		return
	}

	// Print separator
	fmt.Fprintln(out, "--------------------------------------------------")

	// Print file path
	fmt.Fprintf(out, "FILE: %s\n\n", path)

	// Print content
	fmt.Fprintln(out, string(content))
}
