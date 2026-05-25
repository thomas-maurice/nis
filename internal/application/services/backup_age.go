package services

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// parseAgeRecipient routes a wire-form public key to the right age parser.
// Supports:
//   - age1<bech32>          → age.X25519Recipient (the default age form)
//   - ssh-ed25519 <pubkey>  → agessh ed25519 recipient
//   - ssh-rsa <pubkey>      → agessh rsa recipient (also accepted by agessh)
//
// Returns ErrInvalidAgeRecipient (with the underlying parse error wrapped)
// for anything else. age does not ship a one-call dispatcher; we do the
// prefix routing here so the rest of the codebase has one entry point.
func parseAgeRecipient(s string) (age.Recipient, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidAgeRecipient)
	}
	switch {
	case strings.HasPrefix(s, "age1"):
		r, err := age.ParseX25519Recipient(s)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidAgeRecipient, err)
		}
		return r, nil
	case strings.HasPrefix(s, "ssh-"):
		r, err := agessh.ParseRecipient(s)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidAgeRecipient, err)
		}
		return r, nil
	default:
		return nil, fmt.Errorf("%w: unknown recipient form (expected age1... or ssh-...)", ErrInvalidAgeRecipient)
	}
}

// P15 — per-operator age recipient management and the encrypt-step helper
// used by RunBackup. Lives in its own file so backup_service.go stays
// focused on the scheduled-run lifecycle.

// AddRecipient stores a new age recipient public key under one operator.
// The pubkey is validated via age.ParseRecipient (which accepts both
// `age1<bech32>` X25519 and `ssh-ed25519 ...` SSH forms); malformed input
// surfaces as ErrInvalidAgeRecipient → CodeInvalidArgument at the handler.
//
// Duplicate (operator, pubkey) returns repositories.ErrAlreadyExists, which
// the handler maps to CodeAlreadyExists. The unique index in
// operator_age_recipients enforces this at schema level.
//
// addedByUserID is the api_user from authctx — nil for system callers
// (recipient seeded via migration tooling, etc.). Stored as FK SET NULL so
// offboarding the user does not invalidate the recipient.
func (s *BackupService) AddRecipient(ctx context.Context, operatorID uuid.UUID, publicKey, label string, addedByUserID *uuid.UUID) (*entities.OperatorAgeRecipient, error) {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return nil, fmt.Errorf("%w: empty public key", ErrInvalidAgeRecipient)
	}
	if _, err := parseAgeRecipient(publicKey); err != nil {
		return nil, err
	}

	rec := &entities.OperatorAgeRecipient{
		ID:              uuid.New(),
		OperatorID:      operatorID,
		PublicKey:       publicKey,
		Label:           strings.TrimSpace(label),
		CreatedAt:       clock.Now(),
		CreatedByUserID: addedByUserID,
	}

	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		// Ensure the operator exists; CASCADE FK guards a non-existent op
		// but a clean NotFound is friendlier than a vendor-specific FK
		// violation message.
		if _, err := tx.OperatorRepository().GetByID(ctx, operatorID); err != nil {
			return err
		}
		if err := tx.OperatorAgeRecipientRepository().Create(ctx, rec); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorBackupRecipientAdded,
			OperatorID:   &operatorID,
			ResourceType: "operator_age_recipient",
			ResourceID:   rec.ID.String(),
			Payload: map[string]any{
				"recipient_id": rec.ID.String(),
				"label":        rec.Label,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// ListRecipients returns the active recipients for one operator, oldest-first.
// Empty slice when none configured (which the run path interprets as
// ErrNoBackupRecipients).
func (s *BackupService) ListRecipients(ctx context.Context, operatorID uuid.UUID) ([]*entities.OperatorAgeRecipient, error) {
	if _, err := s.factory.OperatorRepository().GetByID(ctx, operatorID); err != nil {
		return nil, err
	}
	return s.factory.OperatorAgeRecipientRepository().ListByOperator(ctx, operatorID)
}

// RemoveRecipient deletes one recipient row. isLastActive in the result is
// true when the deletion left zero recipients on the operator — the
// caller is expected to surface this as a warning since the next scheduled
// run will fail.
type RemoveRecipientResult struct {
	IsLastActive bool
}

func (s *BackupService) RemoveRecipient(ctx context.Context, operatorID, recipientID uuid.UUID) (*RemoveRecipientResult, error) {
	var isLast bool
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		// Confirm the recipient exists AND belongs to the named operator
		// before deleting. A cross-operator probe (recipient ID from
		// operator A, operator_id of operator B) returns ErrNotFound — the
		// handler-level permService.CanManageBackup on operator B already
		// gated the call, but this defensive check pins the per-operator
		// ownership invariant.
		rec, err := tx.OperatorAgeRecipientRepository().Get(ctx, recipientID)
		if err != nil {
			return err
		}
		if rec.OperatorID != operatorID {
			return repositories.ErrNotFound
		}
		if err := tx.OperatorAgeRecipientRepository().Delete(ctx, recipientID); err != nil {
			return err
		}
		count, err := tx.OperatorAgeRecipientRepository().CountByOperator(ctx, operatorID)
		if err != nil {
			return err
		}
		isLast = count == 0
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorBackupRecipientRemoved,
			OperatorID:   &operatorID,
			ResourceType: "operator_age_recipient",
			ResourceID:   recipientID.String(),
			Payload: map[string]any{
				"recipient_id":   recipientID.String(),
				"is_last_active": isLast,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return &RemoveRecipientResult{IsLastActive: isLast}, nil
}

// encryptForBackup encrypts plaintext with the supplied recipients using
// the age primitive. The output is the canonical age artifact (binary, not
// armored — armoring is for human paste-and-copy, S3 doesn't care).
//
// Hard rule: w.Close() MUST be called and its error checked before reading
// the buffer — Close() flushes the final partial chunk and writes the
// stream HMAC. A bare defer w.Close() swallows that error and the resulting
// truncated artifact silently fails to decrypt later.
func encryptForBackup(plaintext []byte, recipients []age.Recipient) ([]byte, error) {
	if len(recipients) == 0 {
		// Defensive — RunBackup should already have short-circuited.
		return nil, ErrNoBackupRecipients
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return nil, fmt.Errorf("age encrypt init: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		// Failure here means Close() can't recover; we still try to close
		// to release the underlying writer but ignore its error since the
		// primary failure is what the caller needs.
		_ = w.Close()
		return nil, fmt.Errorf("age encrypt write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("age encrypt finalize: %w", err)
	}
	return buf.Bytes(), nil
}

// loadRecipientsForOperator pulls the operator's pubkey list and parses each
// one to an age.Recipient. Parse failures here indicate corrupted DB rows
// — every pubkey passed through ParseRecipient at add time, so failure
// implies tampering or a future migration that changed the column shape.
// Either way, treated as unrecoverable for this run.
func (s *BackupService) loadRecipientsForOperator(ctx context.Context, operatorID uuid.UUID) ([]age.Recipient, []*entities.OperatorAgeRecipient, error) {
	rows, err := s.factory.OperatorAgeRecipientRepository().ListByOperator(ctx, operatorID)
	if err != nil {
		return nil, nil, fmt.Errorf("load recipients: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, ErrNoBackupRecipients
	}
	out := make([]age.Recipient, 0, len(rows))
	for _, r := range rows {
		parsed, err := parseAgeRecipient(r.PublicKey)
		if err != nil {
			return nil, nil, fmt.Errorf("recipient %s parse: %w", r.ID, err)
		}
		out = append(out, parsed)
	}
	return out, rows, nil
}

