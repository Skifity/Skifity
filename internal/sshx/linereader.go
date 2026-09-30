package sshx

import (
	"bufio"
	"io"
	"strings"
)

// lineReader turns a stream into lines, handling the carriage returns that
// progress bars from curl and the k3s installer emit. Without this, a single
// download produces one enormous "line".
type lineReader struct {
	reader *bufio.Reader
}

func newLineReader(r io.Reader) *lineReader {
	// 1 MiB is far more than any line these commands produce, and bounds what a
	// hostile or broken server can make the panel buffer.
	return &lineReader{reader: bufio.NewReaderSize(r, 1<<20)}
}

// ReadLine returns the next line, split on either newline or carriage return.
func (l *lineReader) ReadLine() (string, error) {
	var b strings.Builder
	for {
		c, err := l.reader.ReadByte()
		if err != nil {
			return strings.TrimRight(b.String(), "\r\n"), err
		}
		if c == '\n' || c == '\r' {
			line := strings.TrimRight(b.String(), "\r\n")
			if line == "" {
				// Skip the empty lines a \r\n pair produces.
				continue
			}
			return line, nil
		}
		b.WriteByte(c)
		if b.Len() > 1<<20 {
			return b.String(), nil
		}
	}
}
