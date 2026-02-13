package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileTransaction provides atomic multi-file operations with rollback support.
// Uses a two-phase commit pattern: prepare (backup) then apply.
type FileTransaction struct {
	id         string
	operations []FileOperation
	tempDir    string
	committed  bool
	rolledBack bool
	startTime  time.Time
	mu         sync.Mutex
}

// FileOperation represents a single file operation in a transaction.
type FileOperation struct {
	Type       OperationType
	Path       string
	Content    []byte
	TempFile   string // Temp file for staged content
	BackupFile string // Backup of original for rollback
	NewPath    string // For rename operations
	Mode       os.FileMode
	Applied    bool
}

// OperationType defines the type of file operation.
type OperationType int

const (
	// OpWrite creates or overwrites a file.
	OpWrite OperationType = iota
	// OpDelete removes a file.
	OpDelete
	// OpRename moves/renames a file.
	OpRename
	// OpChmod changes file permissions.
	OpChmod
)

// String returns the operation type name.
func (t OperationType) String() string {
	switch t {
	case OpWrite:
		return "write"
	case OpDelete:
		return "delete"
	case OpRename:
		return "rename"
	case OpChmod:
		return "chmod"
	default:
		return "unknown"
	}
}

// TransactionOption configures a FileTransaction.
type TransactionOption func(*FileTransaction)

// WithID sets a custom transaction ID.
func WithID(id string) TransactionOption {
	return func(tx *FileTransaction) {
		tx.id = id
	}
}

// NewFileTransaction creates a new file transaction.
func NewFileTransaction(opts ...TransactionOption) (*FileTransaction, error) {
	tx := &FileTransaction{
		id:        fmt.Sprintf("tx-%d", time.Now().UnixNano()),
		startTime: time.Now(),
	}

	for _, opt := range opts {
		opt(tx)
	}

	tempDir, err := os.MkdirTemp("", "gokin-tx-"+tx.id+"-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	tx.tempDir = tempDir

	return tx, nil
}

// Write stages a file write operation.
func (tx *FileTransaction) Write(path string, content []byte) error {
	return tx.WriteWithMode(path, content, 0644)
}

// WriteWithMode stages a file write with specific permissions.
func (tx *FileTransaction) WriteWithMode(path string, content []byte, mode os.FileMode) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed || tx.rolledBack {
		return fmt.Errorf("transaction already finalized")
	}

	tempFile := filepath.Join(tx.tempDir, fmt.Sprintf("op-%d-write", len(tx.operations)))
	if err := os.WriteFile(tempFile, content, 0644); err != nil {
		return fmt.Errorf("failed to stage write: %w", err)
	}

	tx.operations = append(tx.operations, FileOperation{
		Type:     OpWrite,
		Path:     path,
		Content:  content,
		TempFile: tempFile,
		Mode:     mode,
	})

	return nil
}

// Delete stages a file deletion operation.
func (tx *FileTransaction) Delete(path string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed || tx.rolledBack {
		return fmt.Errorf("transaction already finalized")
	}

	tx.operations = append(tx.operations, FileOperation{
		Type: OpDelete,
		Path: path,
	})

	return nil
}

// Rename stages a file rename operation.
func (tx *FileTransaction) Rename(oldPath, newPath string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed || tx.rolledBack {
		return fmt.Errorf("transaction already finalized")
	}

	tx.operations = append(tx.operations, FileOperation{
		Type:    OpRename,
		Path:    oldPath,
		NewPath: newPath,
	})

	return nil
}

// Chmod stages a permission change operation.
func (tx *FileTransaction) Chmod(path string, mode os.FileMode) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed || tx.rolledBack {
		return fmt.Errorf("transaction already finalized")
	}

	tx.operations = append(tx.operations, FileOperation{
		Type: OpChmod,
		Path: path,
		Mode: mode,
	})

	return nil
}

// Commit applies all staged operations atomically.
func (tx *FileTransaction) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed {
		return fmt.Errorf("transaction already committed")
	}
	if tx.rolledBack {
		return fmt.Errorf("transaction was rolled back")
	}

	if len(tx.operations) == 0 {
		tx.committed = true
		tx.cleanup()
		return nil
	}

	if err := tx.backupPhase(); err != nil {
		rbErr := tx.rollbackInternal()
		return errors.Join(fmt.Errorf("backup phase failed: %w", err), rbErr)
	}

	if err := tx.applyPhase(); err != nil {
		rbErr := tx.rollbackInternal()
		return errors.Join(fmt.Errorf("apply phase failed: %w", err), rbErr)
	}

	tx.committed = true
	tx.cleanup()
	return nil
}

func (tx *FileTransaction) backupPhase() error {
	for i := range tx.operations {
		op := &tx.operations[i]
		switch op.Type {
		case OpWrite, OpDelete, OpRename, OpChmod:
			if _, err := os.Stat(op.Path); err == nil {
				backupPath := filepath.Join(tx.tempDir, fmt.Sprintf("backup-%d", i))
				if err := copyFile(op.Path, backupPath); err != nil {
					return fmt.Errorf("failed to backup %s: %w", op.Path, err)
				}
				op.BackupFile = backupPath
			}
		}
	}
	return nil
}

func (tx *FileTransaction) applyPhase() error {
	for i := range tx.operations {
		op := &tx.operations[i]
		var err error
		switch op.Type {
		case OpWrite:
			err = tx.applyWrite(op)
		case OpDelete:
			err = tx.applyDelete(op)
		case OpRename:
			err = tx.applyRename(op)
		case OpChmod:
			err = tx.applyChmod(op)
		}
		if err != nil {
			return err
		}
		op.Applied = true
	}
	return nil
}

func (tx *FileTransaction) applyWrite(op *FileOperation) error {
	dir := filepath.Dir(op.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.Rename(op.TempFile, op.Path); err == nil {
		return os.Chmod(op.Path, op.Mode)
	}

	// Fallback to copy (cross-device)
	if err := copyFile(op.TempFile, op.Path); err != nil {
		return fmt.Errorf("failed to write %s: %w", op.Path, err)
	}
	return os.Chmod(op.Path, op.Mode)
}

func (tx *FileTransaction) applyDelete(op *FileOperation) error {
	if err := os.Remove(op.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete %s: %w", op.Path, err)
	}
	return nil
}

func (tx *FileTransaction) applyRename(op *FileOperation) error {
	dir := filepath.Dir(op.NewPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.Rename(op.Path, op.NewPath); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", op.Path, op.NewPath, err)
	}
	return nil
}

func (tx *FileTransaction) applyChmod(op *FileOperation) error {
	if err := os.Chmod(op.Path, op.Mode); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", op.Path, err)
	}
	return nil
}

// Rollback undoes all applied operations.
func (tx *FileTransaction) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed {
		return fmt.Errorf("cannot rollback committed transaction")
	}
	if tx.rolledBack {
		return nil
	}

	return tx.rollbackInternal()
}

func (tx *FileTransaction) rollbackInternal() error {
	var errs []error
	for i := len(tx.operations) - 1; i >= 0; i-- {
		op := &tx.operations[i]
		if !op.Applied {
			continue
		}

		switch op.Type {
		case OpWrite:
			if op.BackupFile != "" {
				if err := copyFile(op.BackupFile, op.Path); err != nil {
					errs = append(errs, fmt.Errorf("rollback write restore %s: %w", op.Path, err))
				}
			} else {
				if err := os.Remove(op.Path); err != nil {
					errs = append(errs, fmt.Errorf("rollback write remove %s: %w", op.Path, err))
				}
			}
		case OpDelete:
			if op.BackupFile != "" {
				if err := copyFile(op.BackupFile, op.Path); err != nil {
					errs = append(errs, fmt.Errorf("rollback delete restore %s: %w", op.Path, err))
				}
			}
		case OpRename:
			if err := os.Rename(op.NewPath, op.Path); err != nil {
				errs = append(errs, fmt.Errorf("rollback rename %s: %w", op.Path, err))
			}
		case OpChmod:
			if op.BackupFile != "" {
				if info, err := os.Stat(op.BackupFile); err == nil {
					if err := os.Chmod(op.Path, info.Mode()); err != nil {
						errs = append(errs, fmt.Errorf("rollback chmod %s: %w", op.Path, err))
					}
				}
			}
		}
	}

	tx.rolledBack = true
	tx.cleanup()
	return errors.Join(errs...)
}

func (tx *FileTransaction) cleanup() {
	if tx.tempDir != "" {
		_ = os.RemoveAll(tx.tempDir)
		tx.tempDir = ""
	}
}

// ID returns the transaction ID.
func (tx *FileTransaction) ID() string {
	return tx.id
}

// OperationCount returns the number of staged operations.
func (tx *FileTransaction) OperationCount() int {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return len(tx.operations)
}

// IsFinalized returns true if the transaction is committed or rolled back.
func (tx *FileTransaction) IsFinalized() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.committed || tx.rolledBack
}

// Duration returns how long the transaction has been open.
func (tx *FileTransaction) Duration() time.Duration {
	return time.Since(tx.startTime)
}

// GetOperations returns a copy of the operations for inspection.
func (tx *FileTransaction) GetOperations() []FileOperation {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	ops := make([]FileOperation, len(tx.operations))
	copy(ops, tx.operations)
	return ops
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return os.WriteFile(dst, data, 0644)
	}
	return os.WriteFile(dst, data, info.Mode())
}

// TransactionResult contains information about a completed transaction.
type TransactionResult struct {
	ID             string
	Committed      bool
	RolledBack     bool
	Duration       time.Duration
	OperationCount int
	FilesModified  []string
}

// Result returns a summary of the transaction.
func (tx *FileTransaction) Result() TransactionResult {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	var files []string
	for _, op := range tx.operations {
		files = append(files, op.Path)
		if op.NewPath != "" {
			files = append(files, op.NewPath)
		}
	}

	return TransactionResult{
		ID:             tx.id,
		Committed:      tx.committed,
		RolledBack:     tx.rolledBack,
		Duration:       time.Since(tx.startTime),
		OperationCount: len(tx.operations),
		FilesModified:  files,
	}
}
