package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	archiveVersion = 1
	backupFile     = "baseline.yaml"
	candidateFile  = "candidate.yaml"
	journalFile    = "journal.json"
	maxJournalSize = 16 << 10
)

// Journal is the durable, local-only record of an offline prepared candidate.
// It contains paths and hashes but no YAML. Do not publish this record: paths
// and the separate snapshot files can still contain private information.
type Journal struct {
	Version         int       `json:"version"`
	Phase           string    `json:"phase"`
	CreatedAt       time.Time `json:"created_at"`
	TargetPath      string    `json:"target_path"`
	BaselineSHA256  string    `json:"baseline_sha256"`
	CandidateSHA256 string    `json:"candidate_sha256"`
}

// ArchiveStatus deliberately omits paths and proxy configuration contents.
type ArchiveStatus struct {
	Phase                 string `json:"phase"`
	BaselineSHA256        string `json:"baseline_sha256"`
	CandidateSHA256       string `json:"candidate_sha256"`
	TargetMatchesBaseline bool   `json:"target_matches_baseline"`
	ApplyAllowed          bool   `json:"apply_allowed"`
}

// PrepareArchive validates before creating a fresh private state directory.
// Both snapshots are durable before journal.json is written last as the
// completion marker. This function NEVER replaces the running configuration.
func PrepareArchive(ctx context.Context, currentPath, nextPath, stateDir string, validator Validator) (ArchiveStatus, error) {
	report, err := BuildPlan(ctx, currentPath, nextPath, validator)
	if err != nil {
		return ArchiveStatus{}, err
	}
	current, err := readDocument(currentPath)
	if err != nil {
		return ArchiveStatus{}, err
	}
	next, err := readDocument(nextPath)
	if err != nil {
		return ArchiveStatus{}, err
	}
	if current.sha256 != report.CurrentSHA256 || next.sha256 != report.CandidateSHA256 {
		return ArchiveStatus{}, errors.New("source configuration changed during preflight")
	}
	base, err := filepath.Abs(stateDir)
	if err != nil || strings.TrimSpace(stateDir) == "" {
		return ArchiveStatus{}, errors.New("invalid state directory")
	}
	if base == filepath.Dir(base) {
		return ArchiveStatus{}, errors.New("state directory cannot be a filesystem root")
	}
	target, err := filepath.Abs(currentPath)
	if err != nil {
		return ArchiveStatus{}, errors.New("invalid target path")
	}
	if err := os.Mkdir(base, 0700); err != nil {
		return ArchiveStatus{}, fmt.Errorf("state directory must be new and private: %w", err)
	}
	// Do not reuse an existing transaction, and never overwrite existing files.
	if err := writeExclusive(filepath.Join(base, backupFile), current.data, 0600); err != nil {
		return ArchiveStatus{}, fmt.Errorf("cannot stage baseline: %w", err)
	}
	staged := filepath.Join(base, candidateFile)
	if err := writeExclusive(staged, next.data, 0600); err != nil {
		return ArchiveStatus{}, fmt.Errorf("cannot stage candidate: %w", err)
	}
	// Validate the immutable private copy as well as the original preflight.
	if err := validator.Validate(ctx, staged); err != nil {
		return ArchiveStatus{}, fmt.Errorf("staged candidate validation failed: %w", err)
	}
	if err := checkDigest(staged, next.sha256); err != nil {
		return ArchiveStatus{}, err
	}
	if err := checkDigest(target, current.sha256); err != nil {
		return ArchiveStatus{}, errors.New("target changed while creating transaction")
	}
	if err := checkDigest(nextPath, next.sha256); err != nil {
		return ArchiveStatus{}, errors.New("candidate changed while creating transaction")
	}
	journal := Journal{
		Version: archiveVersion, Phase: "prepared", CreatedAt: time.Now().UTC(),
		TargetPath: target, BaselineSHA256: current.sha256,
		CandidateSHA256: next.sha256,
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return ArchiveStatus{}, err
	}
	if err := writeExclusive(filepath.Join(base, journalFile), append(data, '\n'), 0600); err != nil {
		return ArchiveStatus{}, fmt.Errorf("cannot write transaction journal: %w", err)
	}
	return ArchiveStatus{Phase: journal.Phase, BaselineSHA256: current.sha256,
		CandidateSHA256: next.sha256, TargetMatchesBaseline: true, ApplyAllowed: false}, nil
}

func writeExclusive(path string, data []byte, perm os.FileMode) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return nil
}

func checkDigest(path, expected string) error {
	doc, err := readDocument(path)
	if err != nil {
		return errors.New("archive or source file missing or invalid")
	}
	if doc.sha256 != expected {
		return errors.New("archive or source checksum mismatch")
	}
	return nil
}

// ReadArchive refuses partial or tampered journals and validates both snapshots.
// A changed live target is reported, never silently overwritten.
func ReadArchive(stateDir string) (ArchiveStatus, error) {
	j, base, err := openJournal(stateDir)
	if err != nil {
		return ArchiveStatus{}, err
	}
	if err := checkDigest(filepath.Join(base, backupFile), j.BaselineSHA256); err != nil {
		return ArchiveStatus{}, err
	}
	if err := checkDigest(filepath.Join(base, candidateFile), j.CandidateSHA256); err != nil {
		return ArchiveStatus{}, err
	}
	matches := checkDigest(j.TargetPath, j.BaselineSHA256) == nil
	return ArchiveStatus{
		Phase: j.Phase, BaselineSHA256: j.BaselineSHA256,
		CandidateSHA256:       j.CandidateSHA256,
		TargetMatchesBaseline: matches, ApplyAllowed: false,
	}, nil
}

func openJournal(stateDir string) (Journal, string, error) {
	base, err := filepath.Abs(stateDir)
	if err != nil || strings.TrimSpace(stateDir) == "" {
		return Journal{}, "", errors.New("invalid state directory")
	}
	dir, err := os.Lstat(base)
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return Journal{}, "", errors.New("state directory is missing, a link, or invalid")
	}
	path := filepath.Join(base, journalFile)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxJournalSize {
		return Journal{}, "", errors.New("transaction journal missing or incomplete")
	}
	file, err := os.Open(path)
	if err != nil {
		return Journal{}, "", errors.New("cannot read transaction journal")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxJournalSize+1))
	if err != nil || len(data) > maxJournalSize {
		return Journal{}, "", errors.New("invalid transaction journal")
	}
	var journal Journal
	if err := json.Unmarshal(data, &journal); err != nil ||
		journal.Version != archiveVersion || journal.Phase != "prepared" ||
		journal.CreatedAt.IsZero() || len(journal.BaselineSHA256) != 64 ||
		len(journal.CandidateSHA256) != 64 || !filepath.IsAbs(journal.TargetPath) {
		return Journal{}, "", errors.New("invalid transaction journal")
	}
	return journal, base, nil
}

// ExportBaseline writes a verified backup to a NEW file only. It refuses to
// overwrite any existing file, including active Mihomo configs or symlinks.
// This is manual rescue/export, not production rollback or core reload.
func ExportBaseline(stateDir, outputPath string) error {
	j, base, err := openJournal(stateDir)
	if err != nil {
		return err
	}
	backup, err := readDocument(filepath.Join(base, backupFile))
	if err != nil || backup.sha256 != j.BaselineSHA256 {
		return errors.New("baseline backup missing or corrupted")
	}
	out, err := filepath.Abs(outputPath)
	if err != nil || strings.TrimSpace(outputPath) == "" || out == j.TargetPath {
		return errors.New("output path must be a separate, new file")
	}
	if err := writeExclusive(out, backup.data, 0600); err != nil {
		return fmt.Errorf("cannot export baseline: %w", err)
	}
	return nil
}
