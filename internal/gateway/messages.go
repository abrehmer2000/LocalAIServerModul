package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func strictJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func validateMessages(messages []json.RawMessage) error {
	images := 0
	for _, raw := range messages {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Name    string          `json:"name,omitempty"`
		}
		if err := strictJSON(raw, &m); err != nil {
			return errors.New("invalid message")
		}
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" && m.Role != "developer" {
			return errors.New("unsupported message role")
		}
		var text string
		if json.Unmarshal(m.Content, &text) == nil && string(m.Content) != "null" {
			continue
		}
		var parts []struct {
			Type  string  `json:"type"`
			Text  *string `json:"text,omitempty"`
			Image *struct {
				URL    string `json:"url"`
				Detail string `json:"detail,omitempty"`
			} `json:"image_url,omitempty"`
		}
		if err := strictJSON(m.Content, &parts); err != nil || len(parts) == 0 {
			return errors.New("text or content parts required")
		}
		for _, p := range parts {
			switch p.Type {
			case "text":
				if p.Text == nil || p.Image != nil {
					return errors.New("invalid text part")
				}
			case "image_url":
				images++
				if images > 1 {
					return errors.New("only one image per request supported")
				}
				if m.Role != "user" || p.Image == nil || p.Text != nil {
					return errors.New("invalid image part")
				}
				if p.Image.Detail != "" && p.Image.Detail != "auto" && p.Image.Detail != "low" && p.Image.Detail != "high" {
					return errors.New("invalid image detail")
				}
				if err := validateImage(p.Image.URL); err != nil {
					return err
				}
			default:
				return errors.New("unsupported content part")
			}
		}
	}
	return nil
}

func validateImage(url string) error {
	prefix, payload, ok := strings.Cut(url, ",")
	if !ok {
		return errors.New("embedded image required")
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(prefix, "data:"), ";base64")
	if prefix != "data:"+mime+";base64" || (mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" && mime != "image/gif") {
		return errors.New("unsupported embedded image type")
	}
	if len(payload) > base64.StdEncoding.EncodedLen(8<<20) {
		return errors.New("image exceeds 8 MiB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || len(data) == 0 || len(data) > 8<<20 || strings.ContainsAny(payload, "\r\n") {
		return errors.New("invalid image base64")
	}
	// Check the file signature without decoding pixels (avoids decompression bombs).
	valid := false
	switch mime {
	case "image/png":
		valid = bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		valid = bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
	case "image/gif":
		valid = bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
	case "image/webp":
		valid = len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	}
	if !valid {
		return errors.New("image signature differs from MIME type")
	}
	return nil
}

func hasImage(messages []json.RawMessage) bool {
	for _, raw := range messages {
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw, &m)
		var parts []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(m.Content, &parts) == nil {
			for _, p := range parts {
				if p.Type == "image_url" {
					return true
				}
			}
		}
	}
	return false
}

// JSON permits escaped UTF-16; reject isolated surrogate code units rather than
// letting encoding/json silently substitute a replacement character.
func validUnicodeEscapes(raw []byte) bool {
	hex := func(b byte) int {
		switch {
		case b >= '0' && b <= '9':
			return int(b - '0')
		case b >= 'a' && b <= 'f':
			return int(b-'a') + 10
		case b >= 'A' && b <= 'F':
			return int(b-'A') + 10
		}
		return -1
	}
	unit := func(i int) int {
		if i+4 > len(raw) {
			return -1
		}
		v := 0
		for _, b := range raw[i : i+4] {
			n := hex(b)
			if n < 0 {
				return -1
			}
			v = v*16 + n
		}
		return v
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != 92 {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		v := unit(i + 1)
		if v < 0 {
			return false
		}
		i += 4
		if v >= 0xD800 && v <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1] != 92 || raw[i+2] != 'u' {
				return false
			}
			v = unit(i + 3)
			if v < 0xDC00 || v > 0xDFFF {
				return false
			}
			i += 6
		} else if v >= 0xDC00 && v <= 0xDFFF {
			return false
		}
	}
	return true
}
