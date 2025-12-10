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
		cacheMode := processCmd.String("cache", "", "Cache mode: 'tokens', 'index', or 'ngrams'")
		ngramMax := processCmd.Int("ngrams", 15, "Max n-gram size (used with -cache ngrams)")

		processCmd.Parse(os.Args[2:])

		if *inputDir == "" {
			fmt.Println("Error: -input directory is required")
			processCmd.PrintDefaults()
			os.Exit(1)
		}

		// Handle cache mode
		if *cacheMode != "" {
			switch *cacheMode {
			case "tokens":
				if err := buildTokenCache(*inputDir, *outputFile); err != nil {
					fmt.Printf("Error building token cache: %v\n", err)
					os.Exit(1)
				}
			case "index":
				if err := buildIndexCache(*inputDir, *outputFile); err != nil {
					fmt.Printf("Error building index cache: %v\n", err)
					os.Exit(1)
				}
			case "ngrams":
				if err := buildNgramCache(*outputFile, *ngramMax); err != nil {
					fmt.Printf("Error building ngram cache: %v\n", err)
					os.Exit(1)
				}
			case "ngramfiles":
				if err := buildNgramFilesCache(*outputFile, *ngramMax); err != nil {
					fmt.Printf("Error building ngramfiles cache: %v\n", err)
					os.Exit(1)
				}
			case "ngramfreq":
				if err := buildNgramFreqCache(*outputFile, *ngramMax); err != nil {
					fmt.Printf("Error building ngramfreq cache: %v\n", err)
					os.Exit(1)
				}
			default:
				fmt.Printf("Unknown cache mode: %s (use 'tokens', 'index', 'ngrams', 'ngramfiles', or 'ngramfreq')\n", *cacheMode)
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

	case "ngramfiles":
		ngramfilesCmd := flag.NewFlagSet("ngramfiles", flag.ExitOnError)
		cacheDir := ngramfilesCmd.String("cache", "", "Cache directory containing ngram files (required)")
		ngramMax := ngramfilesCmd.Int("ngrams", 15, "Max n-gram size")

		ngramfilesCmd.Parse(os.Args[2:])

		if *cacheDir == "" {
			fmt.Println("Error: -cache directory is required")
			ngramfilesCmd.PrintDefaults()
			os.Exit(1)
		}

		if err := buildNgramFilesCache(*cacheDir, *ngramMax); err != nil {
			fmt.Printf("Error building ngramfiles cache: %v\n", err)
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
	fmt.Println("  process      Process a directory and extract text from all supported files")
	fmt.Println("  ngramfiles   Build file → ngram reverse index from existing ngram cache")
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

func buildIndexCache(inputDir, outputDir string) error {
	fmt.Println("Building index cache...")
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	// Load settings.txt to get the original input path for token files
	settingsPath := filepath.Join(outputDir, "settings.txt")
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("could not read settings.txt (run -cache tokens first): %w", err)
	}

	// Parse input path from settings
	var tokenInputDir string
	for _, line := range strings.Split(string(settingsData), "\n") {
		if strings.HasPrefix(line, "input=") {
			tokenInputDir = strings.TrimPrefix(line, "input=")
			break
		}
	}
	if tokenInputDir == "" {
		return fmt.Errorf("could not find input path in settings.txt")
	}
	fmt.Printf("Token files dir: %s\n", tokenInputDir)

	// Load uniq.txt into map (word -> index)
	uniqPath := filepath.Join(outputDir, "uniq.txt")
	uniqFile, err := os.Open(uniqPath)
	if err != nil {
		return fmt.Errorf("could not open uniq.txt (run -cache tokens first): %w", err)
	}
	defer uniqFile.Close()

	wordToIndex := make(map[string]int)
	scanner := bufio.NewScanner(uniqFile)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	wordIndex := 0
	for scanner.Scan() {
		word := scanner.Text()
		wordToIndex[word] = wordIndex
		wordIndex++
	}
	fmt.Printf("Loaded %d unique words from uniq.txt\n", len(wordToIndex))

	// Load files.txt into map (relative path -> index)
	filesPath := filepath.Join(outputDir, "files.txt")
	filesFile, err := os.Open(filesPath)
	if err != nil {
		return fmt.Errorf("could not open files.txt (run -cache tokens first): %w", err)
	}
	defer filesFile.Close()

	fileToIndex := make(map[string]int)
	var filesList []string
	scanner = bufio.NewScanner(filesFile)
	fileIndex := 0
	for scanner.Scan() {
		relPath := scanner.Text()
		fileToIndex[relPath] = fileIndex
		filesList = append(filesList, relPath)
		fileIndex++
	}
	fmt.Printf("Loaded %d files from files.txt\n", len(filesList))

	// Build word -> file indices mapping
	// wordToFiles[wordIndex] = list of file indices containing that word
	wordToFiles := make(map[int]map[int]struct{})

	fmt.Println("\nScanning files for word occurrences...")
	for i, relPath := range filesList {
		fullPath := filepath.Join(tokenInputDir, relPath)

		file, err := os.Open(fullPath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		for scanner.Scan() {
			words := strings.Fields(scanner.Text())
			for _, word := range words {
				word = strings.TrimSpace(word)
				if wIdx, ok := wordToIndex[word]; ok {
					if wordToFiles[wIdx] == nil {
						wordToFiles[wIdx] = make(map[int]struct{})
					}
					wordToFiles[wIdx][i] = struct{}{}
				}
			}
		}
		file.Close()

		if (i+1)%1000 == 0 || i+1 == len(filesList) {
			fmt.Printf("Processed: %d / %d files\n", i+1, len(filesList))
		}
	}

	// Write fileuniqindex.txt
	indexPath := filepath.Join(outputDir, "fileuniqindex.txt")
	indexFile, err := os.Create(indexPath)
	if err != nil {
		return fmt.Errorf("could not create fileuniqindex.txt: %w", err)
	}
	defer indexFile.Close()

	writer := bufio.NewWriter(indexFile)

	// Write in order of word index
	for wIdx := 0; wIdx < len(wordToIndex); wIdx++ {
		fileIndices, ok := wordToFiles[wIdx]
		if !ok || len(fileIndices) == 0 {
			// Word not found in any file (shouldn't happen but handle it)
			writer.WriteString(fmt.Sprintf("%d,[]\n", wIdx))
			continue
		}

		// Collect and sort file indices
		indices := make([]int, 0, len(fileIndices))
		for fIdx := range fileIndices {
			indices = append(indices, fIdx)
		}
		sort.Ints(indices)

		// Format as: wordIndex,[fileIndex1,fileIndex2,...]
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%d,[", wIdx))
		for j, fIdx := range indices {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf("%d", fIdx))
		}
		sb.WriteString("]\n")
		writer.WriteString(sb.String())
	}
	writer.Flush()

	fmt.Printf("\nDone! Index written to: %s\n", indexPath)
	fmt.Printf("Mapped %d words to their file locations\n", len(wordToFiles))

	return nil
}

func buildNgramCache(outputDir string, maxN int) error {
	fmt.Printf("Building n-gram cache (2 to %d grams)...\n", maxN)
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	if maxN < 2 {
		return fmt.Errorf("ngrams must be at least 2")
	}

	// Load settings.txt to get the original input path for token files
	settingsPath := filepath.Join(outputDir, "settings.txt")
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("could not read settings.txt (run -cache tokens first): %w", err)
	}

	var tokenInputDir string
	for _, line := range strings.Split(string(settingsData), "\n") {
		if strings.HasPrefix(line, "input=") {
			tokenInputDir = strings.TrimPrefix(line, "input=")
			break
		}
	}
	if tokenInputDir == "" {
		return fmt.Errorf("could not find input path in settings.txt")
	}
	fmt.Printf("Token files dir: %s\n", tokenInputDir)

	// Load uniq.txt into map (word -> index)
	uniqPath := filepath.Join(outputDir, "uniq.txt")
	uniqFile, err := os.Open(uniqPath)
	if err != nil {
		return fmt.Errorf("could not open uniq.txt: %w", err)
	}
	defer uniqFile.Close()

	wordToIndex := make(map[string]int)
	scanner := bufio.NewScanner(uniqFile)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	wordIdx := 0
	for scanner.Scan() {
		wordToIndex[scanner.Text()] = wordIdx
		wordIdx++
	}
	fmt.Printf("Loaded %d unique words\n", len(wordToIndex))

	// Load files.txt
	filesPath := filepath.Join(outputDir, "files.txt")
	filesFile, err := os.Open(filesPath)
	if err != nil {
		return fmt.Errorf("could not open files.txt: %w", err)
	}
	defer filesFile.Close()

	var filesList []string
	scanner = bufio.NewScanner(filesFile)
	for scanner.Scan() {
		filesList = append(filesList, scanner.Text())
	}
	fmt.Printf("Loaded %d files\n\n", len(filesList))

	// For each n from 2 to maxN, we need:
	// - uniqNgram.txt: unique n-grams as word indices (e.g., "0|5|23")
	// - Ngramindex.txt: ngram index -> file indices

	for n := 2; n <= maxN; n++ {
		fmt.Printf("Processing %d-grams...\n", n)

		// Map: ngram string (e.g., "0|5|23") -> ngram index
		ngramToIndex := make(map[string]int)
		// Map: ngram index -> set of file indices
		ngramToFiles := make(map[int]map[int]struct{})
		ngramCount := 0

		for fileIdx, relPath := range filesList {
			fullPath := filepath.Join(tokenInputDir, relPath)

			file, err := os.Open(fullPath)
			if err != nil {
				continue
			}

			// Read all words from file
			var words []int
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				for _, word := range strings.Fields(scanner.Text()) {
					if idx, ok := wordToIndex[strings.TrimSpace(word)]; ok {
						words = append(words, idx)
					}
				}
			}
			file.Close()

			// Slide window of size n
			for i := 0; i <= len(words)-n; i++ {
				// Build ngram key
				var parts []string
				for j := 0; j < n; j++ {
					parts = append(parts, fmt.Sprintf("%d", words[i+j]))
				}
				ngramKey := strings.Join(parts, "|")

				// Get or create ngram index
				ngramIdx, exists := ngramToIndex[ngramKey]
				if !exists {
					ngramIdx = ngramCount
					ngramToIndex[ngramKey] = ngramIdx
					ngramCount++
				}

				// Track file
				if ngramToFiles[ngramIdx] == nil {
					ngramToFiles[ngramIdx] = make(map[int]struct{})
				}
				ngramToFiles[ngramIdx][fileIdx] = struct{}{}
			}

			if (fileIdx+1)%5000 == 0 {
				fmt.Printf("  Scanned %d / %d files (%d unique %d-grams)\n", fileIdx+1, len(filesList), ngramCount, n)
			}
		}

		fmt.Printf("  Found %d unique %d-grams\n", ngramCount, n)

		// Write uniqNgram.txt
		uniqNgramPath := filepath.Join(outputDir, fmt.Sprintf("uniq%dgram.txt", n))
		uniqNgramFile, err := os.Create(uniqNgramPath)
		if err != nil {
			return fmt.Errorf("could not create %s: %w", uniqNgramPath, err)
		}

		// We need to write in order of index, so build reverse map
		indexToNgram := make([]string, ngramCount)
		for ngram, idx := range ngramToIndex {
			indexToNgram[idx] = ngram
		}

		writer := bufio.NewWriter(uniqNgramFile)
		for _, ngram := range indexToNgram {
			writer.WriteString(ngram)
			writer.WriteString("\n")
		}
		writer.Flush()
		uniqNgramFile.Close()

		// Write Ngramindex.txt
		indexPath := filepath.Join(outputDir, fmt.Sprintf("%dgramindex.txt", n))
		indexFile, err := os.Create(indexPath)
		if err != nil {
			return fmt.Errorf("could not create %s: %w", indexPath, err)
		}

		writer = bufio.NewWriter(indexFile)
		for ngramIdx := 0; ngramIdx < ngramCount; ngramIdx++ {
			fileIndices := ngramToFiles[ngramIdx]
			indices := make([]int, 0, len(fileIndices))
			for fIdx := range fileIndices {
				indices = append(indices, fIdx)
			}
			sort.Ints(indices)

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("%d,[", ngramIdx))
			for j, fIdx := range indices {
				if j > 0 {
					sb.WriteString(",")
				}
				sb.WriteString(fmt.Sprintf("%d", fIdx))
			}
			sb.WriteString("]\n")
			writer.WriteString(sb.String())
		}
		writer.Flush()
		indexFile.Close()

		fmt.Printf("  Written: %s, %s\n", uniqNgramPath, indexPath)
	}

	fmt.Println("\nDone!")
	return nil
}

func buildNgramFreqCache(outputDir string, maxN int) error {
	fmt.Printf("Building n-gram frequency cache (2 to %d grams, min 2 occurrences)...\n", maxN)
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	if maxN < 2 {
		return fmt.Errorf("ngrams must be at least 2")
	}

	// Load settings.txt to get the original input path for token files
	settingsPath := filepath.Join(outputDir, "settings.txt")
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("could not read settings.txt (run -cache tokens first): %w", err)
	}

	var tokenInputDir string
	for _, line := range strings.Split(string(settingsData), "\n") {
		if strings.HasPrefix(line, "input=") {
			tokenInputDir = strings.TrimPrefix(line, "input=")
			break
		}
	}
	if tokenInputDir == "" {
		return fmt.Errorf("could not find input path in settings.txt")
	}
	fmt.Printf("Token files dir: %s\n", tokenInputDir)

	// Load uniq.txt into map (word -> index)
	uniqPath := filepath.Join(outputDir, "uniq.txt")
	uniqFile, err := os.Open(uniqPath)
	if err != nil {
		return fmt.Errorf("could not open uniq.txt: %w", err)
	}
	defer uniqFile.Close()

	wordToIndex := make(map[string]int)
	scanner := bufio.NewScanner(uniqFile)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	wordIdx := 0
	for scanner.Scan() {
		wordToIndex[scanner.Text()] = wordIdx
		wordIdx++
	}
	fmt.Printf("Loaded %d unique words\n", len(wordToIndex))

	// Load files.txt
	filesPath := filepath.Join(outputDir, "files.txt")
	filesFile, err := os.Open(filesPath)
	if err != nil {
		return fmt.Errorf("could not open files.txt: %w", err)
	}
	defer filesFile.Close()

	var filesList []string
	scanner = bufio.NewScanner(filesFile)
	for scanner.Scan() {
		filesList = append(filesList, scanner.Text())
	}
	fmt.Printf("Loaded %d files\n\n", len(filesList))

	for n := 2; n <= maxN; n++ {
		fmt.Printf("Processing %d-grams...\n", n)

		// Map: ngram string -> count (total occurrences across all files)
		ngramCount := make(map[string]int)

		for fileIdx, relPath := range filesList {
			fullPath := filepath.Join(tokenInputDir, relPath)

			file, err := os.Open(fullPath)
			if err != nil {
				continue
			}

			// Read all words from file
			var words []int
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				for _, word := range strings.Fields(scanner.Text()) {
					if idx, ok := wordToIndex[strings.TrimSpace(word)]; ok {
						words = append(words, idx)
					}
				}
			}
			file.Close()

			// Slide window of size n, count occurrences
			for i := 0; i <= len(words)-n; i++ {
				var parts []string
				for j := 0; j < n; j++ {
					parts = append(parts, fmt.Sprintf("%d", words[i+j]))
				}
				ngramKey := strings.Join(parts, "|")
				ngramCount[ngramKey]++
			}

			if (fileIdx+1)%5000 == 0 {
				fmt.Printf("  Scanned %d / %d files\n", fileIdx+1, len(filesList))
			}
		}

		// Filter to only keep ngrams with count >= 2
		type ngramFreq struct {
			ngram string
			count int
		}
		var filtered []ngramFreq
		for ngram, count := range ngramCount {
			if count >= 2 {
				filtered = append(filtered, ngramFreq{ngram, count})
			}
		}

		// Sort by count descending
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].count > filtered[j].count
		})

		fmt.Printf("  Found %d %d-grams appearing 2+ times (out of %d total)\n", len(filtered), n, len(ngramCount))

		// Write Ngramfreq.txt
		freqPath := filepath.Join(outputDir, fmt.Sprintf("%dgramfreq.txt", n))
		freqFile, err := os.Create(freqPath)
		if err != nil {
			return fmt.Errorf("could not create %s: %w", freqPath, err)
		}

		writer := bufio.NewWriter(freqFile)
		for _, nf := range filtered {
			writer.WriteString(fmt.Sprintf("%s,%d\n", nf.ngram, nf.count))
		}
		writer.Flush()
		freqFile.Close()

		fmt.Printf("  Written: %s\n", freqPath)

		// Clear memory
		ngramCount = nil
	}

	fmt.Println("\nDone!")
	return nil
}

func buildNgramFilesCache(outputDir string, maxN int) error {
	fmt.Printf("Building n-gram → files reverse index (2 to %d grams)...\n", maxN)
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	if maxN < 2 {
		return fmt.Errorf("ngrams must be at least 2")
	}

	// Load files.txt to get file count
	filesPath := filepath.Join(outputDir, "files.txt")
	filesFile, err := os.Open(filesPath)
	if err != nil {
		return fmt.Errorf("could not open files.txt: %w", err)
	}
	defer filesFile.Close()

	var fileCount int
	scanner := bufio.NewScanner(filesFile)
	for scanner.Scan() {
		fileCount++
	}
	fmt.Printf("Found %d files\n\n", fileCount)

	// For each n from 2 to maxN, read the Ngramindex.txt and create reverse mapping
	for n := 2; n <= maxN; n++ {
		fmt.Printf("Processing %d-grams...\n", n)

		// Read Ngramindex.txt
		indexPath := filepath.Join(outputDir, fmt.Sprintf("%dgramindex.txt", n))
		indexFile, err := os.Open(indexPath)
		if err != nil {
			fmt.Printf("  Skipping: could not open %s\n", indexPath)
			continue
		}

		// fileToNgrams[fileIndex] = list of ngram indices
		fileToNgrams := make(map[int][]int)

		scanner := bufio.NewScanner(indexFile)
		scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024) // 10MB buffer for long lines

		for scanner.Scan() {
			line := scanner.Text()
			// Format: ngramIndex,[fileIndex1,fileIndex2,...]
			// Find the comma separating index from array
			commaIdx := strings.Index(line, ",[")
			if commaIdx == -1 {
				continue
			}

			ngramIdxStr := line[:commaIdx]
			ngramIdx := 0
			fmt.Sscanf(ngramIdxStr, "%d", &ngramIdx)

			// Parse file indices from [1,2,3]
			arrayPart := line[commaIdx+1:]
			arrayPart = strings.TrimPrefix(arrayPart, "[")
			arrayPart = strings.TrimSuffix(arrayPart, "]")

			if arrayPart != "" {
				for _, fIdxStr := range strings.Split(arrayPart, ",") {
					var fIdx int
					fmt.Sscanf(fIdxStr, "%d", &fIdx)
					fileToNgrams[fIdx] = append(fileToNgrams[fIdx], ngramIdx)
				}
			}
		}
		indexFile.Close()

		// Write Ngramfiles.txt
		filesOutPath := filepath.Join(outputDir, fmt.Sprintf("%dgramfiles.txt", n))
		filesOutFile, err := os.Create(filesOutPath)
		if err != nil {
			return fmt.Errorf("could not create %s: %w", filesOutPath, err)
		}

		writer := bufio.NewWriter(filesOutFile)
		for fileIdx := 0; fileIdx < fileCount; fileIdx++ {
			ngrams := fileToNgrams[fileIdx]
			sort.Ints(ngrams)

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("%d,[", fileIdx))
			for j, nIdx := range ngrams {
				if j > 0 {
					sb.WriteString(",")
				}
				sb.WriteString(fmt.Sprintf("%d", nIdx))
			}
			sb.WriteString("]\n")
			writer.WriteString(sb.String())
		}
		writer.Flush()
		filesOutFile.Close()

		fmt.Printf("  Written: %s\n", filesOutPath)
	}

	fmt.Println("\nDone!")
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
