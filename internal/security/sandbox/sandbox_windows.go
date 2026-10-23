package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows backend: Go cannot put a child in a job before it runs, so the launcher joins the job
// itself, grants the sandbox SID its paths, then runs the target under a low-integrity restricted token.

// travisSandboxSID is the restricting SID the target carries; only ACEs granted to it pass the restricted access check.
const travisSandboxSIDString = "S-1-9-1551374245-3916806784-3135587946-1406627330"

// ponytail: fixed limits, move to Spec when config needs them
const (
	maxSandboxProcesses = 32
	maxSandboxJobMemory = 2 << 30
)

const (
	readAccessMask  = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
	writeAccessMask = readAccessMask | windows.FILE_GENERIC_WRITE | windows.DELETE

	inheritToChildren = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE

	// Flags for CreateRestrictedToken, which x/sys/windows does not wrap.
	disableMaxPrivilege = 0x1
	luaToken            = 0x4
)

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

// ResolveRunnerReadPaths grants only the executable's own folder: the restricting SID can open nothing it is not
// granted, and each grant is a persistent ACL edit, so a whole install prefix would be rewritten for good.
func ResolveRunnerReadPaths(executable string) []string {
	return []string{filepath.Dir(executable)}
}

func prepareCommandForOS(*exec.Cmd, Spec) (Tier, error) {
	return TierRestrictedToken, nil
}

func applyToSelf(spec Spec) error {
	if err := confineSelfToJob(); err != nil {
		return err
	}
	for _, path := range spec.ReadPaths {
		// A folder the user cannot re-permission, like Program Files, is left as is: skipping a grant never widens
		// access, Users can already read system installs, and a runner that truly cannot be read fails to start.
		if err := grantSandboxAccess(path, readAccessMask); err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
	}
	for _, path := range spec.WritePaths {
		if err := grantSandboxAccess(path, writeAccessMask); err != nil {
			return err
		}
		if err := labelLowIntegrity(path); err != nil {
			return err
		}
	}
	return nil
}

// confineSelfToJob never closes the job handle: the launcher dying closes it, and KILL_ON_JOB_CLOSE then ends the whole tree.
func confineSelfToJob() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create sandbox job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{JobMemoryLimit: maxSandboxJobMemory}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = maxSandboxProcesses
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("limit sandbox job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("join sandbox job: %w", err)
	}
	return nil
}

// grantSandboxAccess adds a persistent inheritable ACE for the sandbox SID, skipping paths that already have one.
func grantSandboxAccess(path string, mask windows.ACCESS_MASK) error {
	sandboxSID, err := windows.StringToSid(travisSandboxSIDString)
	if err != nil {
		return fmt.Errorf("parse sandbox sid: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read acl of %q: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read acl of %q: %w", path, err)
	}
	if hasSandboxACE(dacl, sandboxSID, mask) {
		return nil
	}
	grant := windows.EXPLICIT_ACCESS{
		AccessPermissions: mask,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sandboxSID),
		},
	}
	merged, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{grant}, dacl)
	if err != nil {
		return fmt.Errorf("build acl of %q: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, merged, nil); err != nil {
		return fmt.Errorf("grant sandbox access to %q: %w", path, err)
	}
	return nil
}

func hasSandboxACE(dacl *windows.ACL, sandboxSID *windows.SID, mask windows.ACCESS_MASK) bool {
	if dacl == nil {
		return false
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil {
			return false
		}
		isInheritableAllow := ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE &&
			ace.Header.AceFlags&inheritToChildren == inheritToChildren &&
			ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if isInheritableAllow && ace.Mask&mask == mask && aceSID.Equals(sandboxSID) {
			return true
		}
	}
	return false
}

// labelLowIntegrity lets the low-integrity target write here; everywhere else no-write-up still applies.
func labelLowIntegrity(path string) error {
	sd, err := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
	if err != nil {
		return fmt.Errorf("build low integrity label: %w", err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return fmt.Errorf("build low integrity label: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		return fmt.Errorf("label %q low integrity: %w", path, err)
	}
	return nil
}

// execTarget exits with the target's code, because Windows has no exec to hand the code through.
func execTarget(spec Spec) error {
	token, err := createSandboxToken()
	if err != nil {
		return err
	}
	defer token.Close()
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) //nolint:gosec,noctx // the target is the sandboxed argv; the job, not a context, bounds it
	cmd.Env, cmd.Dir = spec.Env, spec.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token)}
	err = cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return fmt.Errorf("start sandboxed target: %w", err)
	}
	os.Exit(cmd.ProcessState.ExitCode())
	return nil
}

// createSandboxToken restricts our own token, so no privilege is needed: Everyone and Users keep system DLLs
// readable, while files granted only to the user SID fail the restricted check.
func createSandboxToken() (windows.Token, error) {
	var self windows.Token
	access := uint32(windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_ADJUST_DEFAULT)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access, &self); err != nil {
		return 0, fmt.Errorf("open launcher token: %w", err)
	}
	defer self.Close()

	sandboxSID, err := windows.StringToSid(travisSandboxSIDString)
	if err != nil {
		return 0, fmt.Errorf("parse sandbox sid: %w", err)
	}
	restricting := []windows.SIDAndAttributes{{Sid: sandboxSID}}
	for _, wellKnown := range []windows.WELL_KNOWN_SID_TYPE{windows.WinWorldSid, windows.WinBuiltinUsersSid, windows.WinRestrictedCodeSid} {
		sid, err := windows.CreateWellKnownSid(wellKnown)
		if err != nil {
			return 0, fmt.Errorf("build restricting sid: %w", err)
		}
		restricting = append(restricting, windows.SIDAndAttributes{Sid: sid})
	}

	var restricted windows.Token
	ok, _, callErr := procCreateRestrictedToken.Call(uintptr(self), disableMaxPrivilege|luaToken,
		0, 0, 0, 0,
		uintptr(len(restricting)), uintptr(unsafe.Pointer(&restricting[0])),
		uintptr(unsafe.Pointer(&restricted)))
	if ok == 0 {
		return 0, fmt.Errorf("create restricted token: %w", callErr)
	}

	lowLabel, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		_ = restricted.Close()
		return 0, fmt.Errorf("build low integrity sid: %w", err)
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: lowLabel, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		_ = restricted.Close()
		return 0, fmt.Errorf("lower token integrity: %w", err)
	}
	if err := setSandboxDefaultDACL(self, restricted, sandboxSID); err != nil {
		_ = restricted.Close()
		return 0, err
	}
	return restricted, nil
}

// tokenDefaultDACL is TOKEN_DEFAULT_DACL, which x/sys/windows does not define.
type tokenDefaultDACL struct {
	DefaultDACL *windows.ACL
}

// setSandboxDefaultDACL lets the target open the pipes and other objects it creates: the inherited default DACL
// names only the user and SYSTEM, which fail the restricted check, so `pipe: Access is denied` (uv's interpreter
// query). Everyone is left out so other processes cannot open those objects.
func setSandboxDefaultDACL(self, restricted windows.Token, sandboxSID *windows.SID) error {
	user, err := self.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read launcher user: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("build system sid: %w", err)
	}
	grants := make([]windows.EXPLICIT_ACCESS, 0, 3)
	for _, sid := range []*windows.SID{user.User.Sid, sandboxSID, system} {
		grants = append(grants, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	dacl, err := windows.ACLFromEntries(grants, nil)
	if err != nil {
		return fmt.Errorf("build default dacl: %w", err)
	}
	defaultDACL := tokenDefaultDACL{DefaultDACL: dacl}
	if err := windows.SetTokenInformation(restricted, windows.TokenDefaultDacl,
		(*byte)(unsafe.Pointer(&defaultDACL)), uint32(unsafe.Sizeof(defaultDACL))); err != nil {
		return fmt.Errorf("set default dacl: %w", err)
	}
	return nil
}
