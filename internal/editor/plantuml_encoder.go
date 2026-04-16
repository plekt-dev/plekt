package editor

import (
	"bytes"
	"compress/flate"
	"io"
	"strings"
)

// plantumlEncoder implements PlantUMLEncoder using deflate + PlantUML's custom base64.
type plantumlEncoder struct{}

// NewPlantUMLEncoder returns a PlantUMLEncoder that produces URL-safe encoded strings
// compatible with the PlantUML server (deflate + custom base64 alphabet).
func NewPlantUMLEncoder() PlantUMLEncoder {
	return &plantumlEncoder{}
}

func (e *plantumlEncoder) Encode(src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", nil
	}

	deflated, err := deflateData([]byte(src))
	if err != nil {
		return "", ErrPlantUMLEncode
	}

	return encode64(deflated), nil
}

// deflateData compresses data using raw DEFLATE (no zlib header).
func deflateData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	// Read all compressed bytes
	out, err := io.ReadAll(&buf)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PlantUML uses a custom base64 alphabet:
// 0-9 → '0'-'9', 10-35 → 'A'-'Z', 36-61 → 'a'-'z', 62 → '-', 63 → '_'
func encode6bit(b byte) byte {
	b &= 0x3f
	if b < 10 {
		return '0' + b
	}
	b -= 10
	if b < 26 {
		return 'A' + b
	}
	b -= 26
	if b < 26 {
		return 'a' + b
	}
	b -= 26
	if b == 0 {
		return '-'
	}
	return '_'
}

// encode64 encodes bytes using PlantUML's custom base64.
func encode64(data []byte) string {
	var buf bytes.Buffer
	i := 0
	for i < len(data) {
		switch {
		case i+2 < len(data):
			encode3bytes(&buf, data[i], data[i+1], data[i+2])
			i += 3
		case i+1 < len(data):
			encode3bytes(&buf, data[i], data[i+1], 0)
			i += 2
		default:
			encode3bytes(&buf, data[i], 0, 0)
			i++
		}
	}
	return buf.String()
}

func encode3bytes(buf *bytes.Buffer, b1, b2, b3 byte) {
	c1 := b1 >> 2
	c2 := ((b1 & 0x3) << 4) | (b2 >> 4)
	c3 := ((b2 & 0xF) << 2) | (b3 >> 6)
	c4 := b3 & 0x3F
	buf.WriteByte(encode6bit(c1))
	buf.WriteByte(encode6bit(c2))
	buf.WriteByte(encode6bit(c3))
	buf.WriteByte(encode6bit(c4))
}
