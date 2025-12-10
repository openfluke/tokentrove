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
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -multi 1000 -type lowercase
```

Use `-status` to check conversion progress:
```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -status
```

### Step 2: Run Full Analysis (One Command)

Run all cache building steps automatically:

```bash
go run . analyze -input /home/samuel/data/token -output /home/samuel/data/cache -ngrams 15
```

This runs:
1. **Token cache** → `uniq.txt`, `files.txt`, `settings.txt`
2. **Word-to-file index** → `fileuniqindex.txt`
3. **N-gram frequency** → `2gramfreq.txt` through `15gramfreq.txt`

---

## Manual Steps (Alternative)

If you prefer to run steps individually:

```bash
# Step A: Build token cache
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache tokens

# Step B: Build word-to-file index  
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache index

# Step C: Build n-gram frequency
go run . process -input /home/samuel/data/token -output /home/samuel/data/cache -cache ngramfreq -ngrams 15
```

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
| `-type` | `text` | Processing type: `text`, `token`, or `lowercase` |
| `-multi` | `100` | Number of concurrent workers |
| `-r` | `false` | Replace existing files in output |
| `-ram-limit` | (none) | Soft memory limit (e.g., `1GB`, `512MB`) |
| `-status` | `false` | Show remaining files to convert |
| `-cache` | (none) | Cache mode: `tokens`, `index`, `ngrams`, or `ngramfreq` |
| `-ngrams` | `15` | Max n-gram size |

## Flags (analyze command)

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | (required) | Input directory with token files |
| `-output` | (required) | Output cache directory |
| `-ngrams` | `15` | Max n-gram size for frequency analysis |

## Flags (ngramfiles command)

| Flag | Default | Description |
|------|---------|-------------|
| `-cache` | (required) | Cache directory containing ngram files |
| `-ngrams` | `15` | Max n-gram size |

## Processing Types

### `text` (default)
Preserves the original text formatting including newlines, tabs, and special characters.

### `token`
Cleans the extracted text:
- Removes newlines, tabs, special characters
- Keeps only letters, numbers, and spaces

### `lowercase`
Same as `token` but also converts everything to lowercase. Best for case-insensitive analysis.

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
