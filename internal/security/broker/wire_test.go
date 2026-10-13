package broker_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// These bytes are the frozen protocol (D-19). If a change here fails, the change breaks every
// broker already built against version 5: add a new op or field under a new ProtocolVersion.
// Versions 3 through 5 changed only the version a request carries; the v2 response bytes are unchanged.
const (
	goldenOpenFileRequest         = "0000003384a176cd0005a26964cf0000000000000007a26f70a96f70656e5f66696c65a470617468ae6e6f7465732f746f6461792e6d64"
	goldenOpenFileResponse        = "0000006283a26964cf0000000000000007a4696e666f83a470617468b12f772f6e6f7465732f746f6461792e6d64ae776f726b73706163655f70617468ae6e6f7465732f746f6461792e6d64a46b696e64a466696c65a668616e646c65cf0000000000000001"
	goldenWriteFileRequest        = "0000004487a176cd0005a26964cf0000000000000009a26f70aa77726974655f66696c65a470617468a5612e747874a464617461c4026869a47475726ea27431a463616c6ca26331"
	goldenAppendEntriesRequest    = "0000007584a176cd0005a26964cf000000000000000aa26f70ae617070656e645f656e7472696573a7656e74726965739182a47475726ea27431a76d657373616765c4357b22726f6c65223a2275736572222c227061727473223a5b7b226b696e64223a2274657874222c2274657874223a226869227d5d7d"
	goldenListTurnsResponse       = "0000005682a26964cf000000000000000ba57475726e739185a47475726ea27431a670726f6d7074a26869a26174d6ff6acce7e0ad6368616e6765645f706174687391a82f772f612e747874ac697272657665727369626c65c3"
	goldenRewindRequest           = "0000003385a176cd0005a26964cf000000000000000da26f70a6726577696e64a966726f6d5f7475726ea27431a46d6f6465a4626f7468"
	goldenRewindResponse          = "0000007182a26964cf000000000000000da6726577696e6485a966726f6d5f7475726ea27431a46d6f6465a4626f7468a8726573746f72656491a5612e747874a764656c6574656491a76e65772e747874a7736b69707065649182a470617468a5622e747874a6726561736f6ea76368616e676564"
	goldenIrreversibleAskResponse = "0000008982a26964cf000000000000000ca56572726f7283a4636f6465ae6e656564735f617070726f76616ca76d657373616765a3626967a8617070726f76616c84a46b696e64a866735f7772697465a6746172676574aa2f772f6269672e62696eab6578706c616e6174696f6eb063616e6e6f7420626520756e646f6e65ac697272657665727369626c65c3"
	goldenErrorResponse           = "0000003082a26964cf0000000000000008a56572726f7282a4636f6465a664656e696564a76d657373616765a76f757473696465"
)

var (
	sampleOpenFileRequest  = broker.Request{Version: broker.ProtocolVersion, ID: 7, Op: broker.OpOpenFile, Path: "notes/today.md"}
	sampleOpenFileResponse = broker.Response{
		ID:     7,
		Info:   &broker.PathInfo{Path: "/w/notes/today.md", WorkspacePath: "notes/today.md", Kind: "file"},
		Handle: 1,
	}
	sampleErrorResponse = broker.Response{ID: 8, Error: &broker.ErrorBody{Code: broker.ErrorDenied, Message: "outside"}}

	sampleWriteFileRequest = broker.Request{
		Version: broker.ProtocolVersion, ID: 9, Op: broker.OpWriteFile, Path: "a.txt", Data: []byte("hi"), Turn: "t1", Call: "c1",
	}
	sampleAppendEntriesRequest = broker.Request{Version: broker.ProtocolVersion, ID: 10, Op: broker.OpAppendEntries, Entries: []broker.EntryMessage{
		{Turn: "t1", Message: []byte(`{"role":"user","parts":[{"kind":"text","text":"hi"}]}`)},
	}}
	sampleListTurnsResponse = broker.Response{ID: 11, Turns: []broker.TurnSummaryMessage{
		{Turn: "t1", Prompt: "hi", At: time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC), ChangedPaths: []string{"/w/a.txt"}, HasIrreversible: true},
	}}
	sampleRewindRequest  = broker.Request{Version: broker.ProtocolVersion, ID: 13, Op: broker.OpRewind, FromTurn: "t1", Mode: "both"}
	sampleRewindResponse = broker.Response{ID: 13, Rewind: &broker.RewindReportMessage{
		FromTurn: "t1", Mode: "both", Restored: []string{"a.txt"}, Deleted: []string{"new.txt"},
		Skipped: []broker.SkippedFileMessage{{Path: "b.txt", Reason: "changed"}},
	}}
	sampleIrreversibleAskResponse = broker.Response{ID: 12, Error: &broker.ErrorBody{
		Code: broker.ErrorNeedsApproval, Message: "big", Approval: &broker.OperationMessage{Kind: "fs_write", Target: "/w/big.bin", Explanation: "cannot be undone", IsIrreversible: true},
	}}
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
		{"open_file request", goldenOpenFileRequest, func(w io.Writer) error { return broker.WriteRequest(w, sampleOpenFileRequest) }},
		{"open_file response", goldenOpenFileResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleOpenFileResponse) }},
		{"error response", goldenErrorResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleErrorResponse) }},
		{"write_file request", goldenWriteFileRequest, func(w io.Writer) error { return broker.WriteRequest(w, sampleWriteFileRequest) }},
		{"append_entries request", goldenAppendEntriesRequest, func(w io.Writer) error { return broker.WriteRequest(w, sampleAppendEntriesRequest) }},
		{"list_turns response", goldenListTurnsResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleListTurnsResponse) }},
		{"rewind request", goldenRewindRequest, func(w io.Writer) error { return broker.WriteRequest(w, sampleRewindRequest) }},
		{"rewind response", goldenRewindResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleRewindResponse) }},
		{"irreversible ask response", goldenIrreversibleAskResponse, func(w io.Writer) error { return broker.WriteResponse(w, sampleIrreversibleAskResponse) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeFrameHex(t, tc.write); got != tc.golden {
				t.Fatalf("frame = %s\nwant    %s", got, tc.golden)
			}
		})
	}
}

func TestWireDecodesTheFrozenBytes(t *testing.T) {
	req, err := broker.ReadRequest(bytes.NewReader(decodeHex(t, goldenOpenFileRequest)))
	if err != nil || !reflect.DeepEqual(req, sampleOpenFileRequest) {
		t.Fatalf("ReadRequest = %+v, %v; want %+v", req, err, sampleOpenFileRequest)
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
		"other version":           {broker.Request{Version: broker.ProtocolVersion + 1, ID: 1, Op: broker.OpOpenFile, Path: "a"}, core.ErrUnsupported},
		"unknown op":              {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: "exec", Path: "a"}, core.ErrInvalid},
		"path op without path":    {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpDescribePath}, core.ErrInvalid},
		"read without handle":     {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpReadFile, MaxBytes: 10}, core.ErrInvalid},
		"read beyond chunk limit": {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpReadFile, Handle: 1, MaxBytes: broker.MaxReadChunkBytes + 1}, core.ErrInvalid},
		"close without handle":    {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpCloseFile}, core.ErrInvalid},
		"write without path":      {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpWriteFile, Data: []byte("x")}, core.ErrInvalid},
		"write beyond limit":      {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpWriteFile, Path: "a", Data: make([]byte, broker.MaxWriteFileBytes+1)}, core.ErrInvalid},
		"approve without target":  {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpApproveOnce}, core.ErrInvalid},
		"rule op without rule":    {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpAddDecisionRule}, core.ErrInvalid},
		"version 3 client":        {broker.Request{Version: broker.ProtocolVersion - 1, ID: 1, Op: broker.OpOpenFile, Path: "a"}, core.ErrUnsupported},
		"append without entries":  {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpAppendEntries}, core.ErrInvalid},
		"rewind without a turn":   {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpRewind, Mode: "both"}, core.ErrInvalid},
		"rewind without a mode":   {broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpRewind, FromTurn: "t1"}, core.ErrInvalid},
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
	valid, err := msgpack.Marshal(sampleOpenFileRequest)
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

func TestWireCarriesApprovalsAndDecisionRules(t *testing.T) {
	op := core.Operation{Kind: core.CapabilityFSWrite, Target: "/w/a.txt", Explanation: "ask: untrusted step"}
	var required *core.ApprovalRequiredError
	if err := broker.ConvertErrorBody(*broker.BuildErrorBody(&core.ApprovalRequiredError{Operation: op})); !errors.As(err, &required) || required.Operation != op {
		t.Fatalf("needs_approval round trip = %v, want the operation back", err)
	}
	if err := broker.ConvertErrorBody(broker.ErrorBody{Code: broker.ErrorNeedsApproval}); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("needs_approval without an operation = %v, want ErrDenied", err)
	}

	rule := core.DecisionRule{Kind: core.CapabilityFSWrite, Scope: "/w/a.txt", Decision: core.DecisionAllow, Source: "modal"}
	var frame bytes.Buffer
	req := broker.Request{Version: broker.ProtocolVersion, ID: 1, Op: broker.OpAddDecisionRule, Rule: broker.BuildDecisionRuleMessage(rule)}
	if err := broker.WriteRequest(&frame, req); err != nil {
		t.Fatal(err)
	}
	decoded, err := broker.ReadRequest(&frame)
	if err != nil {
		t.Fatal(err)
	}
	// A zero AddedAt must stay zero, so the receiving broker stamps it with its own clock.
	if got := broker.ConvertDecisionRuleMessage(*decoded.Rule); got != rule {
		t.Fatalf("decision rule round trip = %+v, want %+v", got, rule)
	}
}
