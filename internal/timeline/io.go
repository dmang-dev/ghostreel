// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
)

// Marshal encodes a timeline as binary protobuf.
func Marshal(tl *pb.Timeline) ([]byte, error) { return proto.Marshal(tl) }

// MarshalJSON encodes a timeline as indented protobuf JSON, for debugging.
func MarshalJSON(tl *pb.Timeline) ([]byte, error) {
	return protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(tl)
}

// Unmarshal decodes a binary timeline and checks its schema version.
func Unmarshal(data []byte) (*pb.Timeline, error) {
	tl := &pb.Timeline{}
	if err := proto.Unmarshal(data, tl); err != nil {
		return nil, err
	}
	if tl.SchemaVersion != pb.SchemaVersion_SCHEMA_VERSION_1 {
		return nil, fmt.Errorf("unsupported timeline schema version %d", tl.SchemaVersion)
	}
	return tl, nil
}

// ReadFile loads a binary timeline.
func ReadFile(path string) (*pb.Timeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Unmarshal(data)
}
