package broker_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// These bytes are the frozen protocol (D-19). If a change here fails, the change breaks every
// broker already built against that version: add a new op or field under a new ProtocolVersion.
const (
	// goldenOpenFileRequestV1 is pinned to literal version 1 forever, proving the v1 byte
	// shape never changes even though the live ProtocolVersion has since moved on.
	goldenOpenFileRequestV1 = "0000003384a176cd0001a26964cf0000000000000007a26f70a96f70656e5f66696c65a470617468ae6e6f7465732f746f6461792e6d64"
	// goldenOpenFileRequestV2 is the same request at the current protocol version, used to
	// prove ReadRequest still accepts it; a future version bump needs its own golden pair.
	goldenOpenFileRequestV2      = "0000003384a176cd0002a26964cf0000000000000007a26f70a96f70656e5f66696c65a470617468ae6e6f7465732f746f6461792e6d64"
	goldenAddDecisionRuleRequest = "0000008587a176cd0002a26964cf000000000000000aa26f70b16164645f6465636973696f6e5f72756c65a972756c655f6b696e64a866735f7772697465aa72756c655f73636f7065b12f772f6e6f7465732f746f6461792e6d64ad72756c655f6465636973696f6ea5616c6c6f77ab72756c655f736f75726365ad7573657220617070726f766564"
	goldenOpenFileResponse       = "0000006283a26964cf0000000000000007a4696e666f83a470617468b12f772f6e6f7465732f746f6461792e6d64ae776f726b73706163655f70617468ae6e6f7465732f746f6461792e6d64a46b696e64a466696c65a668616e646c65cf0000000000000001"
	goldenErrorResponse          = "0000003082a26964cf0000000000000008a56572726f7282a4636f6465a664656e696564a76d657373616765a76f757473696465"
)

var (
	sampleOpenFileRequestV1      = broker.Request{Version: 1, ID: 7, Op: broker.OpOpenFile, Path: "notes/today.md"}
	sampleOpenFileRequestV2      = broker.Request{Version: 2, ID: 7, Op: broker.OpOpenFile, Path: "notes/today.md"}
	sampleAddDecisionRuleRequest = broker.BuildDecisionRuleRequest(10, core.DecisionRule{
		Kind: core.CapabilityFSWrite, Scope: "/w/notes/today.md", Decision: core.DecisionAllow, Source: "user approved",
	})
	sampleOpenFileResponse = broker.Response{
		ID:     7,
		Info:   &broker.PathInfo{Path: "/w/notes/today.md", WorkspacePath: "notes/today.md", Kind: "file"},
		Handle: 1,
	}
	sampleErrorResponse = broker.Response{ID: 8, Error: &broker.ErrorBody{Code: broker.ErrorDenied, Message: "outside"}}
)

func encodeFrameHex(t *testing.T, write func(io.Writer) error) string {
	t.Helper()
	var frame bytes.Buffer
	if err := write(&frame); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(frame.Bytes())
}

func decodeHex(t *testing.T, encoded string) []byte {
	t.Helper()
	data, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWireBytesStayFrozen(t *testing.T) {
	cases := []struct {
		name   string
		golden string
		write  func(io.Writer) error
	}{
		{"open_file request v1", goldenOpenFileRequestV1, func(w io.Writer) error { return broker.WriteRequest(w, sampleOpenFileRequestV1) }},
		{"open_file request v2", goldenOpenFileRequestV2, func(w io.Writer) error { return broker.WriteRequest(w, sampleOpenFileRequestV2) }},
		{"add_decision_rule request", goldenAddDecisionRuleRequest, func(w io.Writer) error { return broker.WriteRequest(w, sampleAddDecisionRuleRequest) }},
		{"open_file response", goldenOpenFileResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleOpenFileResponse) }},
		{"error response", goldenErrorResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleErrorResponse) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeFrameHex(t, tc.write); got != tc.golden {
				t.Fatalf("frame = %s\nwant    %s", got, tc.golden)
			}
		})
	}
}

// TestWireDecodesTheFrozenBytes decodes a request at the live ProtocolVersion: a v1-only
// request is no longer accepted once the broker speaks v2 (see "other version" below),
// so this exercises the version the broker actually speaks today.
func TestWireDecodesTheFrozenBytes(t *testing.T) {
	req, err := broker.ReadRequest(bytes.NewReader(decodeHex(t, goldenOpenFileRequestV2)))
	if err != nil || req != sampleOpenFileRequestV2 {
		t.Fatalf("ReadRequest = %+v, %v; want %+v", req, err, sampleOpenFileRequestV2)
	}

	ruleReq, err := broker.ReadRequest(bytes.NewReader(decodeHex(t, goldenAddDecisionRuleRequest)))
	if err != nil || ruleReq != sampleAddDecisionRuleRequest {
		t.Fatalf("ReadRequest(add_decision_rule) = %+v, %v; want %+v", ruleReq, err, sampleAddDecisionRuleRequest)
	}
	rule, err := broker.ConvertDecisionRuleRequest(ruleReq)
	if err != nil || rule.Kind != core.CapabilityFSWrite || rule.Scope != "/w/notes/today.md" || rule.Decision != core.DecisionAllow {
		t.Fatalf("ConvertDecisionRuleRequest = %+v, %v", rule, err)
	}

	resp, err := broker.ReadResponse(bytes.NewReader(decodeHex(t, goldenOpenFileResponse)))
	if err != nil || resp.ID != 7 || resp.Handle != 1 || resp.Info == nil || *resp.Info != *sampleOpenFileResponse.Info {
		t.Fatalf("ReadResponse = %+v, %v", resp, err)
	}
}

func TestWireCarriesFileDataAndEndOfFile(t *testing.T) {
	var stream bytes.Buffer
	sent := broker.Response{ID: 9, Data: []byte("line one\n\x00binary"), IsEOF: true}
	if err := broker.WriteResponse(&stream, sent); err != nil {
		t.Fatal(err)
	}
	got, err := broker.ReadResponse(&stream)
	if err != nil || !bytes.Equal(got.Data, sent.Data) || !got.IsEOF {
		t.Fatalf("ReadResponse = %+v, %v", got, err)
	}
}

func TestReadRequestRejectsWhatTheBrokerMustNotActOn(t *testing.T) {
	cases := map[string]struct {
		req  broker.Request
		kind error
	}{
		"other version":                    {broker.Request{Version: 3, ID: 1, Op: broker.OpOpenFile, Path: "a"}, core.ErrUnsupported},
		"unknown op":                       {broker.Request{Version: 2, ID: 1, Op: "exec", Path: "a"}, core.ErrInvalid},
		"path op without path":             {broker.Request{Version: 2, ID: 1, Op: broker.OpDescribePath}, core.ErrInvalid},
		"read without handle":              {broker.Request{Version: 2, ID: 1, Op: broker.OpReadFile, MaxBytes: 10}, core.ErrInvalid},
		"read beyond chunk limit":          {broker.Request{Version: 2, ID: 1, Op: broker.OpReadFile, Handle: 1, MaxBytes: broker.MaxReadChunkBytes + 1}, core.ErrInvalid},
		"close without handle":             {broker.Request{Version: 2, ID: 1, Op: broker.OpCloseFile}, core.ErrInvalid},
		"add_decision_rule missing fields": {broker.Request{Version: 2, ID: 1, Op: broker.OpAddDecisionRule}, core.ErrInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var frame bytes.Buffer
			if err := broker.WriteRequest(&frame, tc.req); err != nil {
				t.Fatal(err)
			}
			if _, err := broker.ReadRequest(&frame); !errors.Is(err, tc.kind) {
				t.Fatalf("ReadRequest error = %v, want %v", err, tc.kind)
			}
		})
	}
}

func buildRawFrame(payload []byte) *bytes.Reader {
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	return bytes.NewReader(append(frame, payload...))
}

func TestReadRequestRejectsMalformedFrames(t *testing.T) {
	unknownField, err := msgpack.Marshal(map[string]any{"v": 1, "id": 1, "op": "open_file", "path": "a", "token": "x"})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := msgpack.Marshal(sampleOpenFileRequestV2)
	if err != nil {
		t.Fatal(err)
	}
	oversize := binary.BigEndian.AppendUint32(nil, broker.MaxFrameBytes+1)

	cases := map[string]io.Reader{
		"unknown field":    buildRawFrame(unknownField),
		"trailing data":    buildRawFrame(append(valid, 0xc0)),
		"empty frame":      buildRawFrame(nil),
		"oversize header":  bytes.NewReader(oversize),
		"truncated frame":  bytes.NewReader(binary.BigEndian.AppendUint32(nil, 10)),
		"not msgpack data": buildRawFrame([]byte{0xc1}),
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := broker.ReadRequest(frame); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("ReadRequest error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestWireErrorCodesKeepTheirCoreKind(t *testing.T) {
	for _, kind := range []error{core.ErrDenied, core.ErrNotFound, core.ErrIsDirectory, core.ErrInvalid, core.ErrUnsupported} {
		body := broker.BuildErrorBody(errors.Join(errors.New("detail"), kind))
		if err := broker.ConvertErrorBody(*body); !errors.Is(err, kind) {
			t.Errorf("%v → %s → %v; kind lost", kind, body.Code, err)
		}
	}
	body := broker.BuildErrorBody(errors.New("disk on fire"))
	if body.Code != broker.ErrorFailed || body.Message != "disk on fire" {
		t.Fatalf("BuildErrorBody(plain) = %+v", body)
	}
}

func TestWirePathInfoRejectsUnknownKinds(t *testing.T) {
	info := core.PathInfo{Path: "/w/a", WorkspacePath: "a", Kind: core.PathKindFile}
	if got, err := broker.ConvertPathInfoMessage(broker.BuildPathInfoMessage(info)); err != nil || got != info {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if _, err := broker.ConvertPathInfoMessage(broker.PathInfo{Kind: "device"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("ConvertPathInfoMessage(device) error = %v, want ErrInvalid", err)
	}
}
