package proto

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
)

// HelloVersion names the attach handshake. The host first says whether it
// holds an identity yet, "HOST concord-cli-host/2 new" or "... existing", so
// that the client knows to ask for a new passphrase twice. The client then
// sends one line, "HELLO concord-cli-host/2 <base64 passphrase>", before the
// bot protocol starts. The passphrase travels inside the transport (the mesh's
// encrypted session), never in an environment variable or argument of the
// remote process, so nothing on the remote side can read it from ps or
// /proc/*/cmdline.
const HelloVersion = "concord-cli-host/2"

const helloPrefix = "HELLO " + HelloVersion + " "

const greetPrefix = "HOST " + HelloVersion + " "

// WriteGreeting announces the host; fresh means it has no identity yet.
func WriteGreeting(w io.Writer, fresh bool) error {
	state := "existing"
	if fresh {
		state = "new"
	}
	_, err := io.WriteString(w, greetPrefix+state+"\n")
	return err
}

// ReadGreeting reads the host's announcement and reports whether it is new.
func ReadGreeting(r *bufio.Reader) (fresh bool, err error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		return false, errors.New("no greeting from the host (is concord-cli installed there, and the same version?)")
	}
	switch string(bytes.TrimRight(line, "\r\n")) {
	case greetPrefix + "new":
		return true, nil
	case greetPrefix + "existing":
		return false, nil
	}
	return false, errors.New("the host does not speak " + HelloVersion + " (is it the same version?)")
}

// WriteHello writes the handshake and zeroes its own buffers. The caller
// still owns, and should zero, pass.
func WriteHello(w io.Writer, pass []byte) error {
	enc := base64.StdEncoding
	line := make([]byte, len(helloPrefix)+enc.EncodedLen(len(pass))+1)
	defer clear(line)
	copy(line, helloPrefix)
	enc.Encode(line[len(helloPrefix):], pass)
	line[len(line)-1] = '\n'
	_, err := w.Write(line)
	return err
}

// ReadHello reads the handshake and returns the passphrase, which the caller
// must zero when done.
func ReadHello(r *bufio.Reader) ([]byte, error) {
	var line []byte
	defer func() { clear(line) }()
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > 4096 {
			return nil, errors.New("handshake line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, errors.New("no handshake from the attaching client")
		}
		break
	}
	body, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r\n"), []byte(helloPrefix))
	if !ok {
		return nil, errors.New("not a " + HelloVersion + " handshake (is the client the same version?)")
	}
	pass := make([]byte, base64.StdEncoding.DecodedLen(len(body)))
	n, err := base64.StdEncoding.Decode(pass, body)
	if err != nil || n == 0 {
		clear(pass)
		return nil, errors.New("malformed handshake")
	}
	return pass[:n], nil
}
