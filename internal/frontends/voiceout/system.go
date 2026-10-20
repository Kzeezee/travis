package voiceout

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// windowsSpeechScript is fixed text; the line to speak arrives on stdin, never inside the script.
const windowsSpeechScript = "[Console]::InputEncoding = [Text.Encoding]::UTF8; " +
	"Add-Type -AssemblyName System.Speech; " +
	"(New-Object System.Speech.Synthesis.SpeechSynthesizer).Speak([Console]::In.ReadToEnd())"

// speechProgram is an OS speech program that reads the line to speak from stdin. Model text
// never goes into args: an argument starting with "-" would be read as a flag (§5.11).
type speechProgram struct {
	name string
	args []string
}

// listSpeechPrograms names each OS's built-in speech programs, most preferred first.
func listSpeechPrograms(goos string) []speechProgram {
	switch goos {
	case "darwin":
		return []speechProgram{{name: "say"}} // with no message, say reads stdin
	case "linux":
		return []speechProgram{{name: "espeak-ng", args: []string{"--stdin"}}, {name: "espeak", args: []string{"--stdin"}}}
	case "windows":
		return []speechProgram{{name: "powershell", args: []string{"-NoProfile", "-NonInteractive", "-Command", windowsSpeechScript}}}
	default:
		return nil
	}
}

// BuildSystemSpeaker returns a speaker backed by this OS's built-in speech program. No such
// program installed is ErrUnsupported, so the caller can turn voice off and say why.
func BuildSystemSpeaker() (Speaker, error) {
	programs := listSpeechPrograms(runtime.GOOS)
	var names []string
	for _, program := range programs {
		path, err := exec.LookPath(program.name)
		if err == nil {
			return programSpeaker{path: path, args: program.args}, nil
		}
		names = append(names, program.name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("voice output on %s: %w", runtime.GOOS, core.ErrUnsupported)
	}
	return nil, fmt.Errorf("voice output needs one of %s installed: %w", strings.Join(names, ", "), core.ErrUnsupported)
}

type programSpeaker struct {
	path string
	args []string
}

func (s programSpeaker) Speak(ctx context.Context, text string) error {
	// #nosec G204 -- path is a fixed program name resolved by LookPath and args are constants; the text goes on stdin.
	cmd := exec.CommandContext(ctx, s.path, s.args...)
	cmd.Stdin = strings.NewReader(text)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", s.path, err, strings.TrimSpace(string(output)))
	}
	return nil
}
