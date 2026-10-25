package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// Registry names the public package registry a launch command installs from (DESIGN.md §5.7).
type Registry string

const (
	RegistryNPM  Registry = "npm"
	RegistryPyPI Registry = "pypi"
)

// PackageRef is the registry package a server's launch command runs, at an exact version.
type PackageRef struct {
	Registry Registry
	Name     string
	Version  string
}

// Provenance is what the registry says about a package before the user agrees to run it.
// Zero values mean the registry did not say; WeeklyDownloads is -1 when unknown.
type Provenance struct {
	Ref             PackageRef
	Publisher       string
	PublishedAt     time.Time
	WeeklyDownloads int
	DependencyCount int
	Integrity       string
}

var (
	exactNPMVersion  = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	exactPyPIVersion = regexp.MustCompile(`^\d+(\.\d+)*((a|b|rc|\.post|\.dev)\d+)*$`)
)

// ResolvePackageRef reports the registry package command runs. A command that is not an
// npx or uvx launch, such as a local binary, runs no registry package and reports false.
// A registry launch without an exact version is an error: "latest" can change under you (§5.7).
func ResolvePackageRef(command string, args []string) (PackageRef, bool, error) {
	runner := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(filepath.Base(command)), ".cmd"), ".exe")
	switch runner {
	case "npx":
		spec := selectPackageSpec(args, "-p", "--package")
		if spec == "" {
			return PackageRef{}, true, fmt.Errorf("npx names no package to run: %w", core.ErrInvalid)
		}
		name, version := splitNPMSpec(spec)
		if !exactNPMVersion.MatchString(version) {
			return PackageRef{}, true, fmt.Errorf("pin an exact version, such as %s@1.2.3, not %q: %w", name, spec, core.ErrInvalid)
		}
		return PackageRef{Registry: RegistryNPM, Name: name, Version: version}, true, nil
	case "uvx":
		spec := selectPackageSpec(args, "--from", "--from")
		if spec == "" {
			return PackageRef{}, true, fmt.Errorf("uvx names no package to run: %w", core.ErrInvalid)
		}
		name, version, _ := strings.Cut(strings.Replace(spec, "@", "==", 1), "==")
		if !exactPyPIVersion.MatchString(version) {
			return PackageRef{}, true, fmt.Errorf("pin an exact version, such as %s==1.2.3, not %q: %w", name, spec, core.ErrInvalid)
		}
		return PackageRef{Registry: RegistryPyPI, Name: name, Version: version}, true, nil
	default:
		return PackageRef{}, false, nil
	}
}

// selectPackageSpec is the explicit package flag's value, else the first argument that is not a flag.
func selectPackageSpec(args []string, shortFlag, longFlag string) string {
	for i, arg := range args {
		if value, ok := strings.CutPrefix(arg, longFlag+"="); ok {
			return value
		}
		if (arg == shortFlag || arg == longFlag) && i+1 < len(args) {
			return args[i+1]
		}
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// splitNPMSpec splits name@version, where a scoped name itself starts with "@".
func splitNPMSpec(spec string) (name, version string) {
	at := strings.LastIndex(spec, "@")
	if at <= 0 {
		return spec, ""
	}
	return spec[:at], spec[at+1:]
}

// maxRegistryResponseBytes bounds a registry document; npm's full documents for busy packages run to megabytes.
const maxRegistryResponseBytes = 32 << 20

// RegistryLookup fetches provenance from the public registries.
type RegistryLookup struct {
	client                                   *http.Client
	npmBaseURL, npmDownloadsURL, pypiBaseURL string
}

func BuildRegistryLookup(client *http.Client) RegistryLookup {
	return RegistryLookup{
		client:          client,
		npmBaseURL:      "https://registry.npmjs.org",
		npmDownloadsURL: "https://api.npmjs.org/downloads/point/last-week",
		pypiBaseURL:     "https://pypi.org/pypi",
	}
}

// FetchProvenance reports what ref's registry says about it. A version the registry does not
// have is an error, since installing it would fail anyway; download counts are best effort.
func (l RegistryLookup) FetchProvenance(ctx context.Context, ref PackageRef) (Provenance, error) {
	switch ref.Registry {
	case RegistryNPM:
		return l.fetchNPMProvenance(ctx, ref)
	case RegistryPyPI:
		return l.fetchPyPIProvenance(ctx, ref)
	default:
		return Provenance{}, fmt.Errorf("unknown registry %q: %w", ref.Registry, core.ErrInvalid)
	}
}

func (l RegistryLookup) fetchNPMProvenance(ctx context.Context, ref PackageRef) (Provenance, error) {
	var doc struct {
		Time     map[string]string `json:"time"`
		Versions map[string]struct {
			Dependencies map[string]string `json:"dependencies"`
			NpmUser      struct {
				Name string `json:"name"`
			} `json:"_npmUser"`
			Dist struct {
				Integrity string `json:"integrity"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err := l.fetchJSON(ctx, l.npmBaseURL+"/"+url.PathEscape(ref.Name), &doc); err != nil {
		return Provenance{}, err
	}
	version, ok := doc.Versions[ref.Version]
	if !ok {
		return Provenance{}, fmt.Errorf("npm has no version %s of %s: %w", ref.Version, ref.Name, core.ErrInvalid)
	}
	published, _ := time.Parse(time.RFC3339, doc.Time[ref.Version])
	provenance := Provenance{
		Ref: ref, Publisher: version.NpmUser.Name, PublishedAt: published,
		WeeklyDownloads: -1, DependencyCount: len(version.Dependencies), Integrity: version.Dist.Integrity,
	}
	var downloads struct {
		Downloads int `json:"downloads"`
	}
	if err := l.fetchJSON(ctx, l.npmDownloadsURL+"/"+ref.Name, &downloads); err == nil {
		provenance.WeeklyDownloads = downloads.Downloads
	}
	return provenance, nil
}

func (l RegistryLookup) fetchPyPIProvenance(ctx context.Context, ref PackageRef) (Provenance, error) {
	var doc struct {
		Info struct {
			Author       string   `json:"author"`
			Maintainer   string   `json:"maintainer"`
			RequiresDist []string `json:"requires_dist"`
		} `json:"info"`
		URLs []struct {
			UploadTime string `json:"upload_time_iso_8601"`
			Digests    struct {
				SHA256 string `json:"sha256"`
			} `json:"digests"`
		} `json:"urls"`
	}
	if err := l.fetchJSON(ctx, l.pypiBaseURL+"/"+url.PathEscape(ref.Name)+"/"+url.PathEscape(ref.Version)+"/json", &doc); err != nil {
		return Provenance{}, err
	}
	provenance := Provenance{Ref: ref, Publisher: doc.Info.Author, WeeklyDownloads: -1}
	if provenance.Publisher == "" {
		provenance.Publisher = doc.Info.Maintainer
	}
	for _, requirement := range doc.Info.RequiresDist {
		if !strings.Contains(requirement, "extra ==") { // optional extras are not installed by default
			provenance.DependencyCount++
		}
	}
	if len(doc.URLs) > 0 {
		provenance.PublishedAt, _ = time.Parse(time.RFC3339, doc.URLs[0].UploadTime)
		if sum := doc.URLs[0].Digests.SHA256; sum != "" {
			provenance.Integrity = "sha256-" + sum
		}
	}
	return provenance, nil
}

func (l RegistryLookup) fetchJSON(ctx context.Context, address string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("registry request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := l.client.Do(request)
	if err != nil {
		return fmt.Errorf("reach %s: %w: %w", request.URL.Host, core.ErrTransient, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s does not know this package: %w", request.URL.Host, core.ErrInvalid)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s: %w", request.URL.Host, response.Status, core.ErrTransient)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxRegistryResponseBytes)).Decode(into); err != nil {
		return fmt.Errorf("read %s's answer: %w", request.URL.Host, err)
	}
	return nil
}
