package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/openfluke/tokentrove/pkg"
	"github.com/openfluke/tokentrove/pkg/web"
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
		processType := processCmd.String("type", "text", "Type: 'text', 'token', or 'lowercase'")
		concurrency := processCmd.Int("multi", 100, "Number of concurrent workers")
		replace := processCmd.Bool("r", false, "Replace existing files in output")
		ramLimitStr := processCmd.String("ram-limit", "", "Soft memory limit (e.g., '1GB', '512MB')")
		statusOnly := processCmd.Bool("status", false, "Show remaining files to convert by file type")
		cacheMode := processCmd.String("cache", "", "Cache mode: 'tokens', 'index', 'ngrams', or 'ngramfreq'")
		ngramMax := processCmd.Int("ngrams", 15, "Max n-gram size")

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
				if err := pkg.BuildTokenCache(*inputDir, *outputFile); err != nil {
					fmt.Printf("Error building token cache: %v\n", err)
					os.Exit(1)
				}
			case "index":
				if err := pkg.BuildIndexCache(*inputDir, *outputFile); err != nil {
					fmt.Printf("Error building index cache: %v\n", err)
					os.Exit(1)
				}
			case "ngrams":
				if err := pkg.BuildNgramCache(*outputFile, *ngramMax); err != nil {
					fmt.Printf("Error building ngram cache: %v\n", err)
					os.Exit(1)
				}
			case "ngramfiles":
				if err := pkg.BuildNgramFilesCache(*outputFile, *ngramMax); err != nil {
					fmt.Printf("Error building ngramfiles cache: %v\n", err)
					os.Exit(1)
				}
			case "ngramfreq":
				if err := pkg.BuildNgramFreqCache(*outputFile, *ngramMax); err != nil {
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
			if err := pkg.ShowStatus(*inputDir, *outputFile); err != nil {
				fmt.Printf("Error getting status: %v\n", err)
				os.Exit(1)
			}
			return
		}

		ramLimit, err := pkg.ParseMemoryLimit(*ramLimitStr)
		if err != nil {
			fmt.Printf("Error checking RAM limit: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Starting process (Type: %s, Workers: %d, Replace: %v, RAM Limit: %s)...\n", *processType, *concurrency, *replace, *ramLimitStr)

		if err := pkg.RunProcess(*inputDir, *outputFile, *processType, *concurrency, *replace, ramLimit); err != nil {
			fmt.Printf("Error processing files: %v\n", err)
			os.Exit(1)
		}

	case "analyze":
		analyzeCmd := flag.NewFlagSet("analyze", flag.ExitOnError)
		inputDir := analyzeCmd.String("input", "", "Input directory with token files (required)")
		outputDir := analyzeCmd.String("output", "", "Output cache directory (required)")
		reportsDir := analyzeCmd.String("reports", "", "Reports output directory")
		ngramMax := analyzeCmd.Int("ngrams", 15, "Max n-gram size for frequency analysis")
		host := analyzeCmd.Bool("host", false, "Start web server to browse cache")
		port := analyzeCmd.Int("port", 3000, "Web server port (used with -host)")

		analyzeCmd.Parse(os.Args[2:])

		if *inputDir == "" || *outputDir == "" {
			fmt.Println("Error: -input and -output are required")
			analyzeCmd.PrintDefaults()
			os.Exit(1)
		}

		// If hosting, start web server
		if *host {
			if err := web.StartServer(*outputDir, *reportsDir, *ngramMax, *port); err != nil {
				fmt.Printf("Error starting web server: %v\n", err)
				os.Exit(1)
			}
			return
		}

		// Otherwise run analysis
		if err := pkg.Analyze(*inputDir, *outputDir, *ngramMax); err != nil {
			fmt.Printf("Error during analysis: %v\n", err)
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

		if err := pkg.BuildNgramFilesCache(*cacheDir, *ngramMax); err != nil {
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
	fmt.Println("  analyze      Run all analysis steps (tokens, index, ngramfreq) in one command")
	fmt.Println("  ngramfiles   Build file → ngram reverse index from existing ngram cache")
	fmt.Println("\nRun 'tokentrove <command> -h' for more information.")
}
