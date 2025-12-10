# TokenTrove

A high-performance CLI tool for extracting and converting text from various document formats. Perfect for preparing large document collections for tokenization, RAG pipelines, or text analysis.

## Features

- **Multi-format support**: PDF, DOCX, XLSX, XLS, PPTX, HTML, CSV, RTF, TXT, MD
- **Concurrent processing**: Process thousands of files in parallel with configurable worker count
- **Token mode**: Clean output with only words and spaces (no special characters, newlines, etc.)
- **Status checking**: See remaining files to convert by file type
- **Memory management**: Optional RAM limit to prevent excessive memory usage
- **Incremental processing**: Skip already-converted files (use `-r` to replace)

## Installation

```bash
go install github.com/openfluke/tokentrove@latest
```

Or clone and build:

```bash
git clone https://github.com/openfluke/tokentrove.git
cd tokentrove
go build .
```

## Workflow

### Step 1: Convert Documents to Token Text

Convert all supported files from an input directory to clean token text files:

```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -multi 1000 -type token
```

Use `-status` to check conversion progress:
```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -status
```

### Step 2: Build Token Cache

Extract all unique words and file list:

```bash
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache tokens
```

Creates:
- `settings.txt` - Stores the input path
- `uniq.txt` - All unique words, one per line, sorted
- `files.txt` - Relative paths of all scanned files

### Step 3: Build Word-to-File Index

Create an index mapping each word to the files containing it:

```bash
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache index
```

Creates `fileuniqindex.txt`:
```
0,[1,5,23]
```
Word 0 appears in files 1, 5, 23

### Step 4: Find Most Common Phrases (N-gram Frequency)

Discover the most repeated word sequences across all files:

```bash
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache ngramfreq -ngrams 15
```

Creates `2gramfreq.txt` through `15gramfreq.txt`, sorted by frequency:
```
5|23,1547
100|45,892
```
Word sequence 5|23 appears 1547 times (most common 2-gram)

Only keeps phrases appearing 2+ times.

---

## Optional: Build Full N-gram Index

If you need to know which FILES contain each n-gram (not just frequency):

```bash
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache ngrams -ngrams 15
```

Then build reverse index (file → ngrams):
```bash
go run . ngramfiles -cache /home/samuel/data/cache -ngrams 15
```

## Flags (process command)

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | (required) | Input directory to process |
| `-output` | `output.txt` | Output directory for converted files |
| `-type` | `text` | Processing type: `text` or `token` |
| `-multi` | `100` | Number of concurrent workers |
| `-r` | `false` | Replace existing files in output |
| `-ram-limit` | (none) | Soft memory limit (e.g., `1GB`, `512MB`) |
| `-status` | `false` | Show remaining files to convert |
| `-cache` | (none) | Cache mode: `tokens`, `index`, `ngrams`, or `ngramfreq` |
| `-ngrams` | `15` | Max n-gram size |

## Flags (ngramfiles command)

| Flag | Default | Description |
|------|---------|-------------|
| `-cache` | (required) | Cache directory containing ngram files |
| `-ngrams` | `15` | Max n-gram size |

## Processing Types

### `text` (default)
Preserves the original text formatting including newlines, tabs, and special characters.

### `token`
Cleans the extracted text for tokenization:
- Removes all `\n`, `\r`, `\t` characters
- Removes all special characters (punctuation, symbols)
- Keeps only letters, numbers, and spaces
- Collapses multiple spaces into single spaces

**Before (text mode):**
```
Hello, World!
This is a test...

Line 3.
```

**After (token mode):**
```
Hello World This is a test Line 3
```

## Supported Formats

| Extension | Description |
|-----------|-------------|
| `.pdf` | PDF documents |
| `.docx` | Microsoft Word (Open XML) |
| `.xlsx` | Microsoft Excel (Open XML) |
| `.xls` | Microsoft Excel (Legacy) |
| `.pptx` | Microsoft PowerPoint |
| `.html`, `.htm` | HTML documents |
| `.csv` | Comma-separated values |
| `.rtf` | Rich Text Format |
| `.txt`, `.md` | Plain text / Markdown |

## Output Structure

The tool preserves the directory structure from the input:

```
input/                      output/
├── folder1/                ├── folder1/
│   ├── doc.pdf       →     │   ├── doc.pdf.txt
│   └── data.xlsx     →     │   └── data.xlsx.txt
└── report.docx       →     └── report.docx.txt
```

## Logs

Two log files are created in the output directory:
- `ignored.txt` - Files with unsupported extensions
- `errors.txt` - Files that failed to process

## License

APACHE2
