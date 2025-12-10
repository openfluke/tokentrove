package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

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
		processType := processCmd.String("type", "text", "Type of processing: 'text' (default), 'token' (words and spaces only)")
		concurrency := processCmd.Int("multi", 100, "Number of concurrent workers")
		replace := processCmd.Bool("r", false, "Replace existing files in output")
		ramLimitStr := processCmd.String("ram-limit", "", "Soft memory limit (e.g., '1GB', '512MB'). If exceeded, pauses feeding workers.")
		statusOnly := processCmd.Bool("status", false, "Show remaining files to convert by file type (no processing)")
		cacheMode := processCmd.String("cache", "", "Cache mode: 'tokens' (extract unique words to uniq.txt)")

		processCmd.Parse(os.Args[2:])

		if *inputDir == "" {
			fmt.Println("Error: -input directory is required")
			processCmd.PrintDefaults()
			os.Exit(1)
		}

		// Handle cache mode
		if *cacheMode != "" {
			if *cacheMode == "tokens" {
				if err := buildTokenCache(*inputDir, *outputFile); err != nil {
					fmt.Printf("Error building token cache: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Printf("Unknown cache mode: %s (use 'tokens')\n", *cacheMode)
				os.Exit(1)
			}
			return
		}

		// Handle status mode
		if *statusOnly {
			if err := showStatus(*inputDir, *outputFile); err != nil {
				fmt.Printf("Error getting status: %v\n", err)
				os.Exit(1)
			}
			return
		}

		ramLimit, err := parseMemoryLimit(*ramLimitStr)
		if err != nil {
			fmt.Printf("Error checking RAM limit: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Starting process (Type: %s, Workers: %d, Replace: %v, RAM Limit: %s)...\n", *processType, *concurrency, *replace, *ramLimitStr)
		// Currently only 'all' logic exists, but structure is ready for more types

		if err := runProcess(*inputDir, *outputFile, *processType, *concurrency, *replace, ramLimit); err != nil {
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

func buildTokenCache(inputDir, outputDir string) error {
	fmt.Println("Building token cache...")
	fmt.Printf("Input:  %s\n", inputDir)
	fmt.Printf("Output: %s\n\n", outputDir)

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("could not create output directory: %w", err)
	}

	// Write settings.txt with input path (overwrites if exists)
	settingsPath := filepath.Join(outputDir, "settings.txt")
	if err := os.WriteFile(settingsPath, []byte("input="+inputDir+"\n"), 0644); err != nil {
		return fmt.Errorf("could not write settings: %w", err)
	}
	fmt.Printf("Settings written to: %s\n", settingsPath)

	// Use a map to track unique words
	uniqueWords := make(map[string]struct{})

	// Count files first
	var fileCount int
	err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}
		fileCount++
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("Found %d files to scan...\n", fileCount)

	// Track all file paths (relative)
	var allFiles []string

	// Process each file
	processed := 0
	err = filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		// Track relative file path
		relPath, err := filepath.Rel(inputDir, path)
		if err != nil {
			relPath = path // fallback to full path if rel fails
		}
		allFiles = append(allFiles, relPath)

		// Read file content
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for long lines

		for scanner.Scan() {
			line := scanner.Text()
			// Split by whitespace
			words := strings.Fields(line)
			for _, word := range words {
				// Clean the word - trim
				word = strings.TrimSpace(word)
				if word != "" {
					uniqueWords[word] = struct{}{}
				}
			}
		}

		processed++
		if processed%1000 == 0 || processed == fileCount {
			fmt.Printf("Scanned: %d / %d files (%d unique tokens so far)\n", processed, fileCount, len(uniqueWords))
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Sort the words
	sortedWords := make([]string, 0, len(uniqueWords))
	for word := range uniqueWords {
		sortedWords = append(sortedWords, word)
	}
	sort.Strings(sortedWords)

	// Write to uniq.txt (overwrites if exists)
	outPath := filepath.Join(outputDir, "uniq.txt")
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("could not create output file: %w", err)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	for _, word := range sortedWords {
		writer.WriteString(word)
		writer.WriteString("\n")
	}
	writer.Flush()

	fmt.Printf("\nDone! Found %d unique tokens.\n", len(sortedWords))
	fmt.Printf("Written to: %s\n", outPath)

	// Write files.txt with relative file paths (overwrites if exists)
	filesPath := filepath.Join(outputDir, "files.txt")
	filesFile, err := os.Create(filesPath)
	if err != nil {
		return fmt.Errorf("could not create files list: %w", err)
	}
	defer filesFile.Close()

	filesWriter := bufio.NewWriter(filesFile)
	for _, relPath := range allFiles {
		filesWriter.WriteString(relPath)
		filesWriter.WriteString("\n")
	}
	filesWriter.Flush()

	fmt.Printf("File list written to: %s (%d files)\n", filesPath, len(allFiles))

	return nil
}

func showStatus(inputDir, outputDir string) error {
	// Count files by extension in input directory
	inputCounts := make(map[string]int)
	err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == "" {
			ext = "(no extension)"
		}
		inputCounts[ext]++
		return nil
	})
	if err != nil {
		return err
	}

	// Count files already converted in output directory (they have .txt suffix)
	convertedCounts := make(map[string]int)
	err = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		// Output files are named like original.ext.txt
		// So we need to strip .txt and get the original extension
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".txt") {
			return nil
		}
		// Remove .txt suffix to get original filename
		original := strings.TrimSuffix(base, ".txt")
		ext := strings.ToLower(filepath.Ext(original))
		if ext == "" {
			ext = "(no extension)"
		}
		convertedCounts[ext]++
		return nil
	})
	if err != nil {
		// Output dir might not exist yet, that's okay
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Calculate remaining files
	fmt.Println("\n=== Conversion Status ===")
	fmt.Printf("Input:  %s\n", inputDir)
	fmt.Printf("Output: %s\n\n", outputDir)

	totalInput := 0
	totalConverted := 0
	totalRemaining := 0

	// Collect all extensions
	allExts := make(map[string]bool)
	for ext := range inputCounts {
		allExts[ext] = true
	}

	// Sort extensions for consistent output
	var exts []string
	for ext := range allExts {
		exts = append(exts, ext)
	}

	fmt.Printf("%-15s %8s %10s %10s\n", "Extension", "Total", "Converted", "Remaining")
	fmt.Println(strings.Repeat("-", 45))

	for _, ext := range exts {
		input := inputCounts[ext]
		converted := convertedCounts[ext]
		remaining := input - converted
		if remaining < 0 {
			remaining = 0
		}

		totalInput += input
		totalConverted += converted
		totalRemaining += remaining

		fmt.Printf("%-15s %8d %10d %10d\n", ext, input, converted, remaining)
	}

	fmt.Println(strings.Repeat("-", 45))
	fmt.Printf("%-15s %8d %10d %10d\n", "TOTAL", totalInput, totalConverted, totalRemaining)
	fmt.Println()

	return nil
}

func parseMemoryLimit(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	s = strings.ToUpper(strings.TrimSpace(s))
	var multiplier uint64 = 1
	if strings.HasSuffix(s, "G") || strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "GB"), "G")
	} else if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "MB"), "M")
	} else if strings.HasSuffix(s, "K") || strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "KB"), "K")
	}

	// Poor man's Atoi since we just stripped suffix
	var val uint64
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil {
		return 0, fmt.Errorf("invalid memory format: %s", s)
	}
	return val * multiplier, nil
}

// Job represents a file to be processed
type Job struct {
	Path  string
	Index int
}

// Rewriting runProcess logic to support granular progress updates
func runProcess(inputDir, outputDir, processType string, workers int, replace bool, ramLimit uint64) error {
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
				processFile(job.Path, inputDir, outputDir, processType, replace, logIgnored, logError)
				progressChan <- true // Signal that one file is done
			}
		}()
	}

	// Producer
	go func() {
		var m runtime.MemStats
		for index, path := range allFiles {
			// RAM Throttling
			if ramLimit > 0 {
				for {
					runtime.ReadMemStats(&m)
					if m.Alloc < ramLimit {
						break
					}
					// RAM usage too high, wait for workers to finish some jobs and GC to run
					runtime.GC()
					time.Sleep(100 * time.Millisecond)
				}
			}

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
				runtime.GC() // Manual GC after each batch
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

func processFile(path, inputDir, outputDir, processType string, replace bool, logIgnored, logError chan<- string) {
	// Panic recovery for individual file processing
	defer func() {
		if r := recover(); r != nil {
			logError <- fmt.Sprintf("%s: PANIC during processing: %v", path, r)
		}
	}()

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

	outputText := res.FullText

	// If token mode, clean the text to only words and spaces
	if processType == "token" {
		outputText = cleanToTokens(outputText)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		logError <- fmt.Sprintf("%s: mkdir error: %v", path, err)
		return
	}

	if err := os.WriteFile(outPath, []byte(outputText), 0644); err != nil {
		logError <- fmt.Sprintf("%s: write error: %v", path, err)
		return
	}
}

// cleanToTokens removes all special characters, newlines, tabs, etc.
// and returns only words separated by single spaces
func cleanToTokens(text string) string {
	// Replace common whitespace with spaces
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\t", " ")

	// Keep only letters, numbers, and spaces
	re := regexp.MustCompile(`[^a-zA-Z0-9\s]`)
	text = re.ReplaceAllString(text, " ")

	// Collapse multiple spaces into single space
	spaceRe := regexp.MustCompile(`\s+`)
	text = spaceRe.ReplaceAllString(text, " ")

	// Trim leading/trailing spaces
	text = strings.TrimSpace(text)

	return text
}
