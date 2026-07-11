package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecode_Roundtrip(t *testing.T) {
	req := Request{Type: "ping", ID: "1", Data: json.RawMessage(`{"key":"value"}`)}
	var buf bytes.Buffer
	if err := Encode(&buf, req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded Request
	if err := Decode(&buf, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Type != "ping" || decoded.ID != "1" {
		t.Errorf("decoded = %+v", decoded)
	}
	if !bytes.Equal(decoded.Data, req.Data) {
		t.Errorf("data mismatch: %s vs %s", decoded.Data, req.Data)
	}
}

func TestEncode_LargeMessage(t *testing.T) {
	big := strings.Repeat("x", 100000)
	resp := Response{Type: "list", ID: "1", Data: json.RawMessage(`"` + big + `"`)}
	var buf bytes.Buffer
	if err := Encode(&buf, resp); err != nil {
		t.Fatalf("encode large: %v", err)
	}
	var decoded Response
	if err := Decode(&buf, &decoded); err != nil {
		t.Fatalf("decode large: %v", err)
	}
	if string(decoded.Data) != `"`+big+`"` {
		t.Errorf("data corrupted")
	}
}

func TestEncode_TooLarge(t *testing.T) {
	big := strings.Repeat("x", MaxMessageSize+1)
	req := Request{Type: "big", ID: "1", Data: json.RawMessage(`"` + big + `"`)}
	var buf bytes.Buffer
	err := Encode(&buf, req)
	if err == nil {
		t.Error("expected error for oversized message")
	}
}

func TestDecode_TooLargeHeader(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	var req Request
	err := Decode(&buf, &req)
	if err == nil {
		t.Error("expected error for oversized header")
	}
}

func TestEncode_EmptyRequest(t *testing.T) {
	req := Request{}
	var buf bytes.Buffer
	if err := Encode(&buf, req); err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	var decoded Request
	if err := Decode(&buf, &decoded); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
}
