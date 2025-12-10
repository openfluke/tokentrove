package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// Rewriting runProcess logic to support granular progress updates
func runProcess(inputDir, outputDir string, workers int, replace bool) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("could not create output directory: %w", err)
	}

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

	logIgnored := make(chan string, 1000)
	logError := make(chan string, 1000)

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

	fmt.Println("Scanning input directory to count files...")
	var allFiles []string
	err = filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
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
	fmt.Printf("Found %d files. Starting processing with %d workers...\n", totalFiles, workers)

	jobs := make(chan Job, workers*2)
	progressChan := make(chan bool, workers*2) // Signal for each processed file
	doneProcessing := make(chan struct{})      // Signal when all files are processed and progress reported

	var wg sync.WaitGroup // To wait for all worker goroutines to finish

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1) // Increment WaitGroup counter for each worker
		go func() {
			defer wg.Done() // Decrement when worker exits
			for job := range jobs {
				processFile(job.Path, inputDir, outputDir, replace, logIgnored, logError)
				progressChan <- true // Signal that one file is done
			}
		}()
	}

	// Producer
	go func() {
		for index, path := range allFiles {
			jobs <- Job{Path: path, Index: index + 1}
		}
		close(jobs) // No more jobs will be sent
	}()

	// Progress monitor goroutine
	go func() {
		finished := 0
		// Notify every 'workers' items or 10% or something reasonable.
		// User asked for "clusters of 100 files done... like 1/100"
		// Let's print every 'workers' items to match their request "chunks of the -multi"
		notifyStep := workers
		if notifyStep < 1 {
			notifyStep = 10
		} // Ensure a minimum step

		for range progressChan {
			finished++
			if finished%notifyStep == 0 || finished == totalFiles {
				percent := float64(finished) / float64(totalFiles) * 100
				fmt.Printf("Progress: %d / %d (%.1f%%)\n", finished, totalFiles, percent)
			}
			if finished == totalFiles {
				close(doneProcessing) // Signal that all files have been processed and progress reported
				return
			}
		}
	}()

	// Wait for all workers to finish processing their jobs
	wg.Wait()
	close(progressChan) // Close progress channel after all workers are done

	// Wait for the progress monitor to finish reporting all progress
	<-doneProcessing

	close(logIgnored)
	close(logError)

	fmt.Printf("\nSuccessfully converted files into directory: %s\n", outputDir)
	return nil
}

func processFile(path, inputDir, outputDir string, replace bool, logIgnored, logError chan<- string) {
	relPath, err := filepath.Rel(inputDir, path)
	if err != nil {
		logError <- fmt.Sprintf("%s: relative path error %v", path, err)
		return
	}

	outPath := filepath.Join(outputDir, relPath+".txt")

	if !replace {
		if _, err := os.Stat(outPath); err == nil {
			return // Output file exists, skip silently
		}
	}

	res, err := pkg.ExtractContent(path)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported file extension") {
			logIgnored <- fmt.Sprintf("%s: unsupported extension", path)
			return
		}
		logError <- fmt.Sprintf("%s: extraction error: %v", path, err)
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
}
