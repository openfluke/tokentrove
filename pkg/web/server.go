package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/template/html/v2"
	"github.com/gofiber/websocket/v2"
)

type CacheConfig struct {
	CacheDir   string
	ReportsDir string
	InputDir   string
	MaxN       int
	WordCount  int
	FileCount  int
}

type ReportJob struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Query       string    `json:"query"`
	ChainDepth  int       `json:"chainDepth"`
	MinN        int       `json:"minN"`
	MinFiles    int       `json:"minFiles"`
	SkipNumeric bool      `json:"skipNumeric"`
	TopN        int       `json:"topN"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	Total       int       `json:"total"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
	FilePath    string    `json:"filePath,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// RecurringChain represents text that repeats across files
type RecurringChain struct {
	Segments    []ChainSegment `json:"segments"`
	FullText    string         `json:"fullText"`
	Overlap     string         `json:"overlap"`
	FileCount   int            `json:"fileCount"`
	Files       []string       `json:"files"`
	TotalLength int            `json:"totalLength"`
}

type ChainSegment struct {
	Phrase   string `json:"phrase"`
	N        int    `json:"n"`
	Count    int    `json:"count"`
	StartIdx int    `json:"startIdx"`
	EndIdx   int    `json:"endIdx"`
}

var (
	reportJobs   = make(map[string]*ReportJob)
	reportJobsMu sync.RWMutex
	jobQueue     = make(chan *ReportJob, 100)
	globalConfig *CacheConfig
)

func StartServer(cacheDir, reportsDir string, maxN int, port int) error {
	config := &CacheConfig{CacheDir: cacheDir, ReportsDir: reportsDir, MaxN: maxN}
	globalConfig = config
	config.WordCount = countLines(filepath.Join(cacheDir, "uniq.txt"))
	config.FileCount = countLines(filepath.Join(cacheDir, "files.txt"))

	if data, err := os.ReadFile(filepath.Join(cacheDir, "settings.txt")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "input=") {
				config.InputDir = strings.TrimPrefix(line, "input=")
			}
		}
	}

	if reportsDir != "" {
		os.MkdirAll(reportsDir, 0755)
	}

	go reportWorker(config)

	engine := html.NewFileSystem(http.FS(viewsFS), ".html")
	app := fiber.New(fiber.Config{AppName: "TokenTrove", Views: engine})
	app.Use(cors.New())

	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/ws", websocket.New(func(c *websocket.Conn) { handleWebSocket(c, config) }))

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("views/index", fiber.Map{
			"Title": "TokenTrove", "WordCount": config.WordCount,
			"FileCount": config.FileCount, "MaxN": config.MaxN,
		})
	})

	api := app.Group("/api")
	api.Get("/stats", func(c *fiber.Ctx) error { return c.JSON(getStats(config)) })
	api.Get("/ngrams/:n", func(c *fiber.Ctx) error { return streamNgrams(c, config) })
	api.Get("/search", func(c *fiber.Ctx) error { return streamSearch(c, config) })
	api.Post("/report", func(c *fiber.Ctx) error { return queueReport(c, config) })
	api.Get("/reports", func(c *fiber.Ctx) error { return listReports(c) })
	api.Get("/report/:id", func(c *fiber.Ctx) error { return getReportStatus(c) })
	api.Get("/report/:id/view", func(c *fiber.Ctx) error { return viewReport(c) })

	fmt.Printf("\n🔮 TokenTrove Web Interface: http://localhost:%d\n\n", port)
	return app.Listen(fmt.Sprintf(":%d", port))
}

func countLines(path string) int {
	file, _ := os.Open(path)
	if file == nil {
		return 0
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		count++
	}
	return count
}

func getStats(config *CacheConfig) fiber.Map {
	ngramCounts := make(map[string]int)
	for n := 2; n <= config.MaxN; n++ {
		ngramCounts[fmt.Sprintf("%dgram", n)] = countLines(filepath.Join(config.CacheDir, fmt.Sprintf("%dgramfreq.txt", n)))
	}
	return fiber.Map{"type": "stats", "wordCount": config.WordCount, "fileCount": config.FileCount, "maxN": config.MaxN, "ngramCounts": ngramCounts}
}

func loadWordIndex(cacheDir string) map[int]string {
	index := make(map[int]string)
	file, _ := os.Open(filepath.Join(cacheDir, "uniq.txt"))
	if file == nil {
		return index
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for idx := 0; scanner.Scan(); idx++ {
		index[idx] = scanner.Text()
	}
	return index
}

func loadFileIndex(cacheDir string) []string {
	var files []string
	file, _ := os.Open(filepath.Join(cacheDir, "files.txt"))
	if file == nil {
		return files
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		files = append(files, scanner.Text())
	}
	return files
}

// NgramWithFiles stores n-gram data including which files contain it
type NgramWithFiles struct {
	indices []int
	words   []string
	count   int
	files   map[int]bool // file indices
}

// Load n-grams with file information from uniqNgram.txt + Ngramindex.txt files
func loadNgramsWithFiles(cacheDir string, n int, wordIndex map[int]string, limit int) []NgramWithFiles {
	var result []NgramWithFiles

	// Load n-gram definitions from uniqNgram.txt
	uniqPath := filepath.Join(cacheDir, fmt.Sprintf("uniq%dgram.txt", n))
	indexPath := filepath.Join(cacheDir, fmt.Sprintf("%dgramindex.txt", n))

	uniqFile, err := os.Open(uniqPath)
	if err != nil {
		// Fall back to freq file (no file info)
		return loadNgramsFreqOnly(cacheDir, n, wordIndex, limit)
	}
	defer uniqFile.Close()

	indexFile, err := os.Open(indexPath)
	if err != nil {
		return loadNgramsFreqOnly(cacheDir, n, wordIndex, limit)
	}
	defer indexFile.Close()

	uniqScanner := bufio.NewScanner(uniqFile)
	uniqScanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	indexScanner := bufio.NewScanner(indexFile)
	indexScanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	// Read both files in parallel - they have same line count
	for uniqScanner.Scan() && indexScanner.Scan() && (limit <= 0 || len(result) < limit) {
		ngramLine := uniqScanner.Text()  // Format: wordIdx1|wordIdx2|...
		filesLine := indexScanner.Text() // Format: fileIdx1,fileIdx2,...

		var indices []int
		var words []string
		for _, idxStr := range strings.Split(ngramLine, "|") {
			idx, _ := strconv.Atoi(idxStr)
			indices = append(indices, idx)
			if w, ok := wordIndex[idx]; ok {
				words = append(words, w)
			}
		}

		files := make(map[int]bool)
		for _, fIdxStr := range strings.Split(filesLine, ",") {
			if fIdxStr != "" {
				fIdx, _ := strconv.Atoi(fIdxStr)
				files[fIdx] = true
			}
		}

		result = append(result, NgramWithFiles{
			indices: indices,
			words:   words,
			count:   len(files),
			files:   files,
		})
	}
	return result
}

func loadNgramsFreqOnly(cacheDir string, n int, wordIndex map[int]string, limit int) []NgramWithFiles {
	var result []NgramWithFiles
	path := filepath.Join(cacheDir, fmt.Sprintf("%dgramfreq.txt", n))
	file, _ := os.Open(path)
	if file == nil {
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() && (limit <= 0 || len(result) < limit) {
		line := scanner.Text()
		commaIdx := strings.LastIndex(line, ",")
		if commaIdx == -1 {
			continue
		}
		ngramStr := line[:commaIdx]
		count, _ := strconv.Atoi(line[commaIdx+1:])

		var indices []int
		var words []string
		for _, idxStr := range strings.Split(ngramStr, "|") {
			idx, _ := strconv.Atoi(idxStr)
			indices = append(indices, idx)
			if w, ok := wordIndex[idx]; ok {
				words = append(words, w)
			}
		}
		result = append(result, NgramWithFiles{indices: indices, words: words, count: count, files: nil})
	}
	return result
}

func streamNgrams(c *fiber.Ctx, config *CacheConfig) error {
	n, _ := strconv.Atoi(c.Params("n"))
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))

	wordIndex := loadWordIndex(config.CacheDir)
	ngrams := loadNgramsFreqOnly(config.CacheDir, n, wordIndex, 0)

	total := len(ngrams)
	end := offset + limit
	if end > total {
		end = total
	}

	var result []fiber.Map
	if offset < total {
		for _, ng := range ngrams[offset:end] {
			result = append(result, fiber.Map{"ngram": strings.Join(ng.words, "|"), "count": ng.count, "words": ng.words})
		}
	}

	return c.JSON(fiber.Map{"type": "ngrams", "n": n, "total": total, "offset": offset, "ngrams": result})
}

func streamSearch(c *fiber.Ctx, config *CacheConfig) error {
	query := strings.ToLower(c.Query("q"))
	wordIndex := loadWordIndex(config.CacheDir)

	var wordMatches []fiber.Map
	for idx, word := range wordIndex {
		if strings.Contains(strings.ToLower(word), query) {
			wordMatches = append(wordMatches, fiber.Map{"index": idx, "word": word})
			if len(wordMatches) >= 20 {
				break
			}
		}
	}

	ngramMatches := make(map[int][]fiber.Map)
	for n := 2; n <= config.MaxN; n++ {
		ngrams := loadNgramsFreqOnly(config.CacheDir, n, wordIndex, 500)
		for _, ng := range ngrams {
			if strings.Contains(strings.ToLower(strings.Join(ng.words, " ")), query) {
				ngramMatches[n] = append(ngramMatches[n], fiber.Map{"words": ng.words, "count": ng.count})
				if len(ngramMatches[n]) >= 10 {
					break
				}
			}
		}
	}

	return c.JSON(fiber.Map{"type": "search", "words": wordMatches, "ngrams": ngramMatches})
}

func queueReport(c *fiber.Ctx, config *CacheConfig) error {
	var req struct {
		Type        string `json:"type"`
		Query       string `json:"query"`
		ChainDepth  int    `json:"chainDepth"`
		MinN        int    `json:"minN"`
		MinFiles    int    `json:"minFiles"`
		SkipNumeric bool   `json:"skipNumeric"`
		TopN        int    `json:"topN"`
	}
	c.BodyParser(&req)

	now := time.Now()
	name := fmt.Sprintf("%s - %s", strings.Title(strings.ReplaceAll(req.Type, "_", " ")), now.Format("Jan 2 15:04"))
	desc := ""

	switch req.Type {
	case "top_ngrams":
		desc = "Top 100 most frequent n-grams for each size"
	case "search":
		desc = fmt.Sprintf("Search results for '%s'", req.Query)
	case "recurring_text":
		if req.MinN == 0 {
			req.MinN = 5
		}
		if req.MinFiles == 0 {
			req.MinFiles = 2
		}
		desc = fmt.Sprintf("Find text (min %d-grams) appearing in %d+ files", req.MinN, req.MinFiles)
	case "linked_ngrams":
		if req.MinN == 0 {
			req.MinN = 5
		}
		desc = fmt.Sprintf("N-grams with most chain connections (min %d-grams)", req.MinN)
	case "best_chains":
		if req.MinN == 0 {
			req.MinN = 3
		}
		desc = "Longest recurring chains sorted by (files × length)"
	}

	job := &ReportJob{
		ID:          fmt.Sprintf("%d", now.UnixNano()),
		Type:        req.Type,
		Name:        name,
		Description: desc,
		Query:       req.Query,
		ChainDepth:  req.ChainDepth,
		MinN:        req.MinN,
		MinFiles:    req.MinFiles,
		SkipNumeric: req.SkipNumeric,
		TopN:        req.TopN,
		Status:      "queued",
		CreatedAt:   now,
	}

	reportJobsMu.Lock()
	reportJobs[job.ID] = job
	reportJobsMu.Unlock()

	select {
	case jobQueue <- job:
	default:
		job.Status = "error"
		job.Error = "queue full"
	}

	return c.JSON(job)
}

func listReports(c *fiber.Ctx) error {
	reportJobsMu.RLock()
	var jobs []*ReportJob
	for _, j := range reportJobs {
		jobs = append(jobs, j)
	}
	reportJobsMu.RUnlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return c.JSON(fiber.Map{"jobs": jobs})
}

func getReportStatus(c *fiber.Ctx) error {
	reportJobsMu.RLock()
	job, ok := reportJobs[c.Params("id")]
	reportJobsMu.RUnlock()
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "not found"})
	}
	return c.JSON(job)
}

func viewReport(c *fiber.Ctx) error {
	reportJobsMu.RLock()
	job, ok := reportJobs[c.Params("id")]
	reportJobsMu.RUnlock()
	if !ok || job.FilePath == "" {
		return c.Status(404).JSON(fiber.Map{"error": "not found"})
	}
	data, _ := os.ReadFile(job.FilePath)
	var result interface{}
	if json.Unmarshal(data, &result) == nil {
		return c.JSON(fiber.Map{"job": job, "data": result})
	}
	return c.JSON(fiber.Map{"job": job, "text": string(data)})
}

func reportWorker(config *CacheConfig) {
	for job := range jobQueue {
		processReport(job, config)
	}
}

func updateProgress(job *ReportJob, progress, total int, msg string) {
	reportJobsMu.Lock()
	job.Progress, job.Total, job.Message, job.Status = progress, total, msg, "running"
	reportJobsMu.Unlock()
}

func processReport(job *ReportJob, config *CacheConfig) {
	reportJobsMu.Lock()
	job.Status = "running"
	job.Message = "Starting..."
	reportJobsMu.Unlock()

	outPath := filepath.Join(config.ReportsDir, fmt.Sprintf("report_%s.json", job.ID))
	var err error

	switch job.Type {
	case "top_ngrams":
		err = generateTopNgramsReport(job, config, outPath)
	case "search":
		err = generateSearchReport(job, config, outPath)
	case "recurring_text":
		err = generateRecurringTextReport(job, config, outPath)
	case "linked_ngrams":
		err = generateLinkedNgramsReport(job, config, outPath)
	case "best_chains":
		err = generateBestChainsReport(job, config, outPath)
	default:
		err = fmt.Errorf("unknown type")
	}

	reportJobsMu.Lock()
	if err != nil {
		job.Status, job.Error = "error", err.Error()
	} else {
		job.Status, job.FilePath, job.Progress = "done", outPath, job.Total
	}
	reportJobsMu.Unlock()
}

func generateTopNgramsReport(job *ReportJob, config *CacheConfig, outPath string) error {
	wordIndex := loadWordIndex(config.CacheDir)
	result := make(map[string][]map[string]interface{})

	for n := 2; n <= config.MaxN; n++ {
		updateProgress(job, n-2, config.MaxN-2, fmt.Sprintf("Processing %d-grams", n))
		ngrams := loadNgramsFreqOnly(config.CacheDir, n, wordIndex, 100)
		key := fmt.Sprintf("%dgrams", n)
		for _, ng := range ngrams {
			result[key] = append(result[key], map[string]interface{}{"phrase": strings.Join(ng.words, " "), "count": ng.count})
		}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(outPath, data, 0644)
}

func generateSearchReport(job *ReportJob, config *CacheConfig, outPath string) error {
	query := strings.ToLower(job.Query)
	wordIndex := loadWordIndex(config.CacheDir)
	result := make(map[string][]map[string]interface{})

	for n := 2; n <= config.MaxN; n++ {
		updateProgress(job, n-2, config.MaxN-2, fmt.Sprintf("Searching %d-grams", n))
		ngrams := loadNgramsFreqOnly(config.CacheDir, n, wordIndex, 0)
		key := fmt.Sprintf("%dgrams", n)
		count := 0
		for _, ng := range ngrams {
			if strings.Contains(strings.ToLower(strings.Join(ng.words, " ")), query) {
				result[key] = append(result[key], map[string]interface{}{"phrase": strings.Join(ng.words, " "), "count": ng.count})
				count++
				if count >= 50 {
					break
				}
			}
		}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(outPath, data, 0644)
}

// generateRecurringTextReport finds text patterns that repeat across files
func generateRecurringTextReport(job *ReportJob, config *CacheConfig, outPath string) error {
	wordIndex := loadWordIndex(config.CacheDir)
	fileNames := loadFileIndex(config.CacheDir)
	minN := job.MinN
	if minN < 3 {
		minN = 5
	}

	updateProgress(job, 0, 100, "Loading n-grams with file data...")

	// Load n-grams with file information
	type ngramEntry struct {
		words []string
		n     int
		files map[int]bool
		count int
	}

	// Map: last 2 words -> list of n-grams ending with those words
	endsWith := make(map[string][]ngramEntry)
	// Map: first 2 words -> list of n-grams starting with those words
	startsWith := make(map[string][]ngramEntry)

	totalLoaded := 0
	for n := minN; n <= config.MaxN; n++ {
		updateProgress(job, (n-minN)*10, 100, fmt.Sprintf("Loading %d-grams...", n))

		ngrams := loadNgramsWithFiles(config.CacheDir, n, wordIndex, 200) // Top 200 per n
		for _, ng := range ngrams {
			if len(ng.words) < 2 {
				continue
			}

			// Skip numeric-only n-grams if requested
			if job.SkipNumeric && isNumericOnly(ng.words) {
				continue
			}

			entry := ngramEntry{words: ng.words, n: n, files: ng.files, count: ng.count}

			endKey := strings.Join(ng.words[len(ng.words)-2:], " ")
			endsWith[endKey] = append(endsWith[endKey], entry)

			startKey := strings.Join(ng.words[:2], " ")
			startsWith[startKey] = append(startsWith[startKey], entry)

			totalLoaded++
		}
	}

	updateProgress(job, 50, 100, fmt.Sprintf("Loaded %d n-grams, finding chains...", totalLoaded))

	// Find chains where n-gram A ends with same words that n-gram B starts with
	// AND they share files
	var chains []RecurringChain
	seen := make(map[string]bool)

	for endKey, endList := range endsWith {
		startList, ok := startsWith[endKey]
		if !ok {
			continue
		}

		for _, from := range endList {
			for _, to := range startList {
				fromPhrase := strings.Join(from.words, " ")
				toPhrase := strings.Join(to.words, " ")

				// Skip if same n-gram
				if fromPhrase == toPhrase {
					continue
				}

				// Find file intersection
				var sharedFiles []int
				if from.files != nil && to.files != nil {
					for fIdx := range from.files {
						if to.files[fIdx] {
							sharedFiles = append(sharedFiles, fIdx)
						}
					}
				}

				// Skip if no shared files (or no file data)
				fileCount := len(sharedFiles)
				if fileCount == 0 && from.files != nil {
					continue
				}
				if from.files == nil {
					// No file data available, estimate based on counts
					fileCount = min(from.count, to.count)
				}

				// Skip if below minimum file count
				minFiles := job.MinFiles
				if minFiles < 2 {
					minFiles = 2
				}
				if fileCount < minFiles {
					continue
				}

				// Create unique key to avoid duplicates
				chainKey := fromPhrase + " | " + toPhrase
				if seen[chainKey] {
					continue
				}
				seen[chainKey] = true

				// Build full text by merging overlapping parts
				// from.words ends with [overlap1, overlap2]
				// to.words starts with [overlap1, overlap2, rest...]
				overlap := strings.Join(from.words[len(from.words)-2:], " ")
				fullText := fromPhrase + " " + strings.Join(to.words[2:], " ")
				fullWords := strings.Split(fullText, " ")

				// Calculate segment positions in fullText
				// Segment 1 (from): words 0 to len(from.words)-1
				// Overlap: words len(from.words)-2 to len(from.words)-1
				// Segment 2 (to): words len(from.words)-2 to end

				// Convert file indices to names
				var fileNameList []string
				for i, fIdx := range sharedFiles {
					if i >= 20 { // Limit to 20 files shown
						fileNameList = append(fileNameList, fmt.Sprintf("... and %d more", len(sharedFiles)-20))
						break
					}
					if fIdx < len(fileNames) {
						fileNameList = append(fileNameList, fileNames[fIdx])
					}
				}

				chains = append(chains, RecurringChain{
					Segments: []ChainSegment{
						{Phrase: fromPhrase, N: from.n, Count: from.count, StartIdx: 0, EndIdx: from.n - 1},
						{Phrase: toPhrase, N: to.n, Count: to.count, StartIdx: from.n - 2, EndIdx: len(fullWords) - 1},
					},
					FullText:    fullText,
					Overlap:     overlap,
					FileCount:   fileCount,
					Files:       fileNameList,
					TotalLength: len(fullWords),
				})

				if len(chains) >= 500 {
					break
				}
			}
			if len(chains) >= 500 {
				break
			}
		}
		if len(chains) >= 500 {
			break
		}
	}

	// Sort by file count (most recurring first), then by length
	sort.Slice(chains, func(i, j int) bool {
		if chains[i].FileCount != chains[j].FileCount {
			return chains[i].FileCount > chains[j].FileCount
		}
		return chains[i].TotalLength > chains[j].TotalLength
	})

	if len(chains) > 100 {
		chains = chains[:100]
	}

	updateProgress(job, 100, 100, "Writing report...")

	result := map[string]interface{}{
		"type":       "recurring_text",
		"minN":       minN,
		"chainCount": len(chains),
		"chains":     chains,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(outPath, data, 0644)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isNumericOnly checks if n-gram is mostly numeric junk (census data, spreadsheets)
// Returns true if:
// - All words are pure numbers/scientific notation
// - OR words start with numbers (like "0inhouseholds", "7spouse")
// - OR more than half the words are numeric
func isNumericOnly(words []string) bool {
	if len(words) == 0 {
		return true
	}

	numericCount := 0
	for _, w := range words {
		if w == "" {
			continue
		}

		// Check if word is pure numeric
		isPureNum := true
		for _, c := range w {
			if (c < '0' || c > '9') && c != 'e' && c != '.' && c != '-' && c != '+' {
				isPureNum = false
				break
			}
		}
		if isPureNum {
			numericCount++
			continue
		}

		// Check if word starts with a digit (like "0inhouseholds", "7spouse", "28019")
		if len(w) > 0 && w[0] >= '0' && w[0] <= '9' {
			numericCount++
			continue
		}
	}

	// Filter if more than 60% of words are numeric/semi-numeric
	return float64(numericCount)/float64(len(words)) > 0.6
}

// NgramChainResult represents an n-gram chain sequence with file occurrence data
type NgramChainResult struct {
	Chain       []ChainNode `json:"chain"`
	FullText    string      `json:"fullText"`
	ChainLength int         `json:"chainLength"`
	FileCount   int         `json:"fileCount"`
	Files       []string    `json:"files"`
}

type ChainNode struct {
	Phrase string `json:"phrase"`
	N      int    `json:"n"`
	Count  int    `json:"count"`
}

// generateLinkedNgramsReport finds chains of n-grams (A→B→C) that form sentences across files
func generateLinkedNgramsReport(job *ReportJob, config *CacheConfig, outPath string) error {
	wordIndex := loadWordIndex(config.CacheDir)
	fileNames := loadFileIndex(config.CacheDir)
	minN := job.MinN
	if minN < 3 {
		minN = 5
	}

	updateProgress(job, 0, 100, "Loading n-grams with file data...")

	// Load n-grams with file information
	type ngramEntry struct {
		words []string
		n     int
		files map[int]bool
		count int
	}

	// Map: last 2 words -> list of n-grams ending with those words
	endsWith := make(map[string][]ngramEntry)
	// Map: first 2 words -> list of n-grams starting with those words
	startsWith := make(map[string][]ngramEntry)

	totalLoaded := 0
	for n := minN; n <= config.MaxN; n++ {
		updateProgress(job, (n-minN)*15, 100, fmt.Sprintf("Loading %d-grams...", n))

		ngrams := loadNgramsWithFiles(config.CacheDir, n, wordIndex, 300)
		for _, ng := range ngrams {
			if len(ng.words) < 2 {
				continue
			}

			// Skip numeric-only n-grams if requested
			if job.SkipNumeric && isNumericOnly(ng.words) {
				continue
			}

			entry := ngramEntry{words: ng.words, n: n, files: ng.files, count: ng.count}

			endKey := strings.Join(ng.words[len(ng.words)-2:], " ")
			endsWith[endKey] = append(endsWith[endKey], entry)

			startKey := strings.Join(ng.words[:2], " ")
			startsWith[startKey] = append(startsWith[startKey], entry)

			totalLoaded++
		}
	}

	updateProgress(job, 50, 100, fmt.Sprintf("Building chains from %d n-grams...", totalLoaded))

	// Find chains: A → B → C (3 n-grams linked together)
	var chains []NgramChainResult
	seen := make(map[string]bool)

	minFiles := job.MinFiles
	if minFiles < 2 {
		minFiles = 2
	}

	for endKey, endList := range endsWith {
		midList, ok := startsWith[endKey]
		if !ok {
			continue
		}

		// For each A that ends with endKey
		for _, from := range endList {
			// For each B that starts with endKey
			for _, mid := range midList {
				fromPhrase := strings.Join(from.words, " ")
				midPhrase := strings.Join(mid.words, " ")

				if fromPhrase == midPhrase {
					continue
				}

				// Find intersection of files between A and B
				var sharedFilesAB []int
				if from.files != nil && mid.files != nil {
					for fIdx := range from.files {
						if mid.files[fIdx] {
							sharedFilesAB = append(sharedFilesAB, fIdx)
						}
					}
				}

				if len(sharedFilesAB) < minFiles && from.files != nil {
					continue
				}

				// Try to find C that links from B
				midEndKey := strings.Join(mid.words[len(mid.words)-2:], " ")
				toList, hasC := startsWith[midEndKey]

				if hasC {
					// Build 3-way chains (A → B → C)
					for _, to := range toList {
						toPhrase := strings.Join(to.words, " ")
						if toPhrase == midPhrase || toPhrase == fromPhrase {
							continue
						}

						// Find files shared by all 3
						var sharedFilesABC []int
						if to.files != nil {
							for _, fIdx := range sharedFilesAB {
								if to.files[fIdx] {
									sharedFilesABC = append(sharedFilesABC, fIdx)
								}
							}
						}

						fileCount := len(sharedFilesABC)
						if fileCount < minFiles && to.files != nil {
							continue
						}
						if to.files == nil {
							fileCount = min(min(from.count, mid.count), to.count)
						}

						chainKey := fromPhrase + "|" + midPhrase + "|" + toPhrase
						if seen[chainKey] {
							continue
						}
						seen[chainKey] = true

						// Build full text
						fullText := fromPhrase + " " + strings.Join(mid.words[2:], " ") + " " + strings.Join(to.words[2:], " ")

						var fileList []string
						for i, fIdx := range sharedFilesABC {
							if i >= 10 {
								fileList = append(fileList, fmt.Sprintf("...+%d more", len(sharedFilesABC)-10))
								break
							}
							if fIdx < len(fileNames) {
								fileList = append(fileList, fileNames[fIdx])
							}
						}

						chains = append(chains, NgramChainResult{
							Chain: []ChainNode{
								{Phrase: fromPhrase, N: from.n, Count: from.count},
								{Phrase: midPhrase, N: mid.n, Count: mid.count},
								{Phrase: toPhrase, N: to.n, Count: to.count},
							},
							FullText:    fullText,
							ChainLength: 3,
							FileCount:   fileCount,
							Files:       fileList,
						})

						if len(chains) >= 300 {
							break
						}
					}
				}

				if len(chains) >= 300 {
					break
				}
			}
			if len(chains) >= 300 {
				break
			}
		}
		if len(chains) >= 300 {
			break
		}
	}

	// Sort by file count descending
	sort.Slice(chains, func(i, j int) bool {
		if chains[i].FileCount != chains[j].FileCount {
			return chains[i].FileCount > chains[j].FileCount
		}
		return chains[i].ChainLength > chains[j].ChainLength
	})

	if len(chains) > 100 {
		chains = chains[:100]
	}

	updateProgress(job, 100, 100, "Writing report...")

	result := map[string]interface{}{
		"type":       "linked_ngrams",
		"minN":       minN,
		"minFiles":   minFiles,
		"chainCount": len(chains),
		"chains":     chains,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(outPath, data, 0644)
}

// BestChain represents a chain scored by (files × length)
type BestChain struct {
	Chain     []ChainNode `json:"chain"`
	FullText  string      `json:"fullText"`
	WordCount int         `json:"wordCount"`
	FileCount int         `json:"fileCount"`
	Score     int         `json:"score"`
	Files     []string    `json:"files"`
}

// generateBestChainsReport finds the longest chains sorted by (files × length)
func generateBestChainsReport(job *ReportJob, config *CacheConfig, outPath string) error {
	wordIndex := loadWordIndex(config.CacheDir)
	fileNames := loadFileIndex(config.CacheDir)
	minN := job.MinN
	if minN < 2 {
		minN = 3
	}
	topN := job.TopN
	if topN <= 0 {
		topN = 100
	}

	updateProgress(job, 0, 100, fmt.Sprintf("Loading top %d n-grams with file data...", topN))

	type ngramEntry struct {
		words []string
		n     int
		files map[int]bool
		count int
	}

	endsWith := make(map[string][]ngramEntry)
	startsWith := make(map[string][]ngramEntry)

	// Parallel loading of n-grams
	type loadResult struct {
		entries []ngramEntry
		n       int
	}
	resultChan := make(chan loadResult, config.MaxN-minN+1)
	var wg sync.WaitGroup

	for n := minN; n <= config.MaxN; n++ {
		wg.Add(1)
		go func(nSize int) {
			defer wg.Done()
			ngrams := loadNgramsWithFiles(config.CacheDir, nSize, wordIndex, topN)
			var entries []ngramEntry
			for _, ng := range ngrams {
				if len(ng.words) < 2 || (job.SkipNumeric && isNumericOnly(ng.words)) {
					continue
				}
				entries = append(entries, ngramEntry{words: ng.words, n: nSize, files: ng.files, count: ng.count})
			}
			resultChan <- loadResult{entries: entries, n: nSize}
		}(n)
	}

	// Wait and collect results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	loaded := 0
	total := config.MaxN - minN + 1
	for result := range resultChan {
		loaded++
		updateProgress(job, loaded*40/total, 100, fmt.Sprintf("Loaded %d-grams (top %d)...", result.n, topN))
		for _, entry := range result.entries {
			endKey := strings.Join(entry.words[len(entry.words)-2:], " ")
			endsWith[endKey] = append(endsWith[endKey], entry)

			startKey := strings.Join(entry.words[:2], " ")
			startsWith[startKey] = append(startsWith[startKey], entry)
		}
	}

	updateProgress(job, 50, 100, "Building longest chains...")

	// Build chains by following links as far as possible
	var bestChains []BestChain
	seen := make(map[string]bool)

	// For each n-gram, try to build the longest chain starting from it
	for _, startList := range endsWith {
		for _, start := range startList {
			chain := []ngramEntry{start}
			sharedFiles := make(map[int]bool)
			for f := range start.files {
				sharedFiles[f] = true
			}

			// Follow the chain forward
			current := start
			for depth := 0; depth < 10; depth++ { // Max 10 links
				endKey := strings.Join(current.words[len(current.words)-2:], " ")
				nextList, ok := startsWith[endKey]
				if !ok || len(nextList) == 0 {
					break
				}

				// Find best next n-gram (prefer shared files, fallback to highest count)
				var bestNext *ngramEntry
				var bestScore int
				for i := range nextList {
					next := &nextList[i]
					if strings.Join(next.words, " ") == strings.Join(current.words, " ") {
						continue
					}

					// Score: shared files * 1000 + count (prioritize file overlap, but use count as tiebreaker)
					shared := 0
					if len(sharedFiles) > 0 && next.files != nil {
						for f := range sharedFiles {
							if next.files[f] {
								shared++
							}
						}
					}
					score := shared*1000 + next.count
					if bestNext == nil || score > bestScore {
						bestScore = score
						bestNext = next
					}
				}

				if bestNext == nil {
					break
				}

				chain = append(chain, *bestNext)
				// Update shared files (keep intersection, or just use next's files if we had none)
				if len(sharedFiles) > 0 && bestNext.files != nil {
					newShared := make(map[int]bool)
					for f := range sharedFiles {
						if bestNext.files[f] {
							newShared[f] = true
						}
					}
					sharedFiles = newShared
				} else if bestNext.files != nil {
					sharedFiles = bestNext.files
				}
				current = *bestNext
			}

			if len(chain) < 2 {
				continue
			}

			// Build full text
			fullText := strings.Join(chain[0].words, " ")
			for i := 1; i < len(chain); i++ {
				fullText += " " + strings.Join(chain[i].words[2:], " ")
			}

			// Create chain key for deduplication
			chainKey := fullText
			if seen[chainKey] {
				continue
			}
			seen[chainKey] = true

			wordCount := len(strings.Split(fullText, " "))
			fileCount := len(sharedFiles)
			score := wordCount * fileCount

			var fileList []string
			count := 0
			for f := range sharedFiles {
				if count >= 10 {
					fileList = append(fileList, fmt.Sprintf("...+%d more", fileCount-10))
					break
				}
				if f < len(fileNames) {
					fileList = append(fileList, fileNames[f])
				}
				count++
			}

			nodes := make([]ChainNode, len(chain))
			for i, c := range chain {
				nodes[i] = ChainNode{Phrase: strings.Join(c.words, " "), N: c.n, Count: c.count}
			}

			bestChains = append(bestChains, BestChain{
				Chain:     nodes,
				FullText:  fullText,
				WordCount: wordCount,
				FileCount: fileCount,
				Score:     score,
				Files:     fileList,
			})
		}
	}

	// Sort by score (files × length) descending
	sort.Slice(bestChains, func(i, j int) bool {
		return bestChains[i].Score > bestChains[j].Score
	})

	if len(bestChains) > 100 {
		bestChains = bestChains[:100]
	}

	updateProgress(job, 100, 100, "Writing report...")

	result := map[string]interface{}{
		"type":       "best_chains",
		"minN":       minN,
		"chainCount": len(bestChains),
		"chains":     bestChains,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(outPath, data, 0644)
}

func handleWebSocket(c *websocket.Conn, config *CacheConfig) {
	defer c.Close()
	stats := getStats(config)
	statsJSON, _ := json.Marshal(stats)
	c.WriteMessage(websocket.TextMessage, statsJSON)

	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			break
		}

		var req map[string]interface{}
		json.Unmarshal(msg, &req)

		action, _ := req["action"].(string)
		var response fiber.Map

		switch action {
		case "stats":
			response = getStats(config)
		case "ngrams":
			n := int(req["n"].(float64))
			limit := int(req["limit"].(float64))
			offset := int(req["offset"].(float64))
			response = streamNgramsWS(config, n, limit, offset)
		case "search":
			query, _ := req["query"].(string)
			response = streamSearchWS(config, query)
		default:
			response = fiber.Map{"error": "unknown"}
		}

		responseJSON, _ := json.Marshal(response)
		c.WriteMessage(websocket.TextMessage, responseJSON)
	}
}

func streamNgramsWS(config *CacheConfig, n, limit, offset int) fiber.Map {
	wordIndex := loadWordIndex(config.CacheDir)
	ngrams := loadNgramsFreqOnly(config.CacheDir, n, wordIndex, 0)

	total := len(ngrams)
	end := offset + limit
	if end > total {
		end = total
	}

	var result []fiber.Map
	if offset < total {
		for _, ng := range ngrams[offset:end] {
			result = append(result, fiber.Map{"ngram": strings.Join(ng.words, "|"), "count": ng.count, "words": ng.words})
		}
	}

	return fiber.Map{"type": "ngrams", "n": n, "total": total, "offset": offset, "ngrams": result}
}

func streamSearchWS(config *CacheConfig, query string) fiber.Map {
	query = strings.ToLower(query)
	wordIndex := loadWordIndex(config.CacheDir)

	var wordMatches []fiber.Map
	for idx, word := range wordIndex {
		if strings.Contains(strings.ToLower(word), query) {
			wordMatches = append(wordMatches, fiber.Map{"index": idx, "word": word})
			if len(wordMatches) >= 20 {
				break
			}
		}
	}

	ngramMatches := make(map[int][]fiber.Map)
	for n := 2; n <= config.MaxN; n++ {
		ngrams := loadNgramsFreqOnly(config.CacheDir, n, wordIndex, 500)
		for _, ng := range ngrams {
			if strings.Contains(strings.ToLower(strings.Join(ng.words, " ")), query) {
				ngramMatches[n] = append(ngramMatches[n], fiber.Map{"words": ng.words, "count": ng.count})
				if len(ngramMatches[n]) >= 10 {
					break
				}
			}
		}
	}

	return fiber.Map{"type": "search", "words": wordMatches, "ngrams": ngramMatches}
}
