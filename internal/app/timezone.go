package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/proxygate/proxygate/internal/storage"
)

// applyTimezone loads the configured timezone into time.Local. It is applied at
// startup so dashboard day boundaries and log timestamps use the operator's
// timezone; changing the setting requires a restart.
func applyTimezone(ctx context.Context, store *storage.Store, log *slog.Logger) {
	raw, ok, err := store.GetSetting(ctx, "timezone")
	if err != nil || !ok || raw == "" {
		return
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		log.Warn("invalid timezone setting; keeping system timezone", "timezone", raw, "error", err)
		return
	}
	time.Local = loc
	log.Info("timezone applied", "timezone", raw)
}
