package pkg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BuildTokenCache extracts all unique words and file list from input directory
func BuildTokenCache(inputDir, outputDir string) error {
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
			words := strings.Fields(line)
			for _, word := range words {
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

// BuildIndexCache creates word-to-file index mapping
func BuildIndexCache(inputDir, outputDir string) error {
	fmt.Println("Building index cache...")
	fmt.Printf("Cache dir: %s\n\n", outputDir)

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

	var filesList []string
	scanner = bufio.NewScanner(filesFile)
	fileIndex := 0
	for scanner.Scan() {
		relPath := scanner.Text()
		filesList = append(filesList, relPath)
		fileIndex++
	}
	fmt.Printf("Loaded %d files from files.txt\n", len(filesList))

	// Build word -> file indices mapping
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

	for wIdx := 0; wIdx < len(wordToIndex); wIdx++ {
		fileIndices, ok := wordToFiles[wIdx]
		if !ok || len(fileIndices) == 0 {
			writer.WriteString(fmt.Sprintf("%d,[]\n", wIdx))
			continue
		}

		indices := make([]int, 0, len(fileIndices))
		for fIdx := range fileIndices {
			indices = append(indices, fIdx)
		}
		sort.Ints(indices)

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

// BuildNgramCache builds n-gram sequences and their file mappings
func BuildNgramCache(outputDir string, maxN int) error {
	fmt.Printf("Building n-gram cache (2 to %d grams)...\n", maxN)
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	if maxN < 2 {
		return fmt.Errorf("ngrams must be at least 2")
	}

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

		ngramToIndex := make(map[string]int)
		ngramToFiles := make(map[int]map[int]struct{})
		ngramCount := 0

		for fileIdx, relPath := range filesList {
			fullPath := filepath.Join(tokenInputDir, relPath)

			file, err := os.Open(fullPath)
			if err != nil {
				continue
			}

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

			for i := 0; i <= len(words)-n; i++ {
				var parts []string
				for j := 0; j < n; j++ {
					parts = append(parts, fmt.Sprintf("%d", words[i+j]))
				}
				ngramKey := strings.Join(parts, "|")

				ngramIdx, exists := ngramToIndex[ngramKey]
				if !exists {
					ngramIdx = ngramCount
					ngramToIndex[ngramKey] = ngramIdx
					ngramCount++
				}

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

		uniqNgramPath := filepath.Join(outputDir, fmt.Sprintf("uniq%dgram.txt", n))
		uniqNgramFile, err := os.Create(uniqNgramPath)
		if err != nil {
			return fmt.Errorf("could not create %s: %w", uniqNgramPath, err)
		}

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

// BuildNgramFreqCache builds n-gram frequency cache (only phrases appearing 2+ times)
func BuildNgramFreqCache(outputDir string, maxN int) error {
	fmt.Printf("Building n-gram frequency cache (2 to %d grams, min 2 occurrences)...\n", maxN)
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	if maxN < 2 {
		return fmt.Errorf("ngrams must be at least 2")
	}

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

		ngramCount := make(map[string]int)

		for fileIdx, relPath := range filesList {
			fullPath := filepath.Join(tokenInputDir, relPath)

			file, err := os.Open(fullPath)
			if err != nil {
				continue
			}

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

		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].count > filtered[j].count
		})

		fmt.Printf("  Found %d %d-grams appearing 2+ times (out of %d total)\n", len(filtered), n, len(ngramCount))

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

		ngramCount = nil
	}

	fmt.Println("\nDone!")
	return nil
}

// BuildNgramFilesCache builds file-to-ngram reverse index
func BuildNgramFilesCache(outputDir string, maxN int) error {
	fmt.Printf("Building n-gram → files reverse index (2 to %d grams)...\n", maxN)
	fmt.Printf("Cache dir: %s\n\n", outputDir)

	if maxN < 2 {
		return fmt.Errorf("ngrams must be at least 2")
	}

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

	for n := 2; n <= maxN; n++ {
		fmt.Printf("Processing %d-grams...\n", n)

		indexPath := filepath.Join(outputDir, fmt.Sprintf("%dgramindex.txt", n))
		indexFile, err := os.Open(indexPath)
		if err != nil {
			fmt.Printf("  Skipping: could not open %s\n", indexPath)
			continue
		}

		fileToNgrams := make(map[int][]int)

		scanner := bufio.NewScanner(indexFile)
		scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			commaIdx := strings.Index(line, ",[")
			if commaIdx == -1 {
				continue
			}

			ngramIdxStr := line[:commaIdx]
			ngramIdx := 0
			fmt.Sscanf(ngramIdxStr, "%d", &ngramIdx)

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

// ShowStatus displays conversion status between input and output directories
func ShowStatus(inputDir, outputDir string) error {
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

	convertedCounts := make(map[string]int)
	err = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".txt") {
			return nil
		}
		original := strings.TrimSuffix(base, ".txt")
		ext := strings.ToLower(filepath.Ext(original))
		if ext == "" {
			ext = "(no extension)"
		}
		convertedCounts[ext]++
		return nil
	})
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	fmt.Println("\n=== Conversion Status ===")
	fmt.Printf("Input:  %s\n", inputDir)
	fmt.Printf("Output: %s\n\n", outputDir)

	totalInput := 0
	totalConverted := 0
	totalRemaining := 0

	allExts := make(map[string]bool)
	for ext := range inputCounts {
		allExts[ext] = true
	}

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

// Analyze runs all cache building steps in sequence: tokens, index, ngramfreq
func Analyze(inputDir, outputDir string, maxN int) error {
	fmt.Println("=== STEP 1/3: Building Token Cache ===")
	if err := BuildTokenCache(inputDir, outputDir); err != nil {
		return fmt.Errorf("token cache failed: %w", err)
	}

	fmt.Println("\n=== STEP 2/3: Building Word-to-File Index ===")
	if err := BuildIndexCache(inputDir, outputDir); err != nil {
		return fmt.Errorf("index cache failed: %w", err)
	}

	fmt.Println("\n=== STEP 3/3: Building N-gram Frequency Cache ===")
	if err := BuildNgramFreqCache(outputDir, maxN); err != nil {
		return fmt.Errorf("ngramfreq cache failed: %w", err)
	}

	fmt.Println("\n=== ANALYSIS COMPLETE ===")
	fmt.Printf("All results written to: %s\n", outputDir)
	return nil
}
