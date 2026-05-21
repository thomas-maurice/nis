// Package services — JWTExpirySweeper periodically reconciles user-JWT state
// against the per-operator JWT lifecycle policy (P2).
//
// Three phases per tick, in this order:
//
//  1. Prune. Revocations whose JWTExp has elapsed are marked pruned and the
//     parent account JWT is regenerated without them, then pushed. NATS would
//     reject these JWTs on exp anyway, so the entry would only bloat the
//     account JWT.
//
//  2. Expiring-soon alert. User JWTs whose exp is within JWTWarnWindow get a
//     user.cred.expiring_soon event emitted once per JWT iat (dedup via
//     LastExpiringWarnIAT, which survives sweeper restarts).
//
//  3. Auto-renew. When the operator policy has JWTAutoRenew=true, the same
//     "expiring soon" set is fed to UserRevocationService.RegenerateUserJWT,
//     which mints a fresh JWT and emits user.cred.renewed. The user's holder
//     still has the old creds; the new creds are available via the
//     GetUserCredentials / RegenerateUserCredentials RPCs.
//
// Plus a fourth phase that's an alert, never a renew:
//
//  4. Expired alert. JWTs whose exp has elapsed (system was down through it,
//     or operator didn't auto-renew). One user.cred.expired event per JWT iat.
//     Auto-renew is intentionally NOT applied — silently re-signing dead
//     credentials defeats the purpose of expiry. Holder must call
//     RegenerateUserJWT explicitly.
//
// Scheduling: the recurring tick is driven by the A2 jobs substrate via
// the jwt.expiry_sweep handler (RegisterJWTExpiryHandler). Tick is also
// callable directly — OperatorHandler.RunJWTExpirySweep does so for the
// admin out-of-band sweep RPC because that RPC's contract returns the
// SweepResult counts inline. The struct's mu serialises those two
// callers; the substrate's per-type dedup_key handles row-level
// singleton in the queue.
package services

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// SweepResult is what RunJWTExpirySweep returns. Mirrors the proto message.
type SweepResult struct {
	RevocationsPruned    int
	ExpiringSoonEmitted  int
	ExpiredAlertsEmitted int
	AutoRenewed          int
}

// JWTExpirySweeper holds the dependencies the four-phase Tick needs.
// The recurring schedule is owned by the A2 job substrate; this struct
// is purely the Tick entry point + state.
type JWTExpirySweeper struct {
	factory       persistence.RepositoryFactory
	jwtService    *JWTService
	revocationSvc *UserRevocationService
	clusterPush   AccountJWTPusher
	batchLimit    int
	mu            sync.Mutex // serialises substrate tick vs admin out-of-band tick
}

// NewJWTExpirySweeper builds a sweeper. batchLimit caps how many rows
// a single phase processes per tick; 0 picks 500. Scheduling is owned
// by the A2 jobs substrate (see RegisterJWTExpiryHandler).
func NewJWTExpirySweeper(
	factory persistence.RepositoryFactory,
	jwtService *JWTService,
	revocationSvc *UserRevocationService,
	clusterPush AccountJWTPusher,
	batchLimit int,
) *JWTExpirySweeper {
	if batchLimit <= 0 {
		batchLimit = 500
	}
	return &JWTExpirySweeper{
		factory:       factory,
		jwtService:    jwtService,
		revocationSvc: revocationSvc,
		clusterPush:   clusterPush,
		batchLimit:    batchLimit,
	}
}

// Tick runs one sweep pass. Called by the jwt.expiry_sweep job handler
// on the configured schedule, and by OperatorHandler.RunJWTExpirySweep
// for the admin out-of-band sweep RPC. The mutex serialises those two
// callers.
func (w *JWTExpirySweeper) Tick(ctx context.Context) (SweepResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	log := logging.GetLogger()
	now := clock.Now()
	var result SweepResult

	pruned, err := w.prunePhase(ctx, now)
	if err != nil {
		log.Error("jwt expiry sweeper: prune phase failed", "error", err)
		return result, err
	}
	result.RevocationsPruned = pruned

	warned, renewed, err := w.expiringSoonPhase(ctx, now)
	if err != nil {
		log.Error("jwt expiry sweeper: expiring-soon phase failed", "error", err)
		return result, err
	}
	result.ExpiringSoonEmitted = warned
	result.AutoRenewed = renewed

	expired, err := w.expiredPhase(ctx, now)
	if err != nil {
		log.Error("jwt expiry sweeper: expired phase failed", "error", err)
		return result, err
	}
	result.ExpiredAlertsEmitted = expired

	if result.RevocationsPruned+result.ExpiringSoonEmitted+result.ExpiredAlertsEmitted+result.AutoRenewed > 0 {
		log.Info("jwt expiry sweeper tick",
			"pruned", result.RevocationsPruned,
			"expiring_soon", result.ExpiringSoonEmitted,
			"expired_alerts", result.ExpiredAlertsEmitted,
			"auto_renewed", result.AutoRenewed,
		)
	}
	return result, nil
}

// prunePhase removes revocations whose JWTExp <= now from the active list and
// re-signs the parent account JWT without them, then pushes to clusters.
func (w *JWTExpirySweeper) prunePhase(ctx context.Context, now time.Time) (int, error) {
	prunable, err := w.factory.UserJWTRevocationRepository().ListPrunable(ctx, now, w.batchLimit)
	if err != nil {
		return 0, err
	}
	if len(prunable) == 0 {
		return 0, nil
	}

	// Group by account so we do one tx + one push per account, not one per row.
	byAccount := make(map[uuid.UUID][]*entities.UserJWTRevocation)
	for _, rv := range prunable {
		byAccount[rv.AccountID] = append(byAccount[rv.AccountID], rv)
	}

	totalPruned := 0
	for accountID, rows := range byAccount {
		var account *entities.Account
		err := w.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
			accountRepo := tx.AccountRepository()
			operatorRepo := tx.OperatorRepository()
			scopedKeyRepo := tx.ScopedSigningKeyRepository()
			revRepo := tx.UserJWTRevocationRepository()

			acc, err := accountRepo.GetByID(ctx, accountID)
			if err != nil {
				return err
			}
			operator, err := operatorRepo.GetByID(ctx, acc.OperatorID)
			if err != nil {
				return err
			}

			ids := make([]uuid.UUID, len(rows))
			for i, rv := range rows {
				ids[i] = rv.ID
			}
			if err := revRepo.MarkPruned(ctx, ids, now); err != nil {
				return err
			}

			scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, acc.ID, repositories.ListOptions{Limit: 1000})
			if err != nil {
				return err
			}
			activeRevs, err := revRepo.ListActiveByAccount(ctx, acc.ID)
			if err != nil {
				return err
			}
			newJWT, err := w.jwtService.GenerateAccountJWT(ctx, acc, operator, scopedKeys, activeRevs, operator.AccountJWTTTL)
			if err != nil {
				return err
			}
			acc.JWT = newJWT
			acc.UpdatedAt = now
			if err := accountRepo.Update(ctx, acc); err != nil {
				return err
			}

			if err := events.EmitTx(ctx, tx, events.Event{
				Type:         entities.EventTypeUserRevocationPruned,
				OperatorID:   &acc.OperatorID,
				AccountID:    &acc.ID,
				ResourceType: "account",
				ResourceID:   acc.ID.String(),
				Payload:      map[string]any{"count": len(rows)},
			}); err != nil {
				return err
			}

			account = acc
			return nil
		})
		if err != nil {
			logging.GetLogger().Error("jwt sweeper prune: account update failed", "account", accountID, "error", err)
			continue
		}
		totalPruned += len(rows)
		metrics.Default().RecordUserJWTRevocationPruned(ctx, len(rows))

		if account != nil && w.clusterPush != nil {
			if pushErrs := w.clusterPush.PushAccountToAllClusters(ctx, account.OperatorID, account); len(pushErrs) > 0 {
				log := logging.GetLogger()
				for _, e := range pushErrs {
					log.Warn("jwt sweeper prune: account push failed", "account", e.AccountName, "error", e.Error)
				}
			}
		}
	}
	return totalPruned, nil
}

// expiringSoonPhase finds JWTs inside the warn window, emits the alert event
// (dedup via LastExpiringWarnIAT), and optionally auto-renews when the
// operator policy has JWTAutoRenew=true. To handle the per-operator warn
// window we iterate operators and let the SQL filter do the heavy lifting.
func (w *JWTExpirySweeper) expiringSoonPhase(ctx context.Context, now time.Time) (warned, renewed int, err error) {
	operators, err := w.factory.OperatorRepository().List(ctx, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return 0, 0, err
	}

	for _, op := range operators {
		if op.UserJWTTTL <= 0 {
			// No TTL = no exp on user JWTs = no expiring-soon events possible.
			continue
		}
		warnWindow := op.JWTWarnWindow
		if warnWindow <= 0 {
			warnWindow = 14 * 24 * time.Hour
		}

		users, listErr := w.factory.UserRepository().ListForExpirySweep(ctx, repositories.ExpirySweepKindExpiringSoon, now, warnWindow, w.batchLimit)
		if listErr != nil {
			return warned, renewed, listErr
		}

		for _, u := range users {
			// Skip users not owned by this operator. ListForExpirySweep does not
			// filter by operator since the warn-window is per-operator and we'd
			// have to do per-operator scans regardless. Quick account lookup is
			// the lightest filter.
			acc, err := w.factory.AccountRepository().GetByID(ctx, u.AccountID)
			if err != nil {
				continue
			}
			if acc.OperatorID != op.ID {
				continue
			}

			if err := w.emitExpiringSoon(ctx, u, acc, &op.ID); err != nil {
				logging.GetLogger().Error("jwt sweeper: emit expiring_soon failed", "user", u.ID, "error", err)
				continue
			}
			warned++
			metrics.Default().RecordUserJWTExpiringSoonEvent(ctx)

			if op.JWTAutoRenew {
				if _, err := w.revocationSvc.RegenerateUserJWT(ctx, u.ID); err != nil {
					metrics.Default().RecordUserJWTAutoRenewal(ctx, "err")
					logging.GetLogger().Error("jwt sweeper: auto-renew failed", "user", u.ID, "error", err)
					continue
				}
				renewed++
				metrics.Default().RecordUserJWTAutoRenewal(ctx, "ok")
			}
		}
	}
	return warned, renewed, nil
}

func (w *JWTExpirySweeper) emitExpiringSoon(ctx context.Context, u *entities.User, acc *entities.Account, operatorID *uuid.UUID) error {
	return w.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		// Re-read inside tx to make the dedup check atomic with the update.
		fresh, err := tx.UserRepository().GetByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if fresh.JWTIssuedAt == nil {
			return nil
		}
		if fresh.LastExpiringWarnIAT != nil && fresh.LastExpiringWarnIAT.Equal(*fresh.JWTIssuedAt) {
			return nil // already alerted for this iat
		}

		fresh.LastExpiringWarnIAT = fresh.JWTIssuedAt
		if err := tx.UserRepository().Update(ctx, fresh); err != nil {
			return err
		}

		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserCredExpiringSoon,
			OperatorID:   operatorID,
			AccountID:    &fresh.AccountID,
			ResourceType: "user",
			ResourceID:   fresh.ID.String(),
			Payload: map[string]any{
				"name":       fresh.Name,
				"public_key": fresh.PublicKey,
				"jwt_iat":    fresh.JWTIssuedAt.Unix(),
				"jwt_exp":    optionalUnix(fresh.JWTExpiresAt),
			},
		})
	})
}

// expiredPhase emits user.cred.expired alerts for JWTs already past exp.
// Intentionally does NOT auto-renew — see package doc.
func (w *JWTExpirySweeper) expiredPhase(ctx context.Context, now time.Time) (int, error) {
	// warnWindow is unused for this kind but the signature requires it.
	users, err := w.factory.UserRepository().ListForExpirySweep(ctx, repositories.ExpirySweepKindExpired, now, 0, w.batchLimit)
	if err != nil {
		return 0, err
	}
	emitted := 0
	for _, u := range users {
		acc, err := w.factory.AccountRepository().GetByID(ctx, u.AccountID)
		if err != nil {
			continue
		}

		err = w.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
			fresh, err := tx.UserRepository().GetByID(ctx, u.ID)
			if err != nil {
				return err
			}
			if fresh.JWTIssuedAt == nil {
				return nil
			}
			if fresh.LastExpiredAlertIAT != nil && fresh.LastExpiredAlertIAT.Equal(*fresh.JWTIssuedAt) {
				return nil
			}
			fresh.LastExpiredAlertIAT = fresh.JWTIssuedAt
			if err := tx.UserRepository().Update(ctx, fresh); err != nil {
				return err
			}
			return events.EmitTx(ctx, tx, events.Event{
				Type:         entities.EventTypeUserCredExpired,
				OperatorID:   &acc.OperatorID,
				AccountID:    &fresh.AccountID,
				ResourceType: "user",
				ResourceID:   fresh.ID.String(),
				Payload: map[string]any{
					"name":       fresh.Name,
					"public_key": fresh.PublicKey,
					"jwt_iat":    fresh.JWTIssuedAt.Unix(),
					"jwt_exp":    optionalUnix(fresh.JWTExpiresAt),
				},
			})
		})
		if err != nil {
			logging.GetLogger().Error("jwt sweeper: emit expired failed", "user", u.ID, "error", err)
			continue
		}
		emitted++
		metrics.Default().RecordUserJWTExpiredEvent(ctx)
	}
	return emitted, nil
}
