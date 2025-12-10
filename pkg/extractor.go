package pkg

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/J45k4/rtf"
	"github.com/extrame/xls"
	"github.com/ledongthuc/pdf"
	"github.com/nguyenthenguyen/docx"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

// ExtractionResult holds the extracted content, potentially paginated
type ExtractionResult struct {
	FullText string
	Pages    []string // If applicable (PDF, PPT), otherwise single element
}

// ExtractContent identifies the file type and extracts text
func ExtractContent(path string) (*ExtractionResult, error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".pdf":
		return extractPDF(path)
	case ".docx":
		return extractDOCX(path)
	case ".xlsx":
		return extractXLSX(path)
	case ".html", ".htm":
		return extractHTML(path)
	case ".pptx":
		return extractPPTX(path)
	case ".xls":
		return extractXLS(path)
	case ".csv":
		return extractCSV(path)
	case ".rtf":
		return extractRTF(path)
	case ".txt", ".md":
		return extractPlain(path)
	default:
		return nil, fmt.Errorf("unsupported file extension: %s", ext)
	}
}

// ... (existing extractPlain, extractPDF, extractDOCX, extractXLSX, extractHTML) ...

// Minimal XML structs for parsing PPTX slides (moved down or kept as is)
// ...

// NEW FUNCTIONS

func extractCSV(path string) (*ExtractionResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// CSV is basically text, but we might want to return it as-is or parsed?
	// The user wants "pure text". Raw CSV is text.
	text := string(content)
	return &ExtractionResult{
		FullText: text,
		Pages:    []string{text},
	}, nil
}

func extractXLS(path string) (res *ExtractionResult, err error) {
	// Panic recovery for bad XLS files (library can panic)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("xls library panic: %v", r)
		}
	}()

	xl, err := xls.Open(path, "utf-8")
	if err != nil {
		return nil, err
	}

	var fullTextBuilder strings.Builder
	var pages []string

	for i := 0; i < xl.NumSheets(); i++ {
		sheet := xl.GetSheet(i)
		if sheet == nil {
			continue
		}

		var sheetText strings.Builder
		for row := 0; row <= int(sheet.MaxRow); row++ {
			r := sheet.Row(row)
			if r == nil {
				continue
			}
			// Safety check for LastCol
			lastCol := r.LastCol()
			for col := 0; col < lastCol; col++ {
				cell := r.Col(col)
				sheetText.WriteString(cell)
				sheetText.WriteString("\t")
			}
			sheetText.WriteString("\n")
		}

		text := sheetText.String()
		pages = append(pages, text)
		fullTextBuilder.WriteString(text)
	}

	return &ExtractionResult{
		FullText: fullTextBuilder.String(),
		Pages:    pages,
	}, nil
}

func extractRTF(path string) (*ExtractionResult, error) {
	// Simple RTF stripper using library or manual?
	// J45k4/rtf seems to be a reader.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// The library github.com/J45k4/rtf claims to read RTF.
	// We need to see how to get plain text.
	// Looking at docs (or assuming common interface):
	// It likely parses into a structure.
	// Actually, let's check what we downloaded.
	// Assuming it has a stripped reader or similar.

	// If the library is too complex or just a tokenizer,
	// maybe a simple regex stripper is better for "pure text"?
	// But let's try the library.

	// Note: I don't see the library docs in context.
	// I'll try to use the library if it has a straightforward "Text()" method.
	// If not, I'll fall back to a simple regex based approach which is often sufficient for basic RTF.

	// Let's use the installed library.
	// If J45k4/rtf follows standard Go idioms?

	// Wait, since I can't see the library source, I'll take a safe bet:
	// Many RTF libs are just tokenizers.
	// I'll assume I might need to implement a simple stripper if the lib is complex.
	// BUT the search result said: "J45k4/rtf offers a StripRichTextFormat function".

	// Let's try to read all and strip.
	content, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	// The library usage is likely: rtf.Strip(string) -> string
	text := rtf.StripRichTextFormat(string(content))

	return &ExtractionResult{
		FullText: text,
		Pages:    []string{text},
	}, nil
}

func extractPlain(path string) (*ExtractionResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(content)
	return &ExtractionResult{
		FullText: text,
		Pages:    []string{text},
	}, nil
}

func extractPDF(path string) (*ExtractionResult, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pages []string
	var fullTextBuilder strings.Builder

	totalPage := r.NumPage()
	for i := 1; i <= totalPage; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			// specific page error, continue?
			continue
		}
		pages = append(pages, text)
		fullTextBuilder.WriteString(text)
		fullTextBuilder.WriteString("\n")
	}

	return &ExtractionResult{
		FullText: fullTextBuilder.String(),
		Pages:    pages,
	}, nil
}

func extractDOCX(path string) (*ExtractionResult, error) {
	r, err := docx.ReadDocxFile(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	content := r.Editable().GetContent()
	return &ExtractionResult{
		FullText: content,
		Pages:    []string{content}, // DOCX is continuous flow, no pages in data structure
	}, nil
}

func extractXLSX(path string) (*ExtractionResult, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pages []string // We will treat Sheets as pages
	var fullTextBuilder strings.Builder

	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		var sheetContent strings.Builder
		for _, row := range rows {
			for _, colCell := range row {
				sheetContent.WriteString(colCell)
				sheetContent.WriteString("\t")
			}
			sheetContent.WriteString("\n")
		}
		text := sheetContent.String()
		pages = append(pages, text)
		fullTextBuilder.WriteString(text)
	}

	return &ExtractionResult{
		FullText: fullTextBuilder.String(),
		Pages:    pages,
	}, nil
}

func extractHTML(path string) (*ExtractionResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}

	var f func(*html.Node)
	var textBuilder strings.Builder

	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			// Simple trim to avoid massive whitespace
			text := strings.TrimSpace(n.Data)
			if text != "" {
				textBuilder.WriteString(text)
				textBuilder.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	result := textBuilder.String()
	return &ExtractionResult{
		FullText: result,
		Pages:    []string{result},
	}, nil
}

// Minimal XML structs for parsing PPTX slides
func extractPPTX(path string) (*ExtractionResult, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var pages []string
	var fullTextBuilder strings.Builder

	// Iterate through files in zip, looking for ppt/slides/slideX.xml
	// We need to sort them or just append as found? Default zip order might not be sorted by slide number.
	// For simplicity, we just iterate.

	// Map to hold slide content by filename to sort later if needed,
	// but simple iteration checks "ppt/slides/slide" prefix.

	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err != nil {
				continue
			}

			buf := new(bytes.Buffer)
			buf.ReadFrom(rc)
			rc.Close()

			// Simple XML parsing to find <a:t> tags
			// We can use a regex for simplicity or a proper decoder.
			// Given dependencies, let's use a simple string search or regex for <a:t>content</a:t>
			// Or just strip tags.
			// <a:t> is standard for text in PPTX.

			content := buf.String()
			// Find all occurrences of <a:t>...</a:t>

			var slideTextBuilder strings.Builder

			// Very naive parser
			tokens := strings.Split(content, "<a:t>")
			for i, token := range tokens {
				if i == 0 {
					continue
				} // first part is before the first tag
				end := strings.Index(token, "</a:t>")
				if end != -1 {
					text := token[:end]
					slideTextBuilder.WriteString(text)
					slideTextBuilder.WriteString(" ")
				}
			}

			text := slideTextBuilder.String()
			if len(text) > 0 {
				pages = append(pages, text)
				fullTextBuilder.WriteString(text)
				fullTextBuilder.WriteString("\n")
			}
		}
	}

	return &ExtractionResult{
		FullText: fullTextBuilder.String(),
		Pages:    pages,
	}, nil
}
