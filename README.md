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

## Usage

### Basic Processing

Convert all supported files from an input directory to text files:

```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -multi 1000 -type token
```

### Check Conversion Status

See how many files of each type remain to be converted:

```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -status
```

Example output:
```
=== Conversion Status ===
Input:  /home/samuel/data/junk
Output: /home/samuel/data/token

Extension           Total  Converted  Remaining
---------------------------------------------
.pdf                  150         75         75
.docx                  50         50          0
.xlsx                  30         10         20
---------------------------------------------
TOTAL                 230        135         95
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | (required) | Input directory to process |
| `-output` | `output.txt` | Output directory for converted files |
| `-type` | `text` | Processing type: `text` (preserve formatting) or `token` (words and spaces only) |
| `-multi` | `100` | Number of concurrent workers |
| `-r` | `false` | Replace existing files in output |
| `-ram-limit` | (none) | Soft memory limit (e.g., `1GB`, `512MB`) |
| `-status` | `false` | Show remaining files to convert (no processing) |

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

MIT
