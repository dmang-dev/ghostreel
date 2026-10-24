// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/OWNER/ghostreel/gen/engine/v1"
)

// WriteSamples stores samples as gzip-compressed protobuf (.engine.pb.gz).
// Frames repeat most of each unit's fields, so they compress well (~10×).
func WriteSamples(path string, s *enginev1.Samples) error {
	data, err := proto.Marshal(s)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ReadSamples loads a file written by WriteSamples.
func ReadSamples(path string) (*enginev1.Samples, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s := &enginev1.Samples{}
	if err := proto.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.SchemaVersion != enginev1.SchemaVersion_SCHEMA_VERSION_1 {
		return nil, fmt.Errorf("%s: unsupported samples schema %d", path, s.SchemaVersion)
	}
	return s, nil
}
