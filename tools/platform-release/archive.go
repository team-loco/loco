package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf16"
)

// encoded sorts object keys and emits ASCII without HTML escaping.
func encoded(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("encode release document: %w", err))
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if checkErr := decoder.Decode(&normalized); checkErr != nil {
		panic(checkErr)
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if checkErr := encoder.Encode(normalized); checkErr != nil {
		panic(fmt.Errorf("encode release document: %w", checkErr))
	}
	var ascii strings.Builder
	for _, r := range strings.TrimSuffix(out.String(), "\n") {
		switch {
		case r < 128:
			ascii.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&ascii, `\u%04x`, r)
		default:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&ascii, `\u%04x\u%04x`, hi, lo)
		}
	}
	return []byte(ascii.String())
}

func digest(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }

func archive(files map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		data := files[name]
		if checkErr := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); checkErr != nil {
			return nil, checkErr
		}
		if _, checkErr := tw.Write(data); checkErr != nil {
			return nil, checkErr
		}
	}
	if checkErr := tw.Close(); checkErr != nil {
		return nil, checkErr
	}
	if checkErr := gz.Close(); checkErr != nil {
		return nil, checkErr
	}
	return out.Bytes(), nil
}

func archiveFiles(data []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[header.Name] = body
	}
}

func content(data []byte) (string, error) {
	files, err := archiveFiles(data)
	if err != nil {
		return "", err
	}
	hashes := map[string]string{}
	for name, body := range files {
		hashes[name] = digest(body)
	}
	return digest(encoded(hashes)), nil
}
