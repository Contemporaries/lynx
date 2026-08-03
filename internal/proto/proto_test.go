package proto

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var b bytes.Buffer
	w := &Framer{RW: &b}
	want := []byte("hello")
	if err := w.WriteFrame(FrameProxyData, want); err != nil {
		t.Fatal(err)
	}
	r := &Framer{RW: &b}
	typ, got, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if typ != FrameProxyData {
		t.Fatalf("type=%x", typ)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestFlowPayloadRoundTrip(t *testing.T) {
	payload := EncodeFlowPayload(42, []byte("data"))
	id, data, err := DecodeFlowPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 || string(data) != "data" {
		t.Fatalf("id=%d data=%q", id, data)
	}
}
