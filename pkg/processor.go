package pkg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Job represents a file to be processed
type Job struct {
	Path  string
	Index int
}

// ParseMemoryLimit parses a memory limit string (e.g., "1GB", "512MB") into bytes
func ParseMemoryLimit(s string) (uint64, error) {
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

	var val uint64
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil {
		return 0, fmt.Errorf("invalid memory format: %s", s)
	}
	return val * multiplier, nil
}

// RunProcess processes files from inputDir to outputDir with concurrent workers
func RunProcess(inputDir, outputDir, processType string, workers int, replace bool, ramLimit uint64) error {
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
	progressChan := make(chan bool, workers*2)
	doneProcessing := make(chan struct{})

	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				processFile(job.Path, inputDir, outputDir, processType, replace, logIgnored, logError)
				progressChan <- true
			}
		}()
	}

	go func() {
		var m runtime.MemStats
		for index, path := range allFiles {
			if ramLimit > 0 {
				for {
					runtime.ReadMemStats(&m)
					if m.Alloc < ramLimit {
						break
					}
					runtime.GC()
					time.Sleep(100 * time.Millisecond)
				}
			}

			jobs <- Job{Path: path, Index: index + 1}
		}
		close(jobs)
	}()

	go func() {
		finished := 0
		notifyStep := workers
		if notifyStep < 1 {
			notifyStep = 10
		}

		for range progressChan {
			finished++
			if finished%notifyStep == 0 || finished == totalFiles {
				runtime.GC()
				percent := float64(finished) / float64(totalFiles) * 100
				fmt.Printf("Progress: %d / %d (%.1f%%)\n", finished, totalFiles, percent)
			}
			if finished == totalFiles {
				close(doneProcessing)
				return
			}
		}
	}()

	wg.Wait()
	close(progressChan)

	<-doneProcessing

	close(logIgnored)
	close(logError)

	fmt.Printf("\nSuccessfully converted files into directory: %s\n", outputDir)
	return nil
}

func processFile(path, inputDir, outputDir, processType string, replace bool, logIgnored, logError chan<- string) {
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
			return
		}
	}

	res, err := ExtractContent(path)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported file extension") {
			logIgnored <- fmt.Sprintf("%s: unsupported extension", path)
			return
		}
		logError <- fmt.Sprintf("%s: extraction error: %v", path, err)
		return
	}

	outputText := res.FullText

	switch processType {
	case "token":
		outputText = CleanToTokens(outputText)
	case "lowercase":
		outputText = CleanToLowerTokens(outputText)
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

// CleanToTokens removes all special characters, newlines, tabs, etc.
// and returns only words separated by single spaces
func CleanToTokens(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\t", " ")

	re := regexp.MustCompile(`[^a-zA-Z0-9\s]`)
	text = re.ReplaceAllString(text, " ")

	spaceRe := regexp.MustCompile(`\s+`)
	text = spaceRe.ReplaceAllString(text, " ")

	text = strings.TrimSpace(text)

	return text
}

// CleanToLowerTokens is like CleanToTokens but also converts to lowercase
func CleanToLowerTokens(text string) string {
	text = CleanToTokens(text)
	return strings.ToLower(text)
}
