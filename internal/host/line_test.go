package host

import (
	"bufio"
	"strings"
	"testing"
)

// endless is a client that sends one line forever.
type endless struct{ n int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	e.n += len(p)
	return len(p), nil
}

// The daemon read a whole line before checking its length, so one endless
// line could exhaust its memory.
func TestCommandLinesAreBounded(t *testing.T) {
	src := &endless{}
	_, err := readCommand(bufio.NewReaderSize(src, 4096), 1<<20)
	if err == nil {
		t.Fatal("no error for an endless line")
	}
	if src.n > 1<<20+8192 {
		t.Fatalf("read %d bytes before giving up", src.n)
	}
	line, err := readCommand(bufio.NewReader(strings.NewReader("{\"cmd\":\"list\"}\nrest")), 1<<20)
	if err != nil || string(line) != "{\"cmd\":\"list\"}\n" {
		t.Fatalf("%q, %v", line, err)
	}
}
