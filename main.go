package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openfluke/tokentrove/pkg"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "process":
		processCmd := flag.NewFlagSet("process", flag.ExitOnError)
		inputDir := processCmd.String("input", "", "Input directory to process (required)")
		outputFile := processCmd.String("output", "output.txt", "Output text file / directory")
		processType := processCmd.String("type", "all", "Type of processing: 'all' (default: extract text from all supported files)")
		concurrency := processCmd.Int("multi", 100, "Number of concurrent workers")
		replace := processCmd.Bool("r", false, "Replace existing files in output")

		processCmd.Parse(os.Args[2:])

		if *inputDir == "" {
			fmt.Println("Error: -input directory is required")
			processCmd.PrintDefaults()
			os.Exit(1)
		}

		fmt.Printf("Starting process (Type: %s, Workers: %d, Replace: %v)...\n", *processType, *concurrency, *replace)
		// Currently only 'all' logic exists, but structure is ready for more types

		if err := runProcess(*inputDir, *outputFile, *concurrency, *replace); err != nil {
			fmt.Printf("Error processing files: %v\n", err)
			os.Exit(1)
		}

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: tokentrove <command> [arguments]")
	fmt.Println("\nCommands:")
	fmt.Println("  process    Process a directory and extract text from all supported files")
	fmt.Println("\nRun 'tokentrove <command> -h' for more information.")
}

// Job represents a file to be processed
type Job struct {
	Path  string
	Index int
}

func runProcess(inputDir, outputDir string, workers int, replace bool) error {
	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("could not create output directory: %w", err)
	}

	// 1. Log files setup
	ignoredFile, err := os.OpenFile(filepath.Join(outputDir, "ignored.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("setup logs: %w", err)
	}
	defer ignoredFile.Close()

	errorsFile, err := os.OpenFile(filepath.Join(outputDir, "errors.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("setup logs: %w", err)
	}
	defer errorsFile.Close()

	// Create channels for safe logging
	logIgnored := make(chan string, 100)
	logError := make(chan string, 100)

	go func() {
		for msg := range logIgnored {
			ignoredFile.WriteString(msg + "\n")
		}
	}()
	go func() {
		for msg := range logError {
			errorsFile.WriteString(msg + "\n")
		}
	}()

	// 2. Count total files first
	fmt.Println("Scanning input directory to count files...")
	var allFiles []string
	err = filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		} // skip permission errors during scan
		if info.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}
		allFiles = append(allFiles, path)
		return nil
	})
	if err != nil {
		return err
	}

	totalFiles := len(allFiles)
	fmt.Printf("Found %d files. Starting processing...\n", totalFiles)

	jobs := make(chan Job, workers*2)
	results := make(chan error, workers)

	// Start workers
	for i := 0; i < workers; i++ {
		go func(id int) {
			for job := range jobs {
				processFile(job.Path, inputDir, outputDir, job.Index, totalFiles, replace, logIgnored, logError)
			}
			results <- nil
		}(i)
	}

	// Producer
	for i, path := range allFiles {
		jobs <- Job{Path: path, Index: i + 1}
	}
	close(jobs)

	// Wait
	for i := 0; i < workers; i++ {
		<-results
	}

	close(logIgnored)
	close(logError)

	fmt.Printf("\nSuccessfully converted files into directory: %s\n", outputDir)
	return nil
}

func processFile(path, inputDir, outputDir string, index, total int, replace bool, logIgnored, logError chan<- string) {
	relPath, err := filepath.Rel(inputDir, path)
	if err != nil {
		logError <- fmt.Sprintf("%s: relative path error %v", path, err)
		return
	}

	outPath := filepath.Join(outputDir, relPath+".txt")

	// Check existing
	if !replace {
		if _, err := os.Stat(outPath); err == nil {
			// exists
			fmt.Printf("[%d/%d] [Skipped] %s (Exists)\n", index, total, path)
			return
		}
	} else {
		// If replace is true, using os.WriteFile will overwrite, so no manual remove needed usually,
		// but checking allows for explicit logging if desired.
	}

	res, err := pkg.ExtractContent(path)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported file extension") {
			logIgnored <- fmt.Sprintf("%s: unsupported extension", path)
			// Silent on console for unsupported
			// fmt.Printf("[%d/%d] [Ignored] %s\n", index, total, path)
			return
		}
		logError <- fmt.Sprintf("%s: extraction error: %v", path, err)
		fmt.Printf("[%d/%d] [Error] %s\n", index, total, path) // Show errors on console too?
		return
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		logError <- fmt.Sprintf("%s: mkdir error: %v", path, err)
		return
	}

	if err := os.WriteFile(outPath, []byte(res.FullText), 0644); err != nil {
		logError <- fmt.Sprintf("%s: write error: %v", path, err)
		return
	}

	// Progress calculation could be fancy, but "1/N" is requested
	fmt.Printf("[%d/%d] [Done] %s\n", index, total, path)
}
