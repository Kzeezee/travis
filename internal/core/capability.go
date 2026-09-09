package core

type CapabilityKind string

const (
	CapabilityFSRead       CapabilityKind = "fs.read"
	CapabilityFSWrite      CapabilityKind = "fs.write"
	CapabilityExec         CapabilityKind = "exec"
	CapabilityNetworkHTTP  CapabilityKind = "net.http"
	CapabilityAudioCapture CapabilityKind = "audio.capture"
	CapabilityAudioPlay    CapabilityKind = "audio.play"
	CapabilitySecret       CapabilityKind = "secret"
)

type Capability struct {
	Kind  CapabilityKind `json:"kind"`
	Scope string         `json:"scope"`
}
