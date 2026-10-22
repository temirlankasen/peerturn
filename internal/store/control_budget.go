package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
)

// Control and expired admissions have a separate grant bucket, so exhausted
// data budgets cannot suppress receipts and expired traffic cannot bypass limits.
func chargeControl(ctx context.Context, tx *sql.Tx, g grant.Grant, now int64) error {
	var start, minute, floor int64
	var used, messages uint64
	err := tx.QueryRowContext(ctx, `SELECT control_start,control_used,control_minute,control_messages,control_clock FROM grants WHERE id=?`, g.GrantID).Scan(&start, &used, &minute, &messages, &floor)
	if err != nil {
		return lookupError(err)
	}
	if now < floor {
		return failure(fault.BudgetExceeded)
	}
	if now-minute >= 60 {
		minute, messages = now, 0
	}
	refill := uint64(max(0, now-start) / 6)
	if refill >= used {
		used = 0
	} else {
		used -= refill
	}
	if used >= g.Limits.MaxBurst || messages >= g.Limits.MaxMessagesPerMinute {
		return failure(fault.BudgetExceeded)
	}
	_, err = tx.ExecContext(ctx, `UPDATE grants SET control_start=?,control_used=?,control_minute=?,control_messages=?,control_clock=? WHERE id=?`, now, used+1, minute, messages+1, now, g.GrantID)
	return databaseError(err)
}
