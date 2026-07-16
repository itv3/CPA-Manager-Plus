package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	BackupFormatVersion = 1

	BackupKindManual       = "manual"
	BackupKindPeriodic     = "periodic"
	BackupKindPreMigration = "pre_migration"
	BackupKindPreRestore   = "pre_restore"
)

type BackupOptions struct {
	Directory string
	Kind      string
	Retention int
	CreatedAt time.Time
}

type BackupManifest struct {
	FormatVersion int    `json:"formatVersion"`
	SchemaVersion int    `json:"schemaVersion"`
	CreatedAtMS   int64  `json:"createdAtMs"`
	Kind          string `json:"kind"`
	DatabaseFile  string `json:"databaseFile"`
	SizeBytes     int64  `json:"sizeBytes"`
	SHA256        string `json:"sha256"`
}

type BackupResult struct {
	DatabasePath string         `json:"databasePath"`
	ManifestPath string         `json:"manifestPath"`
	Manifest     BackupManifest `json:"manifest"`
}

type RestoreOptions struct {
	BackupPath        string
	TargetPath        string
	BackupDirectory   string
	RollbackRetention int
}

type RestoreResult struct {
	RestoredPath string        `json:"restoredPath"`
	Backup       BackupResult  `json:"backup"`
	Rollback     *BackupResult `json:"rollback,omitempty"`
}

type quarantinedFile struct {
	originalPath   string
	quarantinePath string
}

func BackupPath(ctx context.Context, sourcePath string, options BackupOptions) (BackupResult, error) {
	db, _, err := openExistingDatabase(ctx, sourcePath)
	if err != nil {
		return BackupResult{}, err
	}
	defer db.Close()
	return CreateBackup(ctx, db, options)
}

func CreateBackup(ctx context.Context, db *sql.DB, options BackupOptions) (BackupResult, error) {
	if db == nil {
		return BackupResult{}, errors.New("manager sqlite database is nil")
	}
	directory, err := filepath.Abs(strings.TrimSpace(options.Directory))
	if err != nil || strings.TrimSpace(options.Directory) == "" {
		return BackupResult{}, errors.New("backup directory is required")
	}
	kind, err := normalizeBackupKind(options.Kind)
	if err != nil {
		return BackupResult{}, err
	}
	createdAt := options.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return BackupResult{}, fmt.Errorf("create backup directory: %w", err)
	}
	if err := removeStaleBackupTemps(directory); err != nil {
		return BackupResult{}, err
	}
	schemaVersion, err := SchemaVersion(db)
	if err != nil {
		return BackupResult{}, err
	}
	if schemaVersion > CurrentSchemaVersion {
		return BackupResult{}, fmt.Errorf("cannot back up unsupported manager sqlite schema version %d", schemaVersion)
	}
	suffix, err := randomSuffix()
	if err != nil {
		return BackupResult{}, err
	}
	baseName := fmt.Sprintf(
		"usage-%s-schema-v%d-%s-%s.sqlite",
		createdAt.Format("20060102T150405.000Z"), schemaVersion, strings.ReplaceAll(kind, "_", "-"), suffix,
	)
	databasePath := filepath.Join(directory, baseName)
	manifestPath := databasePath + ".manifest.json"
	temporaryFile, err := os.CreateTemp(directory, ".cpa-backup-*.sqlite")
	if err != nil {
		return BackupResult{}, fmt.Errorf("create backup temporary file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	if err := temporaryFile.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return BackupResult{}, err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return BackupResult{}, err
	}
	defer os.Remove(temporaryPath)
	if _, err := db.ExecContext(ctx, `vacuum into ?`, temporaryPath); err != nil {
		return BackupResult{}, fmt.Errorf("create consistent sqlite backup: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return BackupResult{}, fmt.Errorf("protect sqlite backup: %w", err)
	}
	inspectedVersion, err := inspectDatabaseFile(ctx, temporaryPath, true)
	if err != nil {
		return BackupResult{}, err
	}
	if inspectedVersion != schemaVersion {
		return BackupResult{}, fmt.Errorf("backup schema version changed from %d to %d", schemaVersion, inspectedVersion)
	}
	size, digest, err := fileDigest(temporaryPath)
	if err != nil {
		return BackupResult{}, err
	}
	if err := syncFile(temporaryPath); err != nil {
		return BackupResult{}, err
	}
	if err := os.Rename(temporaryPath, databasePath); err != nil {
		return BackupResult{}, fmt.Errorf("publish sqlite backup: %w", err)
	}
	removePublishedOnFailure := true
	defer func() {
		if removePublishedOnFailure {
			_ = os.Remove(databasePath)
			_ = os.Remove(manifestPath)
		}
	}()
	manifest := BackupManifest{
		FormatVersion: BackupFormatVersion,
		SchemaVersion: schemaVersion,
		CreatedAtMS:   createdAt.UnixMilli(),
		Kind:          kind,
		DatabaseFile:  baseName,
		SizeBytes:     size,
		SHA256:        digest,
	}
	rawManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupResult{}, err
	}
	rawManifest = append(rawManifest, '\n')
	if err := writeAtomicFile(manifestPath, rawManifest, 0o600); err != nil {
		return BackupResult{}, fmt.Errorf("publish backup manifest: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return BackupResult{}, err
	}
	removePublishedOnFailure = false
	result := BackupResult{DatabasePath: databasePath, ManifestPath: manifestPath, Manifest: manifest}
	if options.Retention > 0 {
		if err := PruneBackups(directory, kind, options.Retention); err != nil {
			return result, fmt.Errorf("backup created but retention failed: %w", err)
		}
	}
	return result, nil
}

func CreateMigrationBackupIfNeeded(ctx context.Context, sourcePath string, options BackupOptions) (BackupResult, bool, error) {
	if _, err := os.Stat(sourcePath); err != nil {
		if os.IsNotExist(err) {
			return BackupResult{}, false, nil
		}
		return BackupResult{}, false, err
	}
	db, _, err := openExistingDatabase(ctx, sourcePath)
	if err != nil {
		return BackupResult{}, false, err
	}
	defer db.Close()
	version, err := SchemaVersion(db)
	if err != nil {
		return BackupResult{}, false, err
	}
	if version > CurrentSchemaVersion {
		return BackupResult{}, false, fmt.Errorf("unsupported manager sqlite schema version %d; current version is %d", version, CurrentSchemaVersion)
	}
	if version == CurrentSchemaVersion {
		return BackupResult{}, false, nil
	}
	options.Kind = BackupKindPreMigration
	result, err := CreateBackup(ctx, db, options)
	return result, true, err
}

func VerifyBackup(ctx context.Context, backupPath string) (BackupResult, error) {
	databasePath, manifestPath, manifest, err := resolveBackupPair(backupPath)
	if err != nil {
		return BackupResult{}, err
	}
	if manifest.FormatVersion != BackupFormatVersion {
		return BackupResult{}, fmt.Errorf("unsupported backup format version %d", manifest.FormatVersion)
	}
	if manifest.SchemaVersion < 0 || manifest.SchemaVersion > CurrentSchemaVersion {
		return BackupResult{}, fmt.Errorf("unsupported backup schema version %d", manifest.SchemaVersion)
	}
	if manifest.CreatedAtMS <= 0 {
		return BackupResult{}, errors.New("backup manifest has an invalid creation time")
	}
	if _, err := normalizeBackupKind(manifest.Kind); err != nil {
		return BackupResult{}, err
	}
	size, digest, err := fileDigest(databasePath)
	if err != nil {
		return BackupResult{}, err
	}
	if size != manifest.SizeBytes {
		return BackupResult{}, fmt.Errorf("backup size mismatch: got %d, want %d", size, manifest.SizeBytes)
	}
	if !strings.EqualFold(digest, manifest.SHA256) {
		return BackupResult{}, errors.New("backup sha256 mismatch")
	}
	version, err := inspectDatabaseFile(ctx, databasePath, true)
	if err != nil {
		return BackupResult{}, err
	}
	if version != manifest.SchemaVersion {
		return BackupResult{}, fmt.Errorf("backup schema version mismatch: got %d, want %d", version, manifest.SchemaVersion)
	}
	return BackupResult{DatabasePath: databasePath, ManifestPath: manifestPath, Manifest: manifest}, nil
}

func RestoreBackup(ctx context.Context, options RestoreOptions) (RestoreResult, error) {
	verified, err := VerifyBackup(ctx, options.BackupPath)
	if err != nil {
		return RestoreResult{}, err
	}
	targetPath, err := filepath.Abs(strings.TrimSpace(options.TargetPath))
	if err != nil || strings.TrimSpace(options.TargetPath) == "" {
		return RestoreResult{}, errors.New("restore target path is required")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("create restore target directory: %w", err)
	}
	var rollback *BackupResult
	if info, statErr := os.Lstat(targetPath); statErr == nil {
		if !info.Mode().IsRegular() {
			return RestoreResult{}, errors.New("restore target must be a regular file")
		}
		rollbackDirectory := strings.TrimSpace(options.BackupDirectory)
		if rollbackDirectory == "" {
			rollbackDirectory = filepath.Join(filepath.Dir(targetPath), "backups")
		}
		created, backupErr := BackupPath(ctx, targetPath, BackupOptions{
			Directory: rollbackDirectory,
			Kind:      BackupKindPreRestore,
			Retention: options.RollbackRetention,
		})
		if backupErr != nil {
			return RestoreResult{}, fmt.Errorf("create pre-restore rollback backup: %w", backupErr)
		}
		rollback = &created
	} else if !os.IsNotExist(statErr) {
		return RestoreResult{}, statErr
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(targetPath), ".cpa-restore-*.sqlite")
	if err != nil {
		return RestoreResult{}, err
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)
	backupFile, err := os.Open(verified.DatabasePath)
	if err != nil {
		_ = temporaryFile.Close()
		return RestoreResult{}, err
	}
	_, copyErr := io.Copy(temporaryFile, backupFile)
	closeBackupErr := backupFile.Close()
	if copyErr == nil {
		copyErr = closeBackupErr
	}
	if copyErr == nil {
		copyErr = temporaryFile.Sync()
	}
	if closeErr := temporaryFile.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return RestoreResult{}, fmt.Errorf("copy backup for restore: %w", copyErr)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return RestoreResult{}, err
	}
	version, err := inspectDatabaseFile(ctx, temporaryPath, true)
	if err != nil {
		return RestoreResult{}, err
	}
	if version != verified.Manifest.SchemaVersion {
		return RestoreResult{}, errors.New("restored temporary database schema version changed")
	}
	quarantinedSidecars, err := quarantineDatabaseSidecars(targetPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := replaceDatabaseFile(temporaryPath, targetPath); err != nil {
		if restoreErr := restoreQuarantinedFiles(quarantinedSidecars); restoreErr != nil {
			return RestoreResult{}, errors.Join(err, fmt.Errorf("restore sqlite sidecars after failed database replacement: %w", restoreErr))
		}
		return RestoreResult{}, err
	}
	if err := removeQuarantinedFiles(quarantinedSidecars); err != nil {
		return RestoreResult{}, fmt.Errorf("remove replaced sqlite sidecars: %w", err)
	}
	if err := syncDirectory(filepath.Dir(targetPath)); err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{RestoredPath: targetPath, Backup: verified, Rollback: rollback}, nil
}

func PruneBackups(directory string, kind string, retention int) error {
	if retention <= 0 {
		return nil
	}
	kind, err := normalizeBackupKind(kind)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type candidate struct {
		manifestPath string
		databasePath string
		createdAtMS  int64
	}
	items := make([]candidate, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sqlite.manifest.json") {
			continue
		}
		manifestPath := filepath.Join(directory, entry.Name())
		raw, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			return readErr
		}
		var manifest BackupManifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.FormatVersion != BackupFormatVersion || manifest.Kind != kind || filepath.Base(manifest.DatabaseFile) != manifest.DatabaseFile {
			continue
		}
		items = append(items, candidate{
			manifestPath: manifestPath,
			databasePath: filepath.Join(directory, manifest.DatabaseFile),
			createdAtMS:  manifest.CreatedAtMS,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].createdAtMS > items[j].createdAtMS })
	if len(items) <= retention {
		return nil
	}
	for _, item := range items[retention:] {
		if err := os.Remove(item.manifestPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(item.databasePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return syncDirectory(directory)
}

func inspectDatabaseFile(ctx context.Context, path string, requireManagerTables bool) (int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return 0, errors.New("sqlite backup is not a non-empty regular file")
	}
	db, err := sql.Open("sqlite", readOnlyDataSourceName(path))
	if err != nil {
		return 0, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	var quickCheck string
	if err := db.QueryRowContext(ctx, `pragma quick_check`).Scan(&quickCheck); err != nil {
		return 0, fmt.Errorf("run sqlite quick_check: %w", err)
	}
	if quickCheck != "ok" {
		return 0, fmt.Errorf("sqlite quick_check failed: %s", quickCheck)
	}
	version, err := SchemaVersion(db)
	if err != nil {
		return 0, err
	}
	if version > CurrentSchemaVersion {
		return 0, fmt.Errorf("unsupported manager sqlite schema version %d", version)
	}
	if requireManagerTables {
		rows, err := db.QueryContext(ctx, `select name from sqlite_schema where type = 'table' and name in (
			'settings', 'usage_events', 'pro_accounts', 'pro_account_bindings', 'pro_account_drafts'
		)`)
		if err != nil {
			return 0, err
		}
		found := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return 0, err
			}
			found[name] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		requiredTables := []string{"settings", "usage_events"}
		if version >= 1 {
			requiredTables = append(requiredTables, "pro_accounts", "pro_account_bindings", "pro_account_drafts")
		}
		for _, table := range requiredTables {
			if !found[table] {
				return 0, fmt.Errorf("sqlite backup does not contain required Manager table %s", table)
			}
		}
	}
	return version, nil
}

func openExistingDatabase(ctx context.Context, path string) (*sql.DB, string, error) {
	absolutePath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return nil, "", errors.New("sqlite database path is required")
	}
	info, err := os.Lstat(absolutePath)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, "", errors.New("sqlite database must be a non-empty regular file")
	}
	db, err := sql.Open("sqlite", dataSourceName(absolutePath))
	if err != nil {
		return nil, "", err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, "", err
	}
	return db, absolutePath, nil
}

func readOnlyDataSourceName(path string) string {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := &url.URL{Scheme: "file", Path: uriPath}
	query := dsn.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func resolveBackupPair(input string) (string, string, BackupManifest, error) {
	path, err := filepath.Abs(strings.TrimSpace(input))
	if err != nil || strings.TrimSpace(input) == "" {
		return "", "", BackupManifest{}, errors.New("backup path is required")
	}
	manifestPath := path
	if !strings.HasSuffix(path, ".manifest.json") {
		manifestPath = path + ".manifest.json"
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", "", BackupManifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	if len(raw) > 1024*1024 {
		return "", "", BackupManifest{}, errors.New("backup manifest is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var manifest BackupManifest
	if err := decoder.Decode(&manifest); err != nil {
		return "", "", BackupManifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", "", BackupManifest{}, errors.New("backup manifest contains trailing data")
	}
	if filepath.Base(manifest.DatabaseFile) != manifest.DatabaseFile || manifest.DatabaseFile == "." || manifest.DatabaseFile == "" {
		return "", "", BackupManifest{}, errors.New("backup manifest database file is invalid")
	}
	databasePath := filepath.Join(filepath.Dir(manifestPath), manifest.DatabaseFile)
	return databasePath, manifestPath, manifest, nil
}

func normalizeBackupKind(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = BackupKindManual
	}
	switch value {
	case BackupKindManual, BackupKindPeriodic, BackupKindPreMigration, BackupKindPreRestore:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported backup kind %q", value)
	}
}

func randomSuffix() (string, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func fileDigest(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cpa-manifest-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func replaceDatabaseFile(sourcePath string, targetPath string) error {
	if err := os.Rename(sourcePath, targetPath); err == nil {
		return nil
	}
	if _, err := os.Stat(targetPath); err != nil {
		return fmt.Errorf("replace sqlite database: %w", err)
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	oldPath := targetPath + ".restore-old-" + suffix
	if err := os.Rename(targetPath, oldPath); err != nil {
		return fmt.Errorf("move current sqlite database: %w", err)
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		_ = os.Rename(oldPath, targetPath)
		return fmt.Errorf("replace sqlite database: %w", err)
	}
	if err := os.Remove(oldPath); err != nil {
		return fmt.Errorf("remove replaced sqlite database: %w", err)
	}
	return nil
}

// 恢复前先隔离旧边车文件；主库替换失败时可以把它们原样放回。
func quarantineDatabaseSidecars(targetPath string) ([]quarantinedFile, error) {
	suffix, err := randomSuffix()
	if err != nil {
		return nil, err
	}
	quarantined := make([]quarantinedFile, 0, 3)
	for _, originalPath := range []string{targetPath + "-wal", targetPath + "-shm", targetPath + "-journal"} {
		info, statErr := os.Lstat(originalPath)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return rollbackQuarantinedFiles(quarantined, statErr)
		}
		if !info.Mode().IsRegular() {
			return rollbackQuarantinedFiles(quarantined, fmt.Errorf("sqlite sidecar must be a regular file: %s", originalPath))
		}
		item := quarantinedFile{
			originalPath:   originalPath,
			quarantinePath: originalPath + ".restore-old-" + suffix,
		}
		if err := os.Rename(item.originalPath, item.quarantinePath); err != nil {
			return rollbackQuarantinedFiles(quarantined, fmt.Errorf("quarantine sqlite sidecar %s: %w", originalPath, err))
		}
		quarantined = append(quarantined, item)
	}
	return quarantined, nil
}

func rollbackQuarantinedFiles(files []quarantinedFile, cause error) ([]quarantinedFile, error) {
	if restoreErr := restoreQuarantinedFiles(files); restoreErr != nil {
		return nil, errors.Join(cause, fmt.Errorf("restore sqlite sidecars after quarantine failure: %w", restoreErr))
	}
	return nil, cause
}

func restoreQuarantinedFiles(files []quarantinedFile) error {
	var restoreErr error
	for index := len(files) - 1; index >= 0; index-- {
		item := files[index]
		if err := os.Rename(item.quarantinePath, item.originalPath); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
	}
	return restoreErr
}

func removeQuarantinedFiles(files []quarantinedFile) error {
	var removeErr error
	for _, item := range files {
		if err := os.Remove(item.quarantinePath); err != nil && !os.IsNotExist(err) {
			removeErr = errors.Join(removeErr, err)
		}
	}
	return removeErr
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

func removeStaleBackupTemps(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasPrefix(entry.Name(), ".cpa-backup-") && !strings.HasPrefix(entry.Name(), ".cpa-manifest-")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
