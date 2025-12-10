# TokenTrove

A CLI tool for text extraction, n-gram analysis, and finding recurring text patterns across document collections.

## Quick Start (Full Workflow)

### Step 1: Convert Documents → Token Text

```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -multi 100 -type lowercase
```

Check progress:
```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -status
```

### Step 2: Build Cache (Analyze)

```bash
go run . analyze -input /home/samuel/data/token -output /home/samuel/data/cache -ngrams 15
```

This creates:
- `uniq.txt` - All unique words
- `files.txt` - All processed files  
- `2gramfreq.txt` → `15gramfreq.txt` - N-gram frequencies
- `2gram.txt` → `15gram.txt` - N-grams with file indices (for reports)

### Step 3: Launch Web Interface & Generate Reports

```bash
go run . analyze -input /home/samuel/data/token -output /home/samuel/data/cache -reports /home/samuel/data/reports -host
```

Opens `http://localhost:3000` with:
- **Dashboard** - Stats overview
- **N-gram Browser** - Browse and search n-grams (streamed from disk)
- **Reports** - Generate analysis reports:
  - **Top N-grams Summary** - Most frequent phrases
  - **Search Report** - Find all matches for a query
  - **🔥 Recurring Text Finder** - Find text that repeats across multiple files!

---

## Finding Recurring Text (The Main Feature)

The Recurring Text Finder detects **sentences and paragraphs that appear in multiple files**.

### How It Works

1. Builds n-gram chains (e.g., 10-gram + 12-gram that overlap)
2. Finds chains that appear in the **same files**
3. Shows you:
   - The full merged text
   - How many files contain it
   - Which files (expandable dropdown)

### Example Output

```
"445 twelfth street sw washington d c 20554 recorded listing of releases..."
📏 28 words | 📁 789 files (click to expand)
   ├── document1.txt
   ├── document2.txt
   └── ...787 more
```

This shows boilerplate text appearing verbatim in 789 documents!

---

## Directory Structure

```
/home/samuel/data/
├── junk/           ← Original documents (PDF, DOCX, etc.)
├── token/          ← Cleaned token text files
├── cache/          ← N-gram index and frequency files
└── reports/        ← Generated report files
```

---

## Installation

```bash
go install github.com/openfluke/tokentrove@latest
```

Or build from source:

```bash
git clone https://github.com/openfluke/tokentrove.git
cd tokentrove
go build .
```

---

## Commands

### `process` - Convert Documents

```bash
go run . process -input /home/samuel/data/junk -output /home/samuel/data/token -multi 100 -type lowercase
```

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | required | Source directory with documents |
| `-output` | required | Output directory for token files |
| `-type` | `text` | `text`, `token`, or `lowercase` |
| `-multi` | `100` | Concurrent workers |
| `-r` | `false` | Replace existing files |
| `-status` | `false` | Show conversion progress |

### `analyze` - Build Cache & Launch Web

```bash
go run . analyze -input /home/samuel/data/token -output /home/samuel/data/cache -reports /home/samuel/data/reports -ngrams 15 -host
```

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | required | Directory with token files |
| `-output` | required | Cache output directory |
| `-ngrams` | `15` | Max n-gram size |
| `-reports` | none | Reports output directory |
| `-host` | `false` | Start web server |
| `-port` | `3000` | Web server port |

---

## Processing Types

| Type | Description |
|------|-------------|
| `text` | Preserves original formatting |
| `token` | Removes special chars, keeps words/numbers/spaces |
| `lowercase` | Same as token + converts to lowercase |

## Supported Formats

PDF, DOCX, XLSX, XLS, PPTX, HTML, CSV, RTF, TXT, MD

---

## Cache Files Generated

| File | Contents |
|------|----------|
| `uniq.txt` | One unique word per line |
| `files.txt` | One file path per line |
| `settings.txt` | Input directory reference |
| `Ngramfreq.txt` | N-gram → count |
| `Ngram.txt` | N-gram → file indices (for reports) |
| `fileuniqindex.txt` | Word → file indices |

---

## License

Apache 2.0
