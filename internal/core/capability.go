package core

type CapabilityKind string

const (
	CapabilityFSRead       CapabilityKind = "fs_read"
	CapabilityFSWrite      CapabilityKind = "fs_write"
	CapabilityExec         CapabilityKind = "exec"
	CapabilityNetworkHTTP  CapabilityKind = "net_http"
	CapabilityAudioCapture CapabilityKind = "audio_capture"
	CapabilityAudioPlay    CapabilityKind = "audio_play"
	CapabilitySecret       CapabilityKind = "secret"
)

type Capability struct {
	Kind  CapabilityKind `json:"kind"`
	Scope string         `json:"scope"`
}
