package file

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const (
	defaultReadLimit    = 1000
	maxReadResultBytes  = 128 * 1024
	maxReadContentBytes = maxReadResultBytes - 256
	readBufferSize      = 64 * 1024
)

type readPage struct {
	Content       []byte
	Hash          string
	Offset        int
	Limit         int
	ReturnedLines int
	NextOffset    int
	Partial       bool
	Reason        string
	LineCount     int
}

func readFilePage(ctx context.Context, file *os.File, offset, limit int) (readPage, error) {
	if offset < 0 {
		return readPage{}, fmt.Errorf("offset must be >= 0")
	}
	if limit <= 0 {
		return readPage{}, fmt.Errorf("limit must be >= 1")
	}

	hash := sha256.New()
	reader := bufio.NewReaderSize(file, readBufferSize)
	page := readPage{Offset: offset, Limit: limit}
	var selectedLine []byte
	lineNumber := 0
	lineSelected := false
	lineTooLong := false
	limitReached := false
	byteLimited := false

	for {
		if err := ctx.Err(); err != nil {
			return readPage{}, err
		}

		fragment, readErr := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if _, err := hash.Write(fragment); err != nil {
				return readPage{}, err
			}
		}

		if limitReached && len(fragment) > 0 && !page.Partial {
			page.Partial = true
			page.Reason = "limit"
			page.NextOffset = lineNumber
		}

		if lineNumber >= offset && page.ReturnedLines < limit && !byteLimited {
			lineSelected = true
			if !lineTooLong {
				if len(selectedLine)+len(fragment) > maxReadContentBytes {
					lineTooLong = true
				} else {
					selectedLine = append(selectedLine, fragment...)
				}
			}
		}

		lineDone := readErr == nil || readErr == io.EOF
		if lineDone && len(fragment) > 0 {
			if lineSelected {
				if lineTooLong {
					return readPage{}, fmt.Errorf("single line exceeds Read byte limit of %d bytes", maxReadContentBytes)
				}
				if page.ReturnedLines >= limit {
					limitReached = true
				} else if len(page.Content)+len(selectedLine) > maxReadContentBytes {
					byteLimited = true
					page.Partial = true
					page.Reason = "bytes"
					page.NextOffset = lineNumber
				} else {
					page.Content = append(page.Content, selectedLine...)
					page.ReturnedLines++
					if page.ReturnedLines == limit {
						limitReached = true
					}
				}
			}
			lineNumber++
			selectedLine = selectedLine[:0]
			lineSelected = false
			lineTooLong = false
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != bufio.ErrBufferFull {
			return readPage{}, readErr
		}
	}

	page.LineCount = lineNumber
	page.Hash = hex.EncodeToString(hash.Sum(nil))
	if page.Partial && page.NextOffset == 0 {
		page.NextOffset = offset + page.ReturnedLines
	}
	return page, nil
}

func formatReadPage(page readPage) string {
	if !page.Partial {
		return string(page.Content)
	}
	marker := fmt.Sprintf("[Read partial: offset=%d, returned_lines=%d, next_offset=%d, reason=%s]", page.Offset, page.ReturnedLines, page.NextOffset, page.Reason)
	if len(page.Content) == 0 {
		return marker
	}
	return string(page.Content) + marker
}
