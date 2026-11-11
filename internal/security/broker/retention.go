package broker

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

// sweepGraceWindow protects two races the sweep cannot otherwise see: a session between
// filestore.Open and WriteSessionLivenessMarker (startup gap), and a blob written by
// blobs.Snapshot just before the log entry that references it is durably appended
// (§5.15.1's write ordering). Either leaves a file briefly "expired-looking" but in use, so
// nothing this fresh is ever swept regardless of what the liveness marker says.
const sweepGraceWindow = 15 * time.Minute

const sweepLockName = ".sweep.lock"

// reportSessionLivenessMarkerPath is where a session records the PID of the travis-broker that
// owns its log, so a later sweep can tell a session that merely looks old from one still running.
func reportSessionLivenessMarkerPath(sessionsDir string, sessionID core.ID) string {
	return filepath.Join(sessionsDir, string(sessionID)+".lock")
}

// WriteSessionLivenessMarker records pid as the broker owning sessionID's log. It is written
// once, as the first thing travis-broker does after opening the log, and is never removed on
// shutdown: only SweepExpiredSessions removes it, atomically with the session it belongs to,
// once it has independently confirmed that pid is no longer running. Relying on the marker's
// absence as a liveness signal would be unsafe — shutdown is not atomic — so its absence is
// read the same as a session that simply predates this feature: neither alive nor dead, just
// unknown, and the sweep falls back to the log's own mtime plus the grace window.
func WriteSessionLivenessMarker(sessionsDir string, sessionID core.ID, pid int) error {
	file, err := openPrivateFileNoFollow(reportSessionLivenessMarkerPath(sessionsDir, sessionID))
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Write([]byte(strconv.Itoa(pid))); err != nil {
		return err
	}
	return file.Sync()
}

// reportMarkedLivePID reads a session's liveness marker; ok is false when there is none,
// which is not the same as the session being dead (see WriteSessionLivenessMarker).
func reportMarkedLivePID(sessionsDir string, sessionID core.ID) (pid int, ok bool) {
	data, err := os.ReadFile(reportSessionLivenessMarkerPath(sessionsDir, sessionID))
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// ReportExpiredSessionIDs names every session in sessionsDir whose log is past retention and
// is not still in use. A session is kept, regardless of age, when its liveness marker names a
// pid isAlive reports as running, or when its log or marker was written within sweepGraceWindow
// of now. now and isAlive are parameters so tests can move the clock and fake liveness without
// a real retention period or a real process.
func ReportExpiredSessionIDs(sessionsDir string, retention time.Duration, now time.Time, isAlive func(int) bool) ([]core.ID, error) {
	ids, err := filestore.ListSessionIDs(sessionsDir)
	if err != nil {
		return nil, err
	}
	var expired []core.ID
	for _, id := range ids {
		isExpired, err := reportSessionIsExpired(sessionsDir, id, retention, now, isAlive)
		if err != nil {
			return nil, err
		}
		if isExpired {
			expired = append(expired, id)
		}
	}
	return expired, nil
}

func reportSessionIsExpired(sessionsDir string, id core.ID, retention time.Duration, now time.Time, isAlive func(int) bool) (bool, error) {
	if pid, ok := reportMarkedLivePID(sessionsDir, id); ok && isAlive(pid) {
		return false, nil
	}
	logInfo, err := os.Stat(filepath.Join(sessionsDir, string(id)+".jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if now.Sub(logInfo.ModTime()) < sweepGraceWindow {
		return false, nil
	}
	if markerInfo, err := os.Stat(reportSessionLivenessMarkerPath(sessionsDir, id)); err == nil && now.Sub(markerInfo.ModTime()) < sweepGraceWindow {
		return false, nil
	}
	return now.Sub(logInfo.ModTime()) >= retention, nil
}

// SweepReport counts what one SweepExpiredSessions call actually removed.
type SweepReport struct {
	ExpiredSessions        int
	DeletedBlobs           int
	SkippedConcurrentSweep bool
}

// SweepExpiredSessions deletes session logs past retention and then garbage-collects blobs no
// remaining log refers to (D-32: concurrent sessions are legal, so nothing here may assume it
// is the only process touching StateDir). retention == 0 disables both steps. A non-blocking
// try-lock on <StateDir>/.sweep.lock means a sweep from another workspace running at the same
// moment is a cheap no-op rather than redundant work against the same shared directories.
func SweepExpiredSessions(stateDir string, retention time.Duration, clock core.Clock) (SweepReport, error) {
	if retention <= 0 {
		return SweepReport{}, nil
	}
	// StateDir itself may not exist yet (nothing has run long enough to create it), unlike its
	// sessions/ and blobs/ subdirs, which filestore.Open and blobs.Open each create lazily.
	if err := os.MkdirAll(stateDir, privateStateDirMode); err != nil {
		return SweepReport{}, err
	}
	lockFile, err := os.OpenFile(filepath.Join(stateDir, sweepLockName), os.O_CREATE|os.O_RDWR, privateStateFileMode)
	if err != nil {
		return SweepReport{}, err
	}
	defer lockFile.Close()
	held, err := tryLockExclusive(lockFile)
	if err != nil {
		return SweepReport{}, err
	}
	if !held {
		return SweepReport{SkippedConcurrentSweep: true}, nil
	}
	defer func() { _ = unlockExclusive(lockFile) }()

	sessionsDir := filepath.Join(stateDir, "sessions")
	now := clock.ReportCurrentTime()
	expired, err := ReportExpiredSessionIDs(sessionsDir, retention, now, IsProcessAlive)
	if err != nil {
		return SweepReport{}, err
	}
	for _, id := range expired {
		if err := deleteSessionFiles(sessionsDir, id); err != nil {
			return SweepReport{}, err
		}
	}

	retainedRefs, err := reportRetainedBlobRefs(sessionsDir)
	if err != nil {
		return SweepReport{}, err
	}
	deletedBlobs, err := sweepUnreferencedBlobs(filepath.Join(stateDir, "blobs"), retainedRefs, now)
	if err != nil {
		return SweepReport{}, err
	}
	return SweepReport{ExpiredSessions: len(expired), DeletedBlobs: deletedBlobs}, nil
}

func deleteSessionFiles(sessionsDir string, id core.ID) error {
	for _, suffix := range []string{".jsonl", ".lock"} {
		if err := os.Remove(filepath.Join(sessionsDir, string(id)+suffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// reportRetainedBlobRefs collects every blob ref any surviving session's log still points to.
func reportRetainedBlobRefs(sessionsDir string) (map[blobs.Ref]bool, error) {
	ids, err := filestore.ListSessionIDs(sessionsDir)
	if err != nil {
		return nil, err
	}
	refs := make(map[blobs.Ref]bool)
	for _, id := range ids {
		entries, err := filestore.ReadEntries(sessionsDir, id)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.FileChange != nil && entry.FileChange.Before != "" {
				refs[blobs.Ref(entry.FileChange.Before)] = true
			}
		}
	}
	return refs, nil
}

// sweepUnreferencedBlobs removes blobs under blobsDir that are not in retained and are older
// than sweepGraceWindow. A shard entry that buildBlobPath-style validation would reject is left
// untouched and merely skipped: only names the blob store itself could have produced are ever
// candidates for deletion.
func sweepUnreferencedBlobs(blobsDir string, retained map[blobs.Ref]bool, now time.Time) (int, error) {
	shards, err := os.ReadDir(blobsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	deleted := 0
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		shardPath := filepath.Join(blobsDir, shard.Name())
		entries, err := os.ReadDir(shardPath)
		if err != nil {
			return deleted, err
		}
		for _, entry := range entries {
			ref := blobs.Ref(entry.Name())
			if !isValidBlobRef(ref) || retained[ref] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return deleted, err
			}
			if now.Sub(info.ModTime()) < sweepGraceWindow {
				continue
			}
			if err := os.Remove(filepath.Join(shardPath, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

// isValidBlobRef mirrors blobs.Store's own buildBlobPath validation (a 64-char hex SHA-256),
// duplicated here rather than exported: a GC sweep deleting a name it merely thinks looks
// right would be the one place a validation bug turns into real data loss.
func isValidBlobRef(ref blobs.Ref) bool {
	if len(ref) != 64 {
		return false
	}
	for _, c := range string(ref) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
